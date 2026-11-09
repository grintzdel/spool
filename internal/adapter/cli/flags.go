package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/grintzdel/spool/internal/adapter/config"
)

type durationFlag struct{ d *time.Duration }

func (f durationFlag) String() string {
	if f.d == nil || *f.d == 0 {
		return "0"
	}
	return f.d.String()
}

func (f durationFlag) Set(s string) error {
	d, err := config.ParseDuration(s)
	if err != nil {
		return err
	}
	*f.d = d
	return nil
}

func (durationFlag) Type() string { return "duration" }

func parseHeaderFlags(values []string) (map[string]string, error) {
	out := make(map[string]string, len(values))
	for _, v := range values {
		k, val, ok := strings.Cut(v, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("--set-header expects K=V, got %q", v)
		}
		out[strings.TrimSpace(k)] = val
	}
	return out, nil
}
