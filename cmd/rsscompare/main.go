// cmd/rsscompare is a throwaway, manually-invoked operator diagnostic (same
// category as cmd/livecheck): it boots the full compiled uade-bot Go binary
// and the full `node src/bot.js` process side by side, measures each one's
// resident set size (RSS) idle and under a defined concurrency of HTTP
// requests against their unauthenticated dashboard /login route, and writes a
// markdown report. It exists to close the GO-08/SC7 gap identified in
// 03.3-VERIFICATION.md: the only RSS figure the phase had ever produced was a
// microbenchmark of one isolated function (BenchmarkComparator), never a
// measurement of the full running binary against the full running Node
// process. Never deployed; never exposed to any network caller or untrusted
// input -- its flags are supplied only by the operator running it directly.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/ogs/uade-bot/internal/shadow"
)

type flags struct {
	goBin             string
	nodeCwd           string
	goDB              string
	nodeDB            string
	goAddr            string
	nodePort          string
	masterKey         string
	sessionSecret     string
	dashboardPassword string
	warmup            time.Duration
	concurrency       int
	load              time.Duration
	out               string
}

func parseFlags() flags {
	f := flags{}
	flag.StringVar(&f.goBin, "go-bin", "", "path to a pre-built uade-bot binary (required)")
	flag.StringVar(&f.nodeCwd, "node-cwd", ".", "working directory to run `node src/bot.js` from")
	flag.StringVar(&f.goDB, "go-db", filepath.Join(os.TempDir(), "uade-rsscompare-go.db"), "SQLite DB path for the Go process")
	flag.StringVar(&f.nodeDB, "node-db", filepath.Join(os.TempDir(), "uade-rsscompare-node.db"), "SQLite DB path for the Node process")
	flag.StringVar(&f.goAddr, "go-addr", "127.0.0.1:18081", "listen address for the Go dashboard")
	flag.StringVar(&f.nodePort, "node-port", "18082", "listen port for the Node dashboard")
	flag.StringVar(&f.masterKey, "master-key", "", "64 hex char CREDENTIALS_MASTER_KEY shared by both processes (required)")
	flag.StringVar(&f.sessionSecret, "session-secret", "", "shared dashboard session secret, >=32 chars (required)")
	flag.StringVar(&f.dashboardPassword, "dashboard-password", "", "shared dashboard password, must not equal \"admin\" (required)")
	flag.DurationVar(&f.warmup, "warmup", 15*time.Second, "idle warmup duration before sampling idle RSS")
	flag.IntVar(&f.concurrency, "concurrency", 8, "number of concurrent goroutines issuing GET /login during the load phase")
	flag.DurationVar(&f.load, "load", 10*time.Second, "duration of the concurrency load phase")
	flag.StringVar(&f.out, "out", "internal/shadow/RSS_BASELINE.md", "path to write the markdown report to")
	flag.Parse()
	return f
}

func (f flags) validate() error {
	if f.goBin == "" {
		return fmt.Errorf("-go-bin is required")
	}
	if len(f.masterKey) != 64 {
		return fmt.Errorf("-master-key is required and must be 64 hex chars")
	}
	if len(f.sessionSecret) < 32 {
		return fmt.Errorf("-session-secret is required and must be >=32 chars")
	}
	if f.dashboardPassword == "" || f.dashboardPassword == "admin" {
		return fmt.Errorf("-dashboard-password is required and must not equal \"admin\"")
	}
	return nil
}

type sample struct {
	idleGo, idleNode           uint64
	underLoadGo, underLoadNode uint64
	nodeErr                    error
}

func main() {
	f := parseFlags()
	if err := f.validate(); err != nil {
		fmt.Fprintln(os.Stderr, "rsscompare:", err)
		flag.Usage()
		os.Exit(1)
	}
	if err := run(f); err != nil {
		fmt.Fprintln(os.Stderr, "rsscompare:", err)
		os.Exit(1)
	}
}

