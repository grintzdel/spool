package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/grintzdel/spool/internal/adapter/config"
	"github.com/grintzdel/spool/internal/usecase"
)

func write(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".spool.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDefaultsWithoutFile(t *testing.T) {
	t.Setenv("SPOOL_DB", "")
	cfg, err := config.Load(filepath.Join(t.TempDir(), "absent.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Serve.Port != 7777 || cfg.Serve.Bind != "127.0.0.1" || cfg.Serve.UIPort != 7778 || cfg.Serve.Respond != "ack" {
		t.Fatalf("serve defaults %+v", cfg.Serve)
	}
	if cfg.Serve.Timeout.D() != 10*time.Second || cfg.Serve.Concurrency != 4 || cfg.Serve.MaxBody != 10<<20 {
		t.Fatalf("serve defaults %+v", cfg.Serve)
	}
	if cfg.Retention.MaxAge.D() != 30*24*time.Hour || cfg.Retention.MaxRequests != 10_000 || cfg.Verify.Tolerance.D() != 300*time.Second {
		t.Fatalf("retention/verify defaults %+v %+v", cfg.Retention, cfg.Verify)
	}
	if filepath.Base(cfg.DB) != "spool.db" {
		t.Fatalf("db = %s", cfg.DB)
	}
}

func TestLoadFileAndEnvOverride(t *testing.T) {
	path := write(t, `
db = "/tmp/x.db"
[serve]
port = 9000
forward = "http://localhost:3000"
respond = "passthrough"
timeout = "2s"
[retention]
max_age = "7d"
[providers.stripe]
secret_env = "STRIPE_WEBHOOK_SECRET"
[providers.acme]
header = "X-Acme-Signature"
prefix = "sha256="
secret = "acme-secret"
`)
	t.Setenv("SPOOL_DB", "/tmp/override.db")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DB != "/tmp/override.db" || cfg.Serve.Port != 9000 || cfg.Serve.UIPort != 7778 || cfg.Serve.Forward != "http://localhost:3000" {
		t.Fatalf("cfg %+v", cfg)
	}
	if cfg.Serve.Timeout.D() != 2*time.Second || cfg.Retention.MaxAge.D() != 7*24*time.Hour {
		t.Fatalf("durations %v %v", cfg.Serve.Timeout, cfg.Retention.MaxAge)
	}
	providers, err := cfg.GenericProviders()
	if err != nil || len(providers) != 1 || providers[0].Name() != "acme" {
		t.Fatalf("generic providers %v %v", providers, err)
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	for _, content := range []string{
		"[serve]\nrespond = \"maybe\"",
		"[serve]\ntimeout = \"soon\"",
		"[serve]\nforward = \"localhost:3000\"",
		"[providers.acme]\nheader = \"X\"\nalgorithm = \"md5\"",
	} {
		if _, err := config.Load(write(t, content)); err == nil {
			t.Errorf("expected error for %q", content)
		}
	}
}

func TestSecretsResolutionOrder(t *testing.T) {
	cfg, err := config.Load(write(t, `
[providers.stripe]
secret_env = "MY_STRIPE"
secret = "from-file"
[providers.github]
secret = "gh-file"
`))
	if err != nil {
		t.Fatal(err)
	}
	s := cfg.Secrets()

	t.Setenv("CUSTOM", "from-flag-env")
	t.Setenv("SPOOL_STRIPE_SECRET", "from-spool-env")
	t.Setenv("MY_STRIPE", "from-config-env")
	check := func(provider, override, want string) {
		t.Helper()
		got, err := s.Secret(provider, override)
		if err != nil || string(got) != want {
			t.Fatalf("Secret(%s, %s) = %q, %v; want %q", provider, override, got, err, want)
		}
	}
	check("stripe", "CUSTOM", "from-flag-env")
	check("stripe", "", "from-spool-env")
	t.Setenv("SPOOL_STRIPE_SECRET", "")
	check("stripe", "", "from-config-env")
	t.Setenv("MY_STRIPE", "")
	check("stripe", "", "from-file")
	check("github", "", "gh-file")

	if _, err := s.Secret("gitlab", ""); !errors.Is(err, usecase.ErrNoSecret) {
		t.Fatalf("err = %v", err)
	}
	if _, err := s.Secret("stripe", "UNSET_VAR"); !errors.Is(err, usecase.ErrNoSecret) {
		t.Fatalf("err = %v", err)
	}
}

func TestParseDuration(t *testing.T) {
	tests := map[string]time.Duration{"7d": 7 * 24 * time.Hour, "1d12h": 36 * time.Hour, "300s": 5 * time.Minute, "90m": 90 * time.Minute}
	for in, want := range tests {
		got, err := config.ParseDuration(in)
		if err != nil || got != want {
			t.Errorf("ParseDuration(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := config.ParseDuration("x"); err == nil {
		t.Error("expected error")
	}
}
