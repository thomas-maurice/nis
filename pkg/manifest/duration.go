package manifest

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// parseDuration extends time.ParseDuration to accept a "d" (day) suffix.
// "1d" == 24h, "90d" == 2160h, "30d12h" is not supported — unit must be
// the whole string. For everything else it delegates to time.ParseDuration.
func parseDuration(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		n, err := strconv.ParseInt(s[:len(s)-1], 10, 64)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("manifest: invalid day duration %q", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	// Handle year shorthand: "1y" == 365 days.
	if strings.HasSuffix(s, "y") {
		n, err := strconv.ParseInt(s[:len(s)-1], 10, 64)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("manifest: invalid year duration %q", s)
		}
		return time.Duration(n) * 365 * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("manifest: %w", err)
	}
	return d, nil
}
