package capture_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/grintzdel/spool/internal/adapter/capture"
	"github.com/grintzdel/spool/internal/domain/signature"
	"github.com/grintzdel/spool/internal/domain/signature/stripe"
	"github.com/grintzdel/spool/internal/domain/webhook"
	"github.com/grintzdel/spool/internal/usecase"
)

type store struct {
	usecase.Store
	mu   sync.Mutex
	reqs []webhook.Request
	err  error
}

func (s *store) SaveRequest(_ context.Context, r webhook.Request) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reqs = append(s.reqs, r)
	return s.err
}

type ids struct{}

func (ids) NewID() string { return "01TEST" }

type queue struct{ got []webhook.Request }

func (q *queue) Enqueue(r webhook.Request) { q.got = append(q.got, r) }

type deliverer struct {
	attempt webhook.Attempt
	opts    usecase.DeliverOptions
}

func (d *deliverer) Deliver(_ context.Context, _ webhook.Request, o usecase.DeliverOptions) (webhook.Attempt, error) {
	d.opts = o
	return d.attempt, nil
}

func newHandler(s *store) capture.Handler {
	reg := &signature.Registry{}
	reg.Register(stripe.New())
	return capture.Handler{
		Capturer: usecase.Capturer{Store: s, Registry: reg, Publisher: usecase.NopPublisher{}, IDs: ids{},
			Clock: usecase.ClockFunc(time.Now)},
		MaxBody: 1024,
	}
}

func TestCaptureStoresRawBodyOnAnyPath(t *testing.T) {
	s := &store{}
	srv := httptest.NewServer(newHandler(s))
	defer srv.Close()
	body := []byte("{\n  \"description\": \"Caf\u00e9 \\u00e9\"\n}\r\n")

	req, _ := http.NewRequest("PUT", srv.URL+"/any/deep/path?x=1&y=2", bytes.NewReader(body))
	req.Header.Set("Stripe-Signature", "t=1,v1=abc")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != 200 || resp.Header.Get("X-Spool-Id") != "01TEST" {
		t.Fatalf("status %d, id %q", resp.StatusCode, resp.Header.Get("X-Spool-Id"))
	}
	got := s.reqs[0]
	if !bytes.Equal(got.Body, body) || got.BodySHA256 != webhook.SHA256Hex(body) {
		t.Fatal("body not byte-exact")
	}
	if got.Method != "PUT" || got.Path != "/any/deep/path" || got.Query != "x=1&y=2" || got.Provider != "stripe" {
		t.Fatalf("request %+v", got)
	}
	if got.Headers.Get("Host") == "" {
		t.Fatal("Host header not recorded")
	}
}

func TestAckModeEnqueuesForward(t *testing.T) {
	q := &queue{}
	h := newHandler(&store{})
	h.Queue = q
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/hook", strings.NewReader("{}")))
	if rec.Code != 200 || len(q.got) != 1 {
		t.Fatalf("code %d, queued %d", rec.Code, len(q.got))
	}
}

func TestPassthroughRelaysAppResponse(t *testing.T) {
	tests := []struct {
		name    string
		attempt webhook.Attempt
		code    int
		body    string
	}{
		{"app error", webhook.Attempt{StatusCode: 400, ResponseBody: []byte("bad sig")}, 400, "bad sig"},
		{"app down", webhook.Attempt{Error: "connection refused"}, http.StatusBadGateway, "connection refused"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &deliverer{attempt: tt.attempt}
			h := newHandler(&store{})
			h.Passthrough, h.Target = d, "http://app"
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest("POST", "/hook", strings.NewReader("{}")))
			if rec.Code != tt.code || !strings.Contains(rec.Body.String(), tt.body) {
				t.Fatalf("code %d body %q", rec.Code, rec.Body.String())
			}
			if d.opts.Target != "http://app" || d.opts.Kind != webhook.AttemptForward {
				t.Fatalf("opts %+v", d.opts)
			}
		})
	}
}

func TestRejectsOversizedBodyAndReportsStoreFailure(t *testing.T) {
	rec := httptest.NewRecorder()
	newHandler(&store{}).ServeHTTP(rec, httptest.NewRequest("POST", "/", strings.NewReader(strings.Repeat("x", 2048))))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("code %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	newHandler(&store{err: errors.New("disk full")}).ServeHTTP(rec, httptest.NewRequest("POST", "/", strings.NewReader("{}")))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("code %d", rec.Code)
	}
}
