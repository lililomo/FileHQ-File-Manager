package remotedl

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"kppbwi.app/filemanager/internal/db"
)

// Settings keys in the settings table (SPEC §3.3, §4.2). Admin UI: Step 19a.
const (
	KeyEnabled   = "remote_download_enabled"
	KeyMaxSize   = "remote_download_max_size"
	KeyAllowlist = "remote_download.allowlist"
	KeyDenylist  = "remote_download.denylist"
)

// settingsTTL is the runtime cache of admin overrides (SPEC §8.17: "nilai
// berlaku ≤ 5 detik").
const settingsTTL = 5 * time.Second

// Settings are the effective values: environment defaults overridden by
// the settings table (an override wins over the environment, SPEC §3.1;
// the admin API validates max_size ≤ MAX_UPLOAD_SIZE before storing it).
type Settings struct {
	Enabled   bool
	MaxSize   int64
	Allowlist []string
	Denylist  []string
}

type settingsCache struct {
	repo    db.SettingsRepo
	logger  *slog.Logger
	now     func() time.Time
	base    Settings
	mu      sync.Mutex
	cached  Settings
	fetched time.Time
}

func (c *settingsCache) get(ctx context.Context) Settings {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.fetched.IsZero() && c.now().Sub(c.fetched) < settingsTTL {
		return c.cached
	}
	out := c.base
	if c.repo != nil {
		rows, err := c.repo.ListSettings(ctx)
		if err != nil {
			c.logger.WarnContext(ctx, "remote download settings unavailable; using defaults", slog.String("error", err.Error()))
		} else {
			for _, row := range rows {
				switch row.Key {
				case KeyEnabled:
					var b bool
					if json.Unmarshal([]byte(row.ValueJson), &b) == nil {
						out.Enabled = b
					}
				case KeyMaxSize:
					if n := parseSize(row.ValueJson); n > 0 {
						out.MaxSize = n
					}
				case KeyAllowlist:
					out.Allowlist = parseList(row.ValueJson)
				case KeyDenylist:
					out.Denylist = parseList(row.ValueJson)
				}
			}
		}
	}
	c.cached, c.fetched = out, c.now()
	return out
}

// Invalidate drops the cache (tests, admin writes).
func (c *settingsCache) Invalidate() {
	c.mu.Lock()
	c.fetched = time.Time{}
	c.mu.Unlock()
}

// parseSize accepts a JSON number of bytes or a string such as "500MB".
func parseSize(raw string) int64 {
	var n float64
	if json.Unmarshal([]byte(raw), &n) == nil {
		return int64(n)
	}
	var s string
	if json.Unmarshal([]byte(raw), &s) != nil {
		return 0
	}
	s = strings.ToUpper(strings.TrimSpace(s))
	mult := int64(1)
	for _, u := range []struct {
		suf string
		m   int64
	}{{"TB", 1 << 40}, {"GB", 1 << 30}, {"MB", 1 << 20}, {"KB", 1 << 10}, {"B", 1}} {
		if strings.HasSuffix(s, u.suf) {
			s, mult = strings.TrimSpace(strings.TrimSuffix(s, u.suf)), u.m
			break
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v <= 0 {
		return 0
	}
	return int64(v * float64(mult))
}

// parseList accepts a JSON array of strings or a comma/newline separated
// string.
func parseList(raw string) []string {
	var arr []string
	if json.Unmarshal([]byte(raw), &arr) == nil {
		return cleanList(arr)
	}
	var s string
	if json.Unmarshal([]byte(raw), &s) == nil {
		return cleanList(strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' || r == ' ' }))
	}
	return nil
}

func cleanList(in []string) []string {
	out := in[:0]
	for _, v := range in {
		v = strings.ToLower(strings.TrimSpace(v))
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}
