package cli

import (
	"testing"
	"time"
)

func TestParseHumanDuration(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"1h", time.Hour, false},
		{"30m", 30 * time.Minute, false},
		{"5s", 5 * time.Second, false},
		{"1d", 24 * time.Hour, false},
		{"7d", 7 * 24 * time.Hour, false},
		{"2w", 2 * 7 * 24 * time.Hour, false},
		{"90d", 90 * 24 * time.Hour, false},
		{"0.5h", 30 * time.Minute, false},
		{"1h30m", 90 * time.Minute, false}, // compound — falls through to time.ParseDuration
		{"abc", 0, true},
		{"3x", 0, true}, // unknown unit — falls through and time.ParseDuration also rejects
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := parseHumanDuration(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got %v", c.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse %q: %v", c.in, err)
			}
			if got != c.want {
				t.Fatalf("parse %q = %v, want %v", c.in, got, c.want)
			}
		})
	}
}
