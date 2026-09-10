package cutover

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/Ogstra/uade-bot/internal/shadow"
)

type Config struct {
	Enabled bool
	// Standalone marks a Go-only deployment: there is no Node baseline to compare
	// against, so observation window, comparison count and divergences are not
	// relaxed gates -- they are metrics without meaning. Opting in skips the
	// shadow report verification entirely and is audited via StandaloneNotice.
	Standalone         bool
	ReportPath         string
	MinimumWindow      time.Duration
	MinimumComparisons int
	GO09Checkpoint     bool
	DiscordLive        bool
}

func FromEnv(getenv func(string) string) (Config, error) {
	config := Config{
		Enabled: getenv("UADE_CUTOVER_ENABLED") == "true",
		// Strict comparison, same as the other boolean flags: TRUE, 1 or yes do not
		// activate the escape. The bypass must be an unambiguous opt-in.
		Standalone:     getenv("UADE_STANDALONE") == "true",
		ReportPath:     getenv("SHADOW_REPORT_PATH"),
		GO09Checkpoint: getenv("GO09_CHECKPOINT_COMPLETE") == "true",
		DiscordLive:    getenv("DISCORD_LIVE_CHECKPOINT_COMPLETE") == "true",
	}
	if config.ReportPath == "" {
		config.ReportPath = "data/shadow-report.json"
	}
	hours := 24
	comparisons := 100
	var err error
	if raw := getenv("SHADOW_MIN_WINDOW_HOURS"); raw != "" {
		hours, err = strconv.Atoi(raw)
		if err != nil || hours < 1 {
			return Config{}, errors.New("SHADOW_MIN_WINDOW_HOURS must be a positive integer")
		}
	}
	if raw := getenv("SHADOW_MIN_COMPARISONS"); raw != "" {
		comparisons, err = strconv.Atoi(raw)
		if err != nil || comparisons < 1 {
			return Config{}, errors.New("SHADOW_MIN_COMPARISONS must be a positive integer")
		}
	}
	config.MinimumWindow = time.Duration(hours) * time.Hour
	config.MinimumComparisons = comparisons
	return config, nil
}

// CheckIfEnabled is the startup interlock. An explicit cutover request cannot
// boot unless its sanitized report and both live checkpoints are complete.
func CheckIfEnabled(config Config) error {
	if !config.Enabled {
		return nil
	}
	// Order matters: !Enabled first, Standalone second. When Enabled is false the
	// interlock was never armed, so Standalone is irrelevant there and cannot be
	// read as a bypass of anything else. Returning here skips reading the report,
	// unmarshalling it and building/evaluating shadow.Readiness altogether -- not
	// just the outcome of Evaluate.
	if config.Standalone {
		return nil
	}
	data, err := os.ReadFile(config.ReportPath)
	if err != nil {
		return fmt.Errorf("cutover blocked: read shadow report: %w", err)
	}
	var report shadow.Report
	if err = json.Unmarshal(data, &report); err != nil {
		return fmt.Errorf("cutover blocked: invalid shadow report: %w", err)
	}
	readiness := shadow.Readiness{
		MinimumWindow: config.MinimumWindow, MinimumComparisons: config.MinimumComparisons,
		GO09Checkpoint: config.GO09Checkpoint, DiscordLive: config.DiscordLive,
	}
	if err = readiness.Evaluate(report); err != nil {
		return fmt.Errorf("cutover blocked: %w", err)
	}
	return nil
}

// StandaloneNotice returns the audit line for a boot whose cutover interlock was
// skipped, and an empty string otherwise. It is a pure value so the decision stays
// testable and internal/cutover keeps no logging side effects; cmd/uade-bot emits it.
// The text is a fixed literal: it interpolates no paths, environment values or
// report contents.
func StandaloneNotice(config Config) string {
	if !config.Enabled || !config.Standalone {
		return ""
	}
	return "cutover audit: UADE_STANDALONE=true, shadow report verification against the Node baseline was skipped -- a standalone Go-only deployment has no Node baseline to compare against, so this boot did not pass that verification"
}
