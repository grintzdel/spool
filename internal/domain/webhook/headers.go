package webhook

import (
	"net/http"
	"slices"
	"strings"
)

type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type Headers []Header

func (h Headers) Get(name string) string {
	for _, kv := range h {
		if strings.EqualFold(kv.Name, name) {
			return kv.Value
		}
	}
	return ""
}

func (h Headers) Has(name string) bool {
	for _, kv := range h {
		if strings.EqualFold(kv.Name, name) {
			return true
		}
	}
	return false
}

func (h Headers) Values(name string) []string {
	var out []string
	for _, kv := range h {
		if strings.EqualFold(kv.Name, name) {
			out = append(out, kv.Value)
		}
	}
	return out
}

func (h Headers) Set(name, value string) Headers {
	out := make(Headers, 0, len(h)+1)
	replaced := false
	for _, kv := range h {
		if !strings.EqualFold(kv.Name, name) {
			out = append(out, kv)
			continue
		}
		if !replaced {
			out = append(out, Header{Name: kv.Name, Value: value})
			replaced = true
		}
	}
	if !replaced {
		out = append(out, Header{Name: name, Value: value})
	}
	return out
}

func (h Headers) Without(names ...string) Headers {
	out := make(Headers, 0, len(h))
	for _, kv := range h {
		drop := false
		for _, n := range names {
			if strings.EqualFold(kv.Name, n) {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, kv)
		}
	}
	return out
}

func (h Headers) Clone() Headers {
	return append(Headers(nil), h...)
}

func (h Headers) HTTP() http.Header {
	out := make(http.Header, len(h))
	for _, kv := range h {
		key := http.CanonicalHeaderKey(kv.Name)
		out[key] = append(out[key], kv.Value)
	}
	return out
}

// http.Header loses the interleaving of distinct names; the per-name value order is all net/http exposes.
func FromHTTP(src http.Header) Headers {
	out := make(Headers, 0, len(src))
	for name, values := range src {
		for _, v := range values {
			out = append(out, Header{Name: name, Value: v})
		}
	}
	slices.SortStableFunc(out, func(a, b Header) int { return strings.Compare(a.Name, b.Name) })
	return out
}
