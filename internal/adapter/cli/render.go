package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/grintzdel/spool/internal/domain/signature"
	"github.com/grintzdel/spool/internal/domain/webhook"
	"github.com/grintzdel/spool/internal/usecase"
)

const timeLayout = "2006-01-02 15:04:05"

func appStatus(a *webhook.Attempt) string {
	switch {
	case a == nil:
		return "-"
	case a.Error != "":
		return "error"
	default:
		return fmt.Sprint(a.StatusCode)
	}
}

func verifyStatus(v *webhook.Verification) string {
	if v == nil {
		return "-"
	}
	return v.Reason
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func prettyBody(b []byte) string {
	if len(b) == 0 {
		return "(empty)"
	}
	var out bytes.Buffer
	if json.Indent(&out, b, "", "  ") == nil {
		return out.String()
	}
	if utf8.Valid(b) {
		return string(b)
	}
	return fmt.Sprintf("(%d bytes of binary data, use --hex or --raw)", len(b))
}

func writeAttempt(w io.Writer, a webhook.Attempt) {
	flags := ""
	if a.Resigned {
		flags = " resigned"
	}
	fmt.Fprintf(w, "  %s  %-7s %-6s %6s  %s%s\n", a.StartedAt.Local().Format(timeLayout), a.Kind, appStatus(&a),
		a.Duration.Round(time.Millisecond), a.TargetURL, flags)
	if a.Error != "" {
		fmt.Fprintf(w, "      %s\n", a.Error)
	} else if len(a.ResponseBody) > 0 {
		fmt.Fprintf(w, "      %s\n", truncate(strings.TrimSpace(string(a.ResponseBody)), 160))
	}
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

func writeReport(w io.Writer, rep usecase.Report) {
	r := rep.Result
	mark := "✗"
	if r.Reason == signature.OKRaw {
		mark = "✓"
	}
	fmt.Fprintf(w, "%s %s  %s\n", mark, r.Reason, r.Reason.Meaning())
	fmt.Fprintf(w, "  provider: %s   request: %s\n", rep.Provider, rep.Request.ID)
	if r.Expected != "" {
		fmt.Fprintf(w, "  expected: %s\n", r.Expected)
	}
	if r.Got != "" {
		fmt.Fprintf(w, "  got:      %s\n", r.Got)
	}
	for _, h := range r.Hints {
		fmt.Fprintf(w, "  → %s\n", h)
	}
	if rep.Diff != nil {
		fmt.Fprintln(w)
		for line := range strings.Lines(rep.Diff.Render()) {
			fmt.Fprint(w, "  ", line)
		}
	}
	if len(rep.Fixes) > 0 {
		fmt.Fprintln(w, "\n  Fix by framework:")
		for _, f := range rep.Fixes {
			fmt.Fprintf(w, "    %-8s %s\n", f.Framework, f.Snippet)
		}
	}
}

func hexDump(b []byte) string {
	var out strings.Builder
	for off := 0; off < len(b); off += 16 {
		line := b[off:min(off+16, len(b))]
		fmt.Fprintf(&out, "%08x  ", off)
		for i := range 16 {
			if i < len(line) {
				fmt.Fprintf(&out, "%02x ", line[i])
			} else {
				out.WriteString("   ")
			}
			if i == 7 {
				out.WriteByte(' ')
			}
		}
		out.WriteString(" |")
		for _, c := range line {
			if c >= 0x20 && c < 0x7f {
				out.WriteByte(c)
			} else {
				out.WriteByte('.')
			}
		}
		out.WriteString("|\n")
	}
	return out.String()
}
