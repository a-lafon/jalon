// Package cli holds what the jalon commands share: loading the
// configuration, parsing the time windows, and formatting numbers.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/a-lafon/jalon/internal/config"
)

// LoadConfig reads the config file. A missing default file yields the built-in
// defaults, a missing file named with -config is an error.
func LoadConfig(flags *flag.FlagSet, path string) (config.Config, error) {
	cfg, err := config.Load(path)
	switch {
	case errors.Is(err, fs.ErrNotExist) && !flagSet(flags, "config"):
		return config.Default(), nil
	case err != nil:
		return config.Config{}, err
	}
	return cfg, nil
}

func flagSet(flags *flag.FlagSet, name string) bool {
	set := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

// ParseWindow accepts Go durations, plus Nd for whole days.
func ParseWindow(s string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.ParseFloat(days, 64)
		if err == nil && n > 0 {
			return time.Duration(n * float64(24*time.Hour)), nil
		}
	} else if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return d, nil
	}
	return 0, fmt.Errorf("invalid -since %q: want a positive duration such as 90m, 24h or 7d", s)
}

// FormatWindow renders a window as 7d, 24h or 45m rather than Go's 45m0s.
func FormatWindow(d time.Duration) string {
	const day = 24 * time.Hour
	if d >= 2*day && d%day == 0 {
		return fmt.Sprintf("%dd", d/day)
	}
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// ParseSplits parses the --split times, keeps those strictly inside the window
// and returns them in order.
func ParseSplits(s string, since, now time.Time) ([]time.Time, error) {
	if s == "" {
		return nil, nil
	}
	var cuts []time.Time
	for _, part := range strings.Split(s, ",") {
		t, err := parseSplitTime(strings.TrimSpace(part))
		if err != nil {
			return nil, err
		}
		if t.After(since) && t.Before(now) {
			cuts = append(cuts, t)
		}
	}
	sort.Slice(cuts, func(i, j int) bool { return cuts[i].Before(cuts[j]) })
	return cuts, nil
}

func parseSplitTime(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid -split time %q: want 2006-01-02, 2006-01-02T15:04 or RFC 3339", s)
}

// Thousands formats n with a comma between groups of three digits.
func Thousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if neg {
		return "-" + s
	}
	return s
}

// TokensK formats a token count in Thousands, as in the status line: 230K.
func TokensK(v float64) string { return fmt.Sprintf("%.0fK", v/1000) }

func Percent(v float64) string { return fmt.Sprintf("%.1f%%", v*100) }

func PercentOf(v float64) string { return fmt.Sprintf("%.0f%%", v*100) }

func Plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