func run(f flags) error {
	timestamp := time.Now().UTC()

	goCmd := exec.Command(f.goBin)
	goCmd.Env = append(os.Environ(),
		"UADE_DB_PATH="+f.goDB,
		"UADE_RUNTIME_MODE=development",
		"DASHBOARD_ADDR="+f.goAddr,
		"DASHBOARD_SESSION_SECRET="+f.sessionSecret,
		"DASHBOARD_PASSWORD="+f.dashboardPassword,
		"CREDENTIALS_MASTER_KEY="+f.masterKey,
		// DISCORD_BOT_TOKEN intentionally left unset: cmd/uade-bot/main.go
		// only opens the Discord Gateway when a token is present and
		// mode!=shadow, and a network-unreachable Gateway dial would
		// log.Fatal the whole process before any RSS sample is possible.
	)
	if err := goCmd.Start(); err != nil {
		return fmt.Errorf("start go binary: %w", err)
	}
	goPID := goCmd.Process.Pid
	defer killAndWait(goCmd)

	nodeCmd := exec.Command("node", "src/bot.js")
	nodeCmd.Dir = f.nodeCwd
	nodeCmd.Env = append(os.Environ(),
		"DATABASE_PATH="+f.nodeDB,
		"CREDENTIALS_MASTER_KEY="+f.masterKey,
		"DASHBOARD_ENABLED=true",
		"DASHBOARD_PORT="+f.nodePort,
		"DASHBOARD_SESSION_SECRET="+f.sessionSecret,
		"DASHBOARD_PASSWORD="+f.dashboardPassword,
		// Placeholders only: src/config/env.js requires these non-empty, but
		// this measurement never calls client.login successfully -- the
		// dashboard's own open listening socket keeps the process alive
		// regardless (see src/bot.js's startOptionalDashboard/main comment
		// this plan's read_first cites), so no real Discord/UADE creds are
		// needed to measure the full process's idle/under-load footprint.
		"UADE_USERNAME=rsscompare-placeholder",
		"UADE_PASSWORD=rsscompare-placeholder",
		"DISCORD_BOT_TOKEN=rsscompare-placeholder-token",
		"DISCORD_CLIENT_ID=rsscompare-placeholder-id",
	)
	nodeStartErr := nodeCmd.Start()
	var nodePID int
	if nodeStartErr == nil {
		nodePID = nodeCmd.Process.Pid
		defer killAndWait(nodeCmd)
	}

	time.Sleep(f.warmup)

	s := sample{nodeErr: nodeStartErr}
	s.idleGo, _ = shadow.ProcessRSSBytes(goPID)
	if nodeStartErr == nil {
		s.idleNode, _ = shadow.ProcessRSSBytes(nodePID)
	}

	runLoad(f, goPID, nodePID, nodeStartErr == nil)

	s.underLoadGo, _ = shadow.ProcessRSSBytes(goPID)
	if nodeStartErr == nil {
		s.underLoadNode, _ = shadow.ProcessRSSBytes(nodePID)
	}

	report := renderReport(timestamp, f, s)
	if err := os.WriteFile(f.out, []byte(report), 0o644); err != nil {
		return fmt.Errorf("write report to %s: %w", f.out, err)
	}
	fmt.Println(report)
	return nil
}

// runLoad drives -concurrency goroutines issuing repeated GET /login against
// both dashboards for -load, ignoring individual request errors (a load
// phase's purpose is CPU/memory pressure, not correctness assertions).
func runLoad(f flags, goPID, nodePID int, nodeUp bool) {
	deadline := time.Now().Add(f.load)
	client := &http.Client{Timeout: 2 * time.Second}
	var wg sync.WaitGroup
	hit := func(url string) {
		defer wg.Done()
		for time.Now().Before(deadline) {
			resp, err := client.Get(url)
			if err == nil {
				resp.Body.Close()
			}
		}
	}
	for i := 0; i < f.concurrency; i++ {
		wg.Add(1)
		go hit("http://" + f.goAddr + "/login")
		if nodeUp {
			wg.Add(1)
			go hit("http://127.0.0.1:" + f.nodePort + "/login")
		}
	}
	wg.Wait()
}

