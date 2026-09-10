package cutover

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Ogstra/uade-bot/internal/shadow"
)

func TestCutoverDisabledDoesNotRequireReport(t *testing.T) {
	if err := CheckIfEnabled(Config{}); err != nil {
		t.Fatal(err)
	}
}

func TestCutoverFailsClosedUntilReportAndCheckpointsPass(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	report := shadow.Report{StartedAt: time.Unix(0, 0), EndedAt: time.Unix(3601, 0), Comparisons: 10}
	if err := report.Write(path); err != nil {
		t.Fatal(err)
	}
	config := Config{Enabled: true, ReportPath: path, MinimumWindow: time.Hour, MinimumComparisons: 10}
	if err := CheckIfEnabled(config); err == nil || !strings.Contains(err.Error(), "GO-09") || !strings.Contains(err.Error(), "Discord") {
		t.Fatalf("expected checkpoint blockers, got %v", err)
	}
	config.GO09Checkpoint, config.DiscordLive = true, true
	if err := CheckIfEnabled(config); err != nil {
		t.Fatal(err)
	}
	os.Remove(path)
	if err := CheckIfEnabled(config); err == nil {
		t.Fatal("missing report must block cutover")
	}
}

// standaloneConfig is the Go-only production shape: cutover explicitly requested,
// no Node baseline, and therefore no shadow report on disk.
func standaloneConfig(t *testing.T, standalone bool) Config {
	t.Helper()
	return Config{
		Enabled:            true,
		Standalone:         standalone,
		ReportPath:         filepath.Join(t.TempDir(), "missing-report.json"),
		MinimumWindow:      time.Hour,
		MinimumComparisons: 100,
	}
}

func TestStandaloneEscapeBootsWithoutShadowReport(t *testing.T) {
	if err := CheckIfEnabled(standaloneConfig(t, true)); err != nil {
		t.Fatalf("standalone cutover must boot without a shadow report, got %v", err)
	}
}

func TestCutoverStillBlocksWithoutStandalone(t *testing.T) {
	err := CheckIfEnabled(standaloneConfig(t, false))
	if err == nil {
		t.Fatal("missing report must block cutover when standalone is not requested")
	}
	if !strings.Contains(err.Error(), "cutover blocked") {
		t.Fatalf("expected a cutover blocked error, got %v", err)
	}
}

func TestStandaloneIsNotABypassWhenCutoverDisabled(t *testing.T) {
	config := Config{Enabled: false, Standalone: true}
	if err := CheckIfEnabled(config); err != nil {
		t.Fatalf("disabled cutover must stay permissive, got %v", err)
	}
	if notice := StandaloneNotice(config); notice != "" {
		t.Fatalf("no verification was armed, so no audit notice must be emitted, got %q", notice)
	}
}

func TestStandaloneNoticeOnlyWhenEscapeApplies(t *testing.T) {
	notice := StandaloneNotice(Config{Enabled: true, Standalone: true})
	if notice == "" {
		t.Fatal("an applied standalone escape must produce an audit notice")
	}
	if !strings.Contains(notice, "UADE_STANDALONE") {
		t.Fatalf("audit notice must name UADE_STANDALONE, got %q", notice)
	}
	if !strings.Contains(notice, "skipped") {
		t.Fatalf("audit notice must state the verification was skipped, got %q", notice)
	}
	if notice := StandaloneNotice(Config{Enabled: true, Standalone: false}); notice != "" {
		t.Fatalf("a normal migration cutover must not emit a bypass notice, got %q", notice)
	}
}

func TestFromEnvStandaloneRequiresExactTrue(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		env   map[string]string
		wants bool
	}{
		{name: "exact true", env: map[string]string{"UADE_STANDALONE": "true"}, wants: true},
		{name: "absent", env: map[string]string{}, wants: false},
		{name: "empty", env: map[string]string{"UADE_STANDALONE": ""}, wants: false},
		{name: "uppercase", env: map[string]string{"UADE_STANDALONE": "TRUE"}, wants: false},
		{name: "numeric", env: map[string]string{"UADE_STANDALONE": "1"}, wants: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env := testCase.env
			config, err := FromEnv(func(key string) string { return env[key] })
			if err != nil {
				t.Fatal(err)
			}
			if config.Standalone != testCase.wants {
				t.Fatalf("Standalone = %v, want %v", config.Standalone, testCase.wants)
			}
		})
	}
}
