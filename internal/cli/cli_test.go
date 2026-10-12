package cli

import (
	"testing"
	"time"
)

func TestParseWindow(t *testing.T) {
	tests := map[string]time.Duration{
		"24h": 24 * time.Hour, "90m": 90 * time.Minute, "7d": 7 * 24 * time.Hour, "1.5d": 36 * time.Hour,
	}
	for in, want := range tests {
		if got, err := ParseWindow(in); err != nil || got != want {
			t.Errorf("ParseWindow(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
}

func TestThousands(t *testing.T) {
	for in, want := range map[int64]string{0: "0", 999: "999", 1000: "1,000", 41203: "41,203", 1234567: "1,234,567", -1500: "-1,500"} {
		if got := Thousands(in); got != want {
			t.Errorf("Thousands(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestTokensK(t *testing.T) {
	for in, want := range map[float64]string{0: "0K", 499: "0K", 500: "1K", 229_499: "229K", 229_500: "230K", 230_500: "231K", 230_499.5: "230K"} {
		if got := TokensK(in); got != want {
			t.Errorf("TokensK(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatWindow(t *testing.T) {
	tests := map[time.Duration]string{
		45 * time.Minute: "45m", 24 * time.Hour: "24h", 90 * time.Minute: "1h30m",
		7 * 24 * time.Hour: "7d", 36 * time.Hour: "36h", 30 * time.Second: "30s",
	}
	for in, want := range tests {
		if got := FormatWindow(in); got != want {
			t.Errorf("FormatWindow(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestParseSplits(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.Local)
	since := now.Add(-7 * 24 * time.Hour)
	cuts, err := ParseSplits("2026-10-08,2026-10-07T09:49,2026-09-01", since, now)
	if err != nil || len(cuts) != 2 {
		t.Fatalf("cuts = %v, err %v", cuts, err)
	}
	if !cuts[0].Equal(time.Date(2026, 10, 7, 9, 49, 0, 0, time.Local)) || !cuts[1].Equal(time.Date(2026, 10, 8, 0, 0, 0, 0, time.Local)) {
		t.Errorf("cuts = %v, want them sorted and in local time", cuts)
	}
}