// killAndWait force-terminates a spawned process and waits for exit with a
// bounded grace timer, never an unbounded Wait().
func killAndWait(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	done := make(chan struct{})
	go func() {
		_, _ = cmd.Process.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
}

func renderReport(timestamp time.Time, f flags, s sample) string {
	nodeSection := fmt.Sprintf(`- Node idle RSS: %s
- Node under-concurrency RSS: %s`, formatBytes(s.idleNode), formatBytes(s.underLoadNode))
	if s.nodeErr != nil {
		nodeSection = fmt.Sprintf("- Node process failed to start: %v (Node RSS figures not available for this run)", s.nodeErr)
	}

	return fmt.Sprintf(`# RSS Baseline: full Go binary vs full Node process

Generated by cmd/rsscompare at %s.

This documents a real, reproducible measurement of the full running uade-bot
Go binary (dashboard + scheduler + DB open) against the full running
`+"`node src/bot.js`"+` process, idle and under a defined concurrency of HTTP
requests against each side's unauthenticated dashboard `+"`GET /login`"+` route.
It sits alongside, and does not replace, the existing `+"`BenchmarkComparator`"+`
microbenchmark figure in internal/shadow/shadow_test.go (~10.8 MiB/op for one
isolated comparator function call, not a full process).

## Methodology

- Both processes are started as normal OS processes (not containers), each
  with its own SQLite DB path, dashboard listen address, and shared
  CREDENTIALS_MASTER_KEY / session secret / dashboard password.
- Warmup: %s idle, then an idle RSS sample is taken via
  `+"`shadow.ProcessRSSBytes(pid)`"+` for each process (VmRSS on Linux,
  GetProcessMemoryInfo working set on Windows).
- Load: %d concurrent goroutines repeatedly issue `+"`GET /login`"+` against
  each dashboard for %s, then a second RSS sample is taken for each process.
- Both processes are force-terminated at the end of the run with a bounded
  ~5s grace timer.

## Results

- Go idle RSS: %s
- Go under-concurrency RSS: %s
%s

## Caveats

- **Discord Gateway not engaged on either side.** The Go binary is started
  without `+"`DISCORD_BOT_TOKEN`"+` set, so `+"`cmd/uade-bot/main.go`"+` never
  attempts `+"`client.OpenGateway`"+`. The Node process is started with a
  placeholder (invalid) `+"`DISCORD_BOT_TOKEN`"+`; `+"`client.login`"+` fails, but
  `+"`src/bot.js`"+`'s `+"`main().catch`"+` only sets `+"`process.exitCode`"+`, it
  never calls `+"`process.exit()`"+`, so the process stays alive via the
  dashboard's own open listening socket. This isolates the runtime/module/
  DB/dashboard footprint of each full process rather than a live-connected
  Discord session -- consistent with the two already-open `+"`human_verification`"+`
  items in 03.3-VERIFICATION.md (GO-05 live Discord round-trip, GO-09 live
  SSO) that this measurement does not attempt to close.
- Both DBs are freshly created, empty SQLite files (no users/jobs/credentials
  seeded), so scheduler reconciliation on both sides has nothing to poll
  during the measurement window.
- This is a local, single-run, manually-invoked measurement, not a
  statistically averaged benchmark; treat absolute numbers as directional
  and re-run `+"`go run ./cmd/rsscompare`"+` for a fresh sample if needed.
`, timestamp.Format(time.RFC3339), f.warmup, f.concurrency, f.load,
		formatBytes(s.idleGo), formatBytes(s.underLoadGo), nodeSection)
}

func formatBytes(b uint64) string {
	if b == 0 {
		return "unmeasured (0)"
	}
	mib := float64(b) / (1024 * 1024)
	return fmt.Sprintf("%.1f MiB (%d bytes)", mib, b)
}
