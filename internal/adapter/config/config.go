package config

import (
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/grintzdel/spool/internal/domain/signature"
	"github.com/grintzdel/spool/internal/domain/signature/generic"
	"github.com/grintzdel/spool/internal/usecase"
)

const DefaultFile = ".spool.toml"

type Config struct {
	DB        string                    `toml:"db"`
	Serve     Serve                     `toml:"serve"`
	Retention Retention                 `toml:"retention"`
	Verify    Verify                    `toml:"verify"`
	Providers map[string]ProviderConfig `toml:"providers"`
}

type Serve struct {
	Port        int      `toml:"port"`
	Bind        string   `toml:"bind"`
	UIPort      int      `toml:"ui_port"`
	Forward     string   `toml:"forward"`
	Respond     string   `toml:"respond"`
	Timeout     Duration `toml:"timeout"`
	Concurrency int      `toml:"concurrency"`
	MaxBody     int64    `toml:"max_body"`
}

type Retention struct {
	MaxAge      Duration `toml:"max_age"`
	MaxRequests int      `toml:"max_requests"`
}

type Verify struct {
	Tolerance Duration `toml:"tolerance"`
}

type ProviderConfig struct {
	Secret          string `toml:"secret"`
	SecretEnv       string `toml:"secret_env"`
	Header          string `toml:"header"`
	TimestampHeader string `toml:"timestamp_header"`
	Template        string `toml:"template"`
	Algorithm       string `toml:"algorithm"`
	Encoding        string `toml:"encoding"`
	Prefix          string `toml:"prefix"`
	EventHeader     string `toml:"event_header"`
}

func Default() Config {
	return Config{
		Serve: Serve{
			Port: 7777, Bind: "127.0.0.1", UIPort: 7778, Respond: "ack",
			Timeout: Duration(10 * time.Second), Concurrency: 4, MaxBody: 10 << 20,
		},
		Retention: Retention{MaxAge: Duration(30 * 24 * time.Hour), MaxRequests: 10_000},
		Verify:    Verify{Tolerance: Duration(300 * time.Second)},
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return cfg, err
	default:
		if err := toml.Unmarshal(data, &cfg); err != nil {
			return cfg, fmt.Errorf("%s: %w", path, err)
		}
	}
	if env := os.Getenv("SPOOL_DB"); env != "" {
		cfg.DB = env
	}
	if cfg.DB == "" {
		cfg.DB = defaultDBPath()
	}
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	if c.Serve.Respond != "ack" && c.Serve.Respond != "passthrough" {
		return fmt.Errorf("serve.respond must be ack or passthrough, got %q", c.Serve.Respond)
	}
	if c.Serve.Forward != "" {
		if err := ValidateURL(c.Serve.Forward); err != nil {
			return fmt.Errorf("serve.forward: %w", err)
		}
	}
	_, err := c.GenericProviders()
	return err
}

func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%q is not an http(s) URL", raw)
	}
	return nil
}

func (c Config) GenericProviders() ([]signature.Provider, error) {
	var out []signature.Provider
	for _, name := range slices.Sorted(maps.Keys(c.Providers)) {
		pc := c.Providers[name]
		if pc.Header == "" {
			continue
		}
		p, err := generic.New(generic.Config{
			Name: name, Header: pc.Header, TimestampHeader: pc.TimestampHeader, Template: pc.Template,
			Algorithm: pc.Algorithm, Encoding: pc.Encoding, Prefix: pc.Prefix, EventHeader: pc.EventHeader,
		})
		if err != nil {
			return nil, fmt.Errorf("providers.%s: %w", name, err)
		}
		out = append(out, p)
	}
	return out, nil
}

func defaultDBPath() string {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "spool", "spool.db")
	}
	if runtime.GOOS == "windows" {
		if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
			return filepath.Join(dir, "spool", "spool.db")
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "spool.db"
	}
	return filepath.Join(home, ".local", "share", "spool", "spool.db")
}

type Secrets struct {
	providers map[string]ProviderConfig
}

var _ usecase.Secrets = Secrets{}

func (c Config) Secrets() Secrets { return Secrets{providers: c.Providers} }

func (s Secrets) Secret(provider, envOverride string) ([]byte, error) {
	if envOverride != "" {
		if v := os.Getenv(envOverride); v != "" {
			return []byte(v), nil
		}
		return nil, fmt.Errorf("%w: $%s is empty", usecase.ErrNoSecret, envOverride)
	}
	envName := "SPOOL_" + strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(provider)) + "_SECRET"
	if v := os.Getenv(envName); v != "" {
		return []byte(v), nil
	}
	pc := s.providers[provider]
	if pc.SecretEnv != "" {
		if v := os.Getenv(pc.SecretEnv); v != "" {
			return []byte(v), nil
		}
	}
	if pc.Secret != "" {
		return []byte(pc.Secret), nil
	}
	return nil, fmt.Errorf("%w: set $%s or providers.%s in %s", usecase.ErrNoSecret, envName, provider, DefaultFile)
}

type Duration time.Duration

func (d Duration) D() time.Duration { return time.Duration(d) }

func (d Duration) String() string { return time.Duration(d).String() }

func (d *Duration) UnmarshalText(b []byte) error {
	v, err := ParseDuration(string(b))
	*d = Duration(v)
	return err
}

func (d Duration) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	var days time.Duration
	if before, after, ok := strings.Cut(s, "d"); ok {
		n, err := strconv.Atoi(before)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		days, s = time.Duration(n)*24*time.Hour, after
		if s == "" {
			return days, nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q (examples: 300s, 90m, 7d)", s)
	}
	return days + d, nil
}
