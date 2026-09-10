// Package sysstats is a leaf package that reads the calling process's own
// RSS/swap memory usage and the size of the SQLite database file on disk,
// shared by internal/dashboard and internal/discordhttp so neither
// duplicates /proc reading or byte-formatting logic and neither imports the
// other -- the same pattern already used for internal/jobactions.
package sysstats

import (
	"fmt"
	"os"

	"github.com/Ogstra/uade-bot/internal/shadow"
)

// Stats holds every metric Collect can produce, each with its own
// independent availability flag. This is what lets a single unsupported
// reading (e.g. swap on Windows) degrade only that field instead of failing
// the whole response/page.
type Stats struct {
	RSSBytes        uint64
	RSSAvailable    bool
	SwapBytes       uint64
	SwapAvailable   bool
	DBSizeBytes     uint64
	DBSizeAvailable bool
}

// Collect reads pid's resident set size and swap usage via internal/shadow,
// and dbPath's file size via os.Stat when dbPath is non-empty. Each metric's
// Available flag is only set to true when its underlying read succeeded --
// a failure on one metric never prevents the others from being populated.
func Collect(pid int, dbPath string) Stats {
	var out Stats
	if rss, err := shadow.ProcessRSSBytes(pid); err == nil {
		out.RSSBytes = rss
		out.RSSAvailable = true
	}
	if swap, err := shadow.ProcessSwapBytes(pid); err == nil {
		out.SwapBytes = swap
		out.SwapAvailable = true
	}
	if dbPath != "" {
		if info, err := os.Stat(dbPath); err == nil {
			out.DBSizeBytes = uint64(info.Size())
			out.DBSizeAvailable = true
		}
	}
	return out
}

// FormatBytes humanizes a byte count using binary (1024-based) units.
func FormatBytes(bytes uint64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	div, unit := uint64(1024), 0
	units := []byte{'K', 'M', 'G', 'T', 'P', 'E'}
	for n := bytes / 1024; n >= 1024 && unit < len(units)-1; n /= 1024 {
		div *= 1024
		unit++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), units[unit])
}

// FormatOrUnavailable is the single shared source of truth for how an
// unavailable metric is worded across both the dashboard and /admin-stats.
func FormatOrUnavailable(available bool, value uint64) string {
	if !available {
		return "no disponible"
	}
	return FormatBytes(value)
}
