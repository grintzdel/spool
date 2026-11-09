package diagnose

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/grintzdel/spool/internal/domain/signature"
	"github.com/grintzdel/spool/internal/domain/webhook"
)

const contextBytes = 24

type Diff struct {
	Reason      signature.Reason
	Offset      int
	RawContext  string
	SeenContext string
	RawSHA256   string
	SeenSHA256  string
	RawSize     int
	SeenSize    int
}

func (d Diff) Identical() bool { return d.Reason == "" }

func Compare(raw, seen []byte) Diff {
	d := Diff{
		Offset:     -1,
		RawSHA256:  webhook.SHA256Hex(raw),
		SeenSHA256: webhook.SHA256Hex(seen),
		RawSize:    len(raw),
		SeenSize:   len(seen),
	}
	if bytes.Equal(raw, seen) {
		return d
	}
	d.Offset = firstDivergence(raw, seen)
	d.RawContext = around(raw, d.Offset)
	d.SeenContext = around(seen, d.Offset)

	rawWS, seenWS := normalizeWhitespace(raw), normalizeWhitespace(seen)
	switch {
	case bytes.Equal(rawWS, seenWS):
		d.Reason = signature.BodyWhitespace
	case bytes.Equal(unescapeUnicode(rawWS), unescapeUnicode(seenWS)):
		d.Reason = signature.BodyUnicode
	case jsonEqual(raw, seen):
		d.Reason = signature.BodyReformatted
	default:
		d.Reason = signature.BodyChanged
	}
	return d
}

func (d Diff) Render() string {
	var b strings.Builder
	if d.Identical() {
		fmt.Fprintf(&b, "identical: %d bytes, sha256 %s\n", d.RawSize, d.RawSHA256)
		return b.String()
	}
	fmt.Fprintf(&b, "reason:  %s (%s)\n", d.Reason, d.Reason.Meaning())
	fmt.Fprintf(&b, "raw:     %d bytes, sha256 %s\n", d.RawSize, d.RawSHA256)
	fmt.Fprintf(&b, "app:     %d bytes, sha256 %s\n", d.SeenSize, d.SeenSHA256)
	fmt.Fprintf(&b, "first divergent byte at offset %d\n", d.Offset)
	fmt.Fprintf(&b, "  raw: %s\n", d.RawContext)
	fmt.Fprintf(&b, "  app: %s\n", d.SeenContext)
	return b.String()
}

func firstDivergence(a, b []byte) int {
	n := min(len(a), len(b))
	for i := range n {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

func around(b []byte, offset int) string {
	start := max(0, offset-contextBytes)
	end := min(len(b), offset+contextBytes)
	prefix, suffix := "", ""
	if start > 0 {
		prefix = "…"
	}
	if end < len(b) {
		suffix = "…"
	}
	return prefix + strconv.Quote(string(b[start:end])) + suffix
}

var bom = []byte{0xEF, 0xBB, 0xBF}

func normalizeWhitespace(b []byte) []byte {
	b = bytes.TrimPrefix(b, bom)
	b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	b = bytes.ReplaceAll(b, []byte("\r"), []byte("\n"))
	return bytes.TrimRight(b, "\n")
}

func unescapeUnicode(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if b[i] == '\\' && i+1 < len(b) && b[i+1] == '\\' {
			out = append(out, '\\', '\\')
			i++
			continue
		}
		if r, n := decodeEscape(b[i:]); n > 0 {
			out = utf8.AppendRune(out, r)
			i += n - 1
			continue
		}
		out = append(out, b[i])
	}
	return out
}

func decodeEscape(b []byte) (rune, int) {
	if len(b) >= 2 && b[0] == '\\' && b[1] == '/' {
		return '/', 2
	}
	r, ok := hex4(b)
	if !ok {
		return 0, 0
	}
	if r >= 0xD800 && r < 0xDC00 {
		if low, ok := hex4(b[6:]); ok && low >= 0xDC00 && low < 0xE000 {
			return (r-0xD800)<<10 + (low - 0xDC00) + 0x10000, 12
		}
		return 0, 0
	}
	if r < 0x20 || r == '"' || r == '\\' {
		return 0, 0
	}
	return r, 6
}

func hex4(b []byte) (rune, bool) {
	if len(b) < 6 || b[0] != '\\' || b[1] != 'u' {
		return 0, false
	}
	v, err := strconv.ParseUint(string(b[2:6]), 16, 32)
	if err != nil {
		return 0, false
	}
	return rune(v), true
}

func jsonEqual(a, b []byte) bool {
	va, ok := decodeJSON(a)
	if !ok {
		return false
	}
	vb, ok := decodeJSON(b)
	if !ok {
		return false
	}
	return valuesEqual(va, vb)
}

func decodeJSON(b []byte) (any, bool) {
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(b, bom)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	if dec.More() {
		return nil, false
	}
	return v, true
}

func valuesEqual(a, b any) bool {
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, v := range x {
			w, ok := y[k]
			if !ok || !valuesEqual(v, w) {
				return false
			}
		}
		return true
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !valuesEqual(x[i], y[i]) {
				return false
			}
		}
		return true
	case json.Number:
		y, ok := b.(json.Number)
		if !ok {
			return false
		}
		fx, okx := new(big.Float).SetString(string(x))
		fy, oky := new(big.Float).SetString(string(y))
		return okx && oky && fx.Cmp(fy) == 0
	default:
		return a == b
	}
}
