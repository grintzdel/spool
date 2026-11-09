package web_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/grintzdel/spool/internal/adapter/web"
	"github.com/grintzdel/spool/internal/domain/signature"
	"github.com/grintzdel/spool/internal/domain/signature/stripe"
	"github.com/grintzdel/spool/internal/domain/webhook"
	"github.com/grintzdel/spool/internal/usecase"
)

type memStore struct {
	mu            sync.Mutex
	requests      []webhook.Request
	attempts      []webhook.Attempt
	verifications []webhook.Verification
	lastFilter    usecase.ListFilter
}

func (m *memStore) SaveRequest(_ context.Context, r webhook.Request) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, r)
	return nil
}

func (m *memStore) FindRequest(_ context.Context, id string) (webhook.Request, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var found []webhook.Request
	for _, r := range m.requests {
		if strings.HasPrefix(r.ID, id) {
			found = append(found, r)
		}
	}
	switch len(found) {
	case 0:
		return webhook.Request{}, webhook.ErrNotFound
	case 1:
		return found[0], nil
	}
	return webhook.Request{}, webhook.ErrAmbiguous
}

func (m *memStore) ListRequests(_ context.Context, f usecase.ListFilter) ([]usecase.RequestSummary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastFilter = f
	var out []usecase.RequestSummary
	for _, r := range slices.Backward(m.requests) {
		if f.Provider != "" && r.Provider != f.Provider {
			continue
		}
		if f.BeforeID != "" && r.ID >= f.BeforeID {
			continue
		}
		if f.Limit > 0 && len(out) >= f.Limit {
			break
		}
		s := usecase.RequestSummary{Request: r}
		for _, a := range m.attempts {
			if a.RequestID == r.ID {
				s.LastAttempt = &a
			}
		}
		for _, v := range m.verifications {
			if v.RequestID == r.ID {
				s.Verification = &v
			}
		}
		out = append(out, s)
	}
	return out, nil
}

func (m *memStore) SetSeenBody(_ context.Context, id string, body []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.requests {
		if m.requests[i].ID == id {
			m.requests[i].SeenBody = body
			return nil
		}
	}
	return webhook.ErrNotFound
}

func (m *memStore) SaveAttempt(_ context.Context, a webhook.Attempt) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.attempts = append(m.attempts, a)
	return nil
}

func (m *memStore) ListAttempts(_ context.Context, id string) ([]webhook.Attempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []webhook.Attempt
	for _, a := range m.attempts {
		if a.RequestID == id {
			out = append(out, a)
		}
	}
	return out, nil
}

func (m *memStore) SaveVerification(_ context.Context, v webhook.Verification) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.verifications = append(m.verifications, v)
	return nil
}

func (m *memStore) LatestVerification(_ context.Context, id string) (*webhook.Verification, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, v := range slices.Backward(m.verifications) {
		if v.RequestID == id {
			return &v, nil
		}
	}
	return nil, nil
}

func (m *memStore) Prune(context.Context, time.Time, int) (int64, error) { return 0, nil }

func (m *memStore) seenBody(id string) []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.requests {
		if r.ID == id {
			return r.SeenBody
		}
	}
	return nil
}

type fakeSender struct {
	mu       sync.Mutex
	sent     []usecase.Outgoing
	response usecase.Response
}

func (f *fakeSender) Send(_ context.Context, out usecase.Outgoing) (usecase.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, out)
	return f.response, nil
}

func (f *fakeSender) outgoing() []usecase.Outgoing {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.sent)
}

type seqIDs struct {
	mu sync.Mutex
	n  int
}

func (s *seqIDs) NewID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	return fmt.Sprintf("gen%03d", s.n)
}

type mapSecrets map[string]string

func (m mapSecrets) Secret(provider, _ string) ([]byte, error) {
	s, ok := m[provider]
	if !ok {
		return nil, usecase.ErrNoSecret
	}
	return []byte(s), nil
}

type fakeBus struct {
	mu   sync.Mutex
	subs map[chan usecase.Event]struct{}
}

func newFakeBus() *fakeBus { return &fakeBus{subs: map[chan usecase.Event]struct{}{}} }

func (b *fakeBus) Subscribe() (<-chan usecase.Event, func()) {
	ch := make(chan usecase.Event, 8)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
	}
}

func (b *fakeBus) Publish(e usecase.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		ch <- e
	}
}

func (b *fakeBus) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}

var now = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

const stripeSecret = "whsec_test"

var stripeBody = []byte("{\n  \"id\": \"evt_1\",\n  \"type\": \"payment_intent.succeeded\"\n}\n")

func stripeRequest(id string) webhook.Request {
	h, _ := stripe.New().Sign(stripeBody, []byte(stripeSecret), now)
	h = append(webhook.Headers{
		{Name: "Host", Value: "localhost:7777"},
		{Name: "Content-Type", Value: "application/json"},
	}, h...)
	r := webhook.NewRequest(id, now, "POST", "/webhooks/stripe", "", h, stripeBody)
	r.Provider = "stripe"
	r.EventType = "payment_intent.succeeded"
	return r
}

type env struct {
	store  *memStore
	sender *fakeSender
	bus    *fakeBus
	h      http.Handler
}

type envOpts struct {
	secrets mapSecrets
	target  string
}

func newEnv(t *testing.T, opts envOpts, reqs ...webhook.Request) *env {
	t.Helper()
	if opts.secrets == nil {
		opts.secrets = mapSecrets{"stripe": stripeSecret}
	}
	reg := &signature.Registry{}
	reg.Register(stripe.New())
	clock := usecase.ClockFunc(func() time.Time { return now })
	store := &memStore{requests: reqs}
	sender := &fakeSender{response: usecase.Response{StatusCode: 200, Body: []byte(`{"received":true}`)}}
	bus := newFakeBus()
	ids := &seqIDs{}
	verifier := &usecase.Verifier{
		Store: store, Registry: reg, Secrets: opts.secrets, Clock: clock, IDs: ids,
		Publisher: usecase.NopPublisher{}, Tolerance: 300 * time.Second,
	}
	forwarder := &usecase.Forwarder{
		Store: store, Sender: sender, Publisher: usecase.NopPublisher{}, Clock: clock, IDs: ids,
		Registry: reg, Secrets: opts.secrets,
	}
	h := web.NewHandler(web.Deps{
		Queries:      usecase.Queries{Store: store},
		Replayer:     usecase.Replayer{Store: store, Forwarder: forwarder, Registry: reg, Clock: clock, DefaultTarget: opts.target},
		Verifier:     verifier,
		Seen:         usecase.SeenRecorder{Store: store, Publisher: usecase.NopPublisher{}},
		Events:       bus,
		ExportTarget: "http://localhost:3000",
	})
	return &env{store: store, sender: sender, bus: bus, h: h}
}
