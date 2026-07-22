package cutover

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ogs/uade-bot/internal/shadow"
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
