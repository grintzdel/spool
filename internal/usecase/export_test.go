package usecase_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/grintzdel/spool/internal/domain/webhook"
	"github.com/grintzdel/spool/internal/usecase"
)

func TestExportCurlQuotesBodyAndDropsTransportHeaders(t *testing.T) {
	req := webhook.NewRequest("r", now, "POST", "/hook", "a=1", webhook.Headers{
		{Name: "Host", Value: "x"}, {Name: "Content-Length", Value: "9"}, {Name: "X-Sig", Value: "it's"},
	}, []byte(`{"q":"it's"}`))

	got, err := usecase.Export(req, usecase.FormatCurl, "http://localhost:3000")
	if err != nil {
		t.Fatal(err)
	}
	want := `curl -X POST 'http://localhost:3000/hook?a=1' \
  -H 'X-Sig: it'\''s' \
  --data-binary '{"q":"it'\''s"}'
`
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestExportCurlFallsBackToBase64ForBinary(t *testing.T) {
	req := webhook.NewRequest("r", now, "POST", "/", "", nil, []byte{0xff, 0x00})
	got, _ := usecase.Export(req, usecase.FormatCurl, "http://t")
	if !strings.HasPrefix(got, "printf '%s' '/wA=' | base64 -d | curl") {
		t.Fatalf("got %s", got)
	}
}

func TestExportJSONAndHTTP(t *testing.T) {
	req := webhook.NewRequest("r", now, "POST", "/hook", "", webhook.Headers{{Name: "Content-Type", Value: "application/json"}}, []byte(`{}`))
	req.Provider = "stripe"

	js, err := usecase.Export(req, usecase.FormatJSON, "")
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(js), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["body"] != "{}" || decoded["provider"] != "stripe" || decoded["received_at"] != now.Format(time.RFC3339Nano) {
		t.Fatalf("json %v", decoded)
	}

	raw, _ := usecase.Export(req, usecase.FormatHTTP, "")
	if raw != "POST /hook HTTP/1.1\r\nContent-Type: application/json\r\nContent-Length: 2\r\n\r\n{}" {
		t.Fatalf("http %q", raw)
	}
}

func TestPrune(t *testing.T) {
	store := &memStore{}
	store.requests = append(store.requests,
		stripeRequest("old", now.Add(-48*time.Hour)),
		stripeRequest("a", now.Add(-2*time.Hour)),
		stripeRequest("b", now.Add(-time.Hour)),
	)
	p := usecase.Pruner{Store: store, Clock: fixedClock()}
	n, err := p.Prune(context.Background(), 24*time.Hour, 1)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || len(store.requests) != 1 || store.requests[0].ID != "b" {
		t.Fatalf("n=%d left=%v", n, store.requests)
	}
}
