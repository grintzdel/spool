package diagnose_test

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/grintzdel/spool/internal/domain/diagnose"
	"github.com/grintzdel/spool/internal/domain/signature"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestCompareGolden(t *testing.T) {
	want := map[string]signature.Reason{
		"identical":                   "",
		"reformatted":                 signature.BodyReformatted,
		"numbers_reformatted":         signature.BodyReformatted,
		"whitespace_crlf":             signature.BodyWhitespace,
		"whitespace_bom":              signature.BodyWhitespace,
		"whitespace_trailing_newline": signature.BodyWhitespace,
		"unicode":                     signature.BodyUnicode,
		"changed":                     signature.BodyChanged,
		"truncated":                   signature.BodyChanged,
	}
	for name, reason := range want {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join("testdata", name)
			raw := mustRead(t, filepath.Join(dir, "raw.bin"))
			seen := mustRead(t, filepath.Join(dir, "seen.bin"))

			diff := diagnose.Compare(raw, seen)

			if diff.Reason != reason {
				t.Fatalf("reason = %q, want %q", diff.Reason, reason)
			}
			golden := filepath.Join(dir, "expected.golden")
			got := diff.Render()
			if *update {
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if d := cmp.Diff(string(mustRead(t, golden)), got); d != "" {
				t.Fatalf("golden mismatch (-want +got), run with -update:\n%s", d)
			}
		})
	}
}

func TestFixesCoverTheMainFrameworks(t *testing.T) {
	var got []string
	for _, f := range diagnose.Fixes() {
		got = append(got, f.Framework)
	}
	if d := cmp.Diff([]string{"Express", "NestJS", "Fastify", "Go"}, got); d != "" {
		t.Fatal(d)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
