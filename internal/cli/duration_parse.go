package cli

import (
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// parseHumanDuration accepts the standard time.ParseDuration syntax
// ("1h", "30m", "5s") plus the day/week extensions every novel command
// in this CLI exposes ("7d", "2w"). Returns time.Duration on success.
//
// Why this exists: Go's time.ParseDuration explicitly rejects "d" and
// "w" because day/week aren't fixed durations across timezones. For a
// CLI surface where users naturally write "30d" or "1w", that strictness
// is bad UX. We treat "d" as 24h and "w" as 168h, which is correct for
// every Reddit query in this CLI (no timezone arithmetic involved).
var humanDurationRe = regexp.MustCompile(`^(\d+(?:\.\d+)?)([smhdw]?)$`)

func parseHumanDuration(s string) (time.Duration, error) {
	m := humanDurationRe.FindStringSubmatch(s)
	if m == nil {
		// Fall through to standard parser — handles compound forms like "1h30m".
		return time.ParseDuration(s)
	}
	val, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, fmt.Errorf("invalid duration value %q: %w", s, err)
	}
	unit := m[2]
	if unit == "" {
		// Bare number — interpret as seconds, matching time.ParseDuration's
		// behavior for "0".
		return time.Duration(val * float64(time.Second)), nil
	}
	switch unit {
	case "s":
		return time.Duration(val * float64(time.Second)), nil
	case "m":
		return time.Duration(val * float64(time.Minute)), nil
	case "h":
		return time.Duration(val * float64(time.Hour)), nil
	case "d":
		return time.Duration(val * 24 * float64(time.Hour)), nil
	case "w":
		return time.Duration(val * 7 * 24 * float64(time.Hour)), nil
	}
	return 0, fmt.Errorf("unknown duration unit %q in %q", unit, s)
}
