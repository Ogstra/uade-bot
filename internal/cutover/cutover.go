package cutover

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/ogs/uade-bot/internal/shadow"
)

type Config struct {
	Enabled            bool
	ReportPath         string
	MinimumWindow      time.Duration
	MinimumComparisons int
	GO09Checkpoint     bool
	DiscordLive        bool
}

func FromEnv(getenv func(string) string) (Config, error) {
	config := Config{
		Enabled:        getenv("UADE_CUTOVER_ENABLED") == "true",
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
