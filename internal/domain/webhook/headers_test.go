package webhook_test

import (
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/grintzdel/spool/internal/domain/webhook"
)

func TestHeadersGetIsCaseInsensitiveAndReturnsFirst(t *testing.T) {
	h := webhook.Headers{{Name: "X-A", Value: "1"}, {Name: "x-a", Value: "2"}}
	if got := h.Get("x-A"); got != "1" {
		t.Fatalf("Get = %q, want 1", got)
	}
	if got := h.Get("missing"); got != "" {
		t.Fatalf("Get missing = %q, want empty", got)
	}
}

func TestHeadersValuesKeepsDuplicatesInOrder(t *testing.T) {
	h := webhook.Headers{{Name: "Set", Value: "a"}, {Name: "Other", Value: "x"}, {Name: "set", Value: "b"}}
	if diff := cmp.Diff([]string{"a", "b"}, h.Values("SET")); diff != "" {
		t.Fatal(diff)
	}
}

func TestHeadersSetReplacesAllOccurrencesInPlace(t *testing.T) {
	h := webhook.Headers{{Name: "A", Value: "1"}, {Name: "B", Value: "2"}, {Name: "a", Value: "3"}}
	h = h.Set("a", "new")
	want := webhook.Headers{{Name: "A", Value: "new"}, {Name: "B", Value: "2"}}
	if diff := cmp.Diff(want, h); diff != "" {
		t.Fatal(diff)
	}
	h = h.Set("C", "4")
	if h.Get("c") != "4" || len(h) != 3 {
		t.Fatalf("Set did not append: %v", h)
	}
}

func TestHeadersWithoutRemovesCaseInsensitively(t *testing.T) {
	h := webhook.Headers{{Name: "Connection", Value: "close"}, {Name: "A", Value: "1"}, {Name: "connection", Value: "x"}}
	want := webhook.Headers{{Name: "A", Value: "1"}}
	if diff := cmp.Diff(want, h.Without("CONNECTION")); diff != "" {
		t.Fatal(diff)
	}
}

func TestFromHTTPPreservesWireOrderForRepeatedValues(t *testing.T) {
	src := http.Header{"X-Multi": {"1", "2"}}
	got := webhook.FromHTTP(src)
	want := webhook.Headers{{Name: "X-Multi", Value: "1"}, {Name: "X-Multi", Value: "2"}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatal(diff)
	}
	if diff := cmp.Diff(src, got.HTTP()); diff != "" {
		t.Fatal(diff)
	}
}
