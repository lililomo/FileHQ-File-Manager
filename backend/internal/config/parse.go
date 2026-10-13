package config

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// ParseSize parses a human size such as "10GB", "16 MB", "5mb" or "1048576".
// Suffixes are base 1024 (B, KB, MB, GB, TB), case-insensitive; a bare
// integer is interpreted as bytes. Negative values are rejected.
func ParseSize(s string) (int64, error) {
	raw := strings.TrimSpace(s)
	if raw == "" {
		return 0, errors.New("empty size")
	}
	upper := strings.ToUpper(raw)
	mult := int64(1)
	numPart := upper
	suffixes := []struct {
		suffix string
		mult   int64
	}{
		{"TB", 1 << 40}, {"GB", 1 << 30}, {"MB", 1 << 20}, {"KB", 1 << 10}, {"B", 1},
	}
	for _, sf := range suffixes {
		if strings.HasSuffix(upper, sf.suffix) {
			mult = sf.mult
			numPart = strings.TrimSpace(strings.TrimSuffix(upper, sf.suffix))
			break
		}
	}
	if numPart == "" {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	// Allow a decimal fraction such as "1.5GB".
	f, err := strconv.ParseFloat(numPart, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	if f < 0 {
		return 0, fmt.Errorf("size must not be negative: %q", s)
	}
	v := f * float64(mult)
	if v > math.MaxInt64 {
		return 0, fmt.Errorf("size too large: %q", s)
	}
	return int64(v), nil
}

// ParseDuration parses time.ParseDuration syntax plus a "d" suffix for days
// ("30d" = 720h). Negative durations are rejected.
func ParseDuration(s string) (time.Duration, error) {
	raw := strings.TrimSpace(s)
	if raw == "" {
		return 0, errors.New("empty duration")
	}
	if strings.HasSuffix(raw, "d") {
		n, err := strconv.ParseInt(strings.TrimSuffix(raw, "d"), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		if n < 0 || n > 100000 {
			return 0, fmt.Errorf("duration out of range: %q", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	if d < 0 {
		return 0, fmt.Errorf("duration must not be negative: %q", s)
	}
	return d, nil
}

// ParseBool accepts true/false, 1/0 and yes/no (case-insensitive).
func ParseBool(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes":
		return true, nil
	case "false", "0", "no":
		return false, nil
	}
	return false, fmt.Errorf("invalid boolean %q (use true/false, 1/0, yes/no)", s)
}

// ParseList splits a comma-separated list, trimming whitespace and dropping
// empty items.
func ParseList(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// FormatSize renders bytes with a binary suffix (for logs and check-deps).
func FormatSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}
