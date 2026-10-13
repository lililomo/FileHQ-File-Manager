package config

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	if Redact("APP_SECRET", "abc") != Redacted {
		t.Error("APP_SECRET must be redacted")
	}
	if Redact("ADMIN_INITIAL_PASSWORD", "abc") != Redacted {
		t.Error("password must be redacted")
	}
	if Redact("X-CSRF-Token", "abc") != Redacted {
		t.Error("token must be redacted")
	}
	if Redact("APP_NAME", "FileManager") != "FileManager" {
		t.Error("non-sensitive must pass through")
	}
	if Redact("APP_SECRET", "") != "" {
		t.Error("empty values stay empty")
	}
}

func TestLoggerNeverLeaksSecrets(t *testing.T) {
	const secret = "deadbeefcafebabe0123456789abcdef0123456789abcdef0123456789abcdef"
	const password = "IB+35t3U5+I_p5ZJcHLodfcr"
	env, _, _ := baseEnv(t)
	env = append(env[:2], "APP_SECRET="+secret, env[3], env[4], "ADMIN_INITIAL_PASSWORD="+password)
	cfg, err := load(t, env)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{EnvProduction, EnvDevelopment} {
		var buf bytes.Buffer
		logger := NewLogger(&buf, mode, slog.LevelDebug)
		logger.Info("config", slog.Any("config", cfg))
		logger.Info("explicit", slog.String("app_secret", secret), slog.String("password", password), slog.Group("auth", slog.String("token", password)))
		logger.With(slog.String("set-cookie", password)).Warn("with")
		out := buf.String()
		if strings.Contains(out, secret) || strings.Contains(out, password) {
			t.Fatalf("[%s] secret leaked into log output:\n%s", mode, out)
		}
		if !strings.Contains(out, Redacted) {
			t.Errorf("[%s] expected redaction marker in output", mode)
		}
		if !strings.Contains(out, "listen_addr") {
			t.Errorf("[%s] non-sensitive config fields should be logged", mode)
		}
	}
}
