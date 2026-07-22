package shadow

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ogs/uade-bot/internal/uade"
	_ "modernc.org/sqlite"
)

// Input contains only the immutable data shared by both implementations. A
// Runner never receives the production DB, Store or Notifier, making writes
// and notifications impossible through this shadow boundary.
type Input struct {
	FixtureID string
	Payload   []byte
}

type Runner interface {
	Run(context.Context, Input) (Result, error)
}

type Result struct {
	Outcome  uade.Outcome
	RSSBytes uint64
}

type RunnerFunc func(context.Context, Input) (Result, error)

func (f RunnerFunc) Run(ctx context.Context, input Input) (Result, error) {
	return f(ctx, input)
}

type Metrics struct {
	NodeLatencyMicros int64  `json:"nodeLatencyMicros"`
	GoLatencyMicros   int64  `json:"goLatencyMicros"`
	NodeRSSBytes      uint64 `json:"nodeRssBytes"`
	GoRSSBytes        uint64 `json:"goRssBytes"`
}

type Divergence struct {
	InputHash string           `json:"inputHash"`
	Node      uade.OutcomeCode `json:"node"`
	Go        uade.OutcomeCode `json:"go"`
	NodeHash  string           `json:"nodeResultHash"`
	GoHash    string           `json:"goResultHash"`
	Critical  bool             `json:"critical"`
}

type Observation struct {
	InputHash  string      `json:"inputHash"`
	Divergence *Divergence `json:"divergence,omitempty"`
	Metrics    Metrics     `json:"metrics"`
}

type Comparator struct {
	Node Runner
	Go   Runner
	Now  func() time.Time
	RSS  func() uint64

	mu           sync.Mutex
	observations []Observation
}

func (c *Comparator) Compare(ctx context.Context, input Input) (Observation, error) {
	if c.Node == nil || c.Go == nil {
		return Observation{}, errors.New("shadow runners are required")
	}
	now := c.Now
	if now == nil {
		now = time.Now
	}
	rss := c.RSS
	if rss == nil {
		rss = residentBytes
	}
	inputCopy := Input{FixtureID: input.FixtureID, Payload: append([]byte(nil), input.Payload...)}
	inputHash := digest(append([]byte(inputCopy.FixtureID+"\x00"), inputCopy.Payload...))

	started := now()
	nodeResult, nodeErr := c.Node.Run(ctx, Input{FixtureID: inputCopy.FixtureID, Payload: append([]byte(nil), inputCopy.Payload...)})
	nodeLatency := now().Sub(started)
	nodeRSS := nodeResult.RSSBytes
	if nodeRSS == 0 {
		nodeRSS = rss()
	}
	started = now()
	goResult, goErr := c.Go.Run(ctx, Input{FixtureID: inputCopy.FixtureID, Payload: append([]byte(nil), inputCopy.Payload...)})
	goLatency := now().Sub(started)
	goRSS := goResult.RSSBytes
	if goRSS == 0 {
		goRSS = rss()
	}
	if nodeErr != nil || goErr != nil {
		return Observation{}, fmt.Errorf("shadow execution failed: node=%v go=%v", classifyError(nodeErr), classifyError(goErr))
	}

	nodeHash := outcomeHash(nodeResult.Outcome)
	goHash := outcomeHash(goResult.Outcome)
	observation := Observation{InputHash: inputHash, Metrics: Metrics{
		NodeLatencyMicros: nodeLatency.Microseconds(), GoLatencyMicros: goLatency.Microseconds(),
		NodeRSSBytes: nodeRSS, GoRSSBytes: goRSS,
	}}
	if nodeResult.Outcome.Code != goResult.Outcome.Code || nodeHash != goHash {
		observation.Divergence = &Divergence{InputHash: inputHash, Node: nodeResult.Outcome.Code, Go: goResult.Outcome.Code, NodeHash: nodeHash, GoHash: goHash, Critical: nodeResult.Outcome.Code != goResult.Outcome.Code}
	}
	c.mu.Lock()
	c.observations = append(c.observations, observation)
	c.mu.Unlock()
	return observation, nil
}

func (c *Comparator) Report(startedAt, endedAt time.Time) Report {
	c.mu.Lock()
	observations := append([]Observation(nil), c.observations...)
	c.mu.Unlock()
	return NewReport(startedAt, endedAt, observations)
}

type Report struct {
	StartedAt           time.Time    `json:"startedAt"`
	EndedAt             time.Time    `json:"endedAt"`
	Comparisons         int          `json:"comparisons"`
	CriticalDivergences int          `json:"criticalDivergences"`
	Divergences         []Divergence `json:"divergences"`
	Metrics             []Metrics    `json:"metrics"`
}

func NewReport(startedAt, endedAt time.Time, observations []Observation) Report {
	report := Report{StartedAt: startedAt.UTC(), EndedAt: endedAt.UTC(), Comparisons: len(observations)}
	for _, observation := range observations {
		report.Metrics = append(report.Metrics, observation.Metrics)
		if observation.Divergence != nil {
			report.Divergences = append(report.Divergences, *observation.Divergence)
			if observation.Divergence.Critical {
				report.CriticalDivergences++
			}
		}
	}
	return report
}

func (r Report) Write(path string) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

type Readiness struct {
	MinimumWindow      time.Duration
	MinimumComparisons int
	GO09Checkpoint     bool
	DiscordLive        bool
}

func (r Readiness) Evaluate(report Report) error {
	var blockers []string
	if !r.GO09Checkpoint {
		blockers = append(blockers, "GO-09 live SSO checkpoint pending")
	}
	if !r.DiscordLive {
		blockers = append(blockers, "Discord live interaction checkpoint pending")
	}
	if report.CriticalDivergences > 0 {
		blockers = append(blockers, "critical shadow divergences present")
	}
	if report.Comparisons < r.MinimumComparisons {
		blockers = append(blockers, "shadow comparison minimum not reached")
	}
	if report.EndedAt.Sub(report.StartedAt) < r.MinimumWindow {
		blockers = append(blockers, "shadow observation window incomplete")
	}
	if len(blockers) > 0 {
		return errors.New(strings.Join(blockers, "; "))
	}
	return nil
}

// OpenReadOnly opens the existing Node-compatible SQLite file without running
// schema DDL. mode=ro, immutable=1 and PRAGMA query_only form three independent
// barriers against accidental shadow writes.
func OpenReadOnly(path string) (*sql.DB, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	dsn := "file:" + filepath.ToSlash(absolute) + "?mode=ro&immutable=1"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if _, err = db.Exec("PRAGMA query_only = ON"); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func outcomeHash(outcome uade.Outcome) string {
	rows := make([]string, 0, len(outcome.Vacancies))
	for _, vacancy := range outcome.Vacancies {
		rows = append(rows, strings.Join([]string{vacancy.Codigo, vacancy.Materia, vacancy.Turno, vacancy.Sede, vacancy.Horario, vacancy.Dias, fmt.Sprint(vacancy.Cupos)}, "\x1f"))
	}
	sort.Strings(rows)
	return digest([]byte(string(outcome.Code) + "\x00" + strings.Join(rows, "\x1e")))
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:12])
}

func classifyError(err error) string {
	if err == nil {
		return "none"
	}
	return "runner_error"
}
