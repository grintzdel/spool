package usecase_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/grintzdel/spool/internal/domain/signature"
	"github.com/grintzdel/spool/internal/domain/signature/github"
	"github.com/grintzdel/spool/internal/domain/signature/stripe"
	"github.com/grintzdel/spool/internal/domain/webhook"
	"github.com/grintzdel/spool/internal/usecase"
)

type memStore struct {
	mu            sync.Mutex
	requests      []webhook.Request
	attempts      []webhook.Attempt
	verifications []webhook.Verification
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
	var out []usecase.RequestSummary
	for _, r := range slices.Backward(m.requests) {
		out = append(out, usecase.RequestSummary{Request: r})
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

func (m *memStore) Prune(_ context.Context, before time.Time, keep int) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var kept []webhook.Request
	for _, r := range slices.Backward(m.requests) {
		if r.ReceivedAt.Before(before) || (keep > 0 && len(kept) >= keep) {
			continue
		}
		kept = append([]webhook.Request{r}, kept...)
	}
	n := int64(len(m.requests) - len(kept))
	m.requests = kept
	return n, nil
}

type fakeSender struct {
	mu       sync.Mutex
	sent     []usecase.Outgoing
	response usecase.Response
	err      error
}

func (f *fakeSender) Send(_ context.Context, out usecase.Outgoing) (usecase.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, out)
	return f.response, f.err
}

type recorder struct {
	mu     sync.Mutex
	events []usecase.Event
}

func (r *recorder) Publish(e usecase.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recorder) kinds() []usecase.EventKind {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []usecase.EventKind
	for _, e := range r.events {
		out = append(out, e.Kind)
	}
	return out
}

type seqIDs struct {
	mu sync.Mutex
	n  int
}

func (s *seqIDs) NewID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	return fmt.Sprintf("id%03d", s.n)
}

type mapSecrets map[string]string

func (m mapSecrets) Secret(provider, env string) ([]byte, error) {
	key := provider
	if env != "" {
		key = "env:" + env
	}
	s, ok := m[key]
	if !ok {
		return nil, usecase.ErrNoSecret
	}
	return []byte(s), nil
}

var now = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

func fixedClock() usecase.Clock { return usecase.ClockFunc(func() time.Time { return now }) }

func registry() *signature.Registry {
	r := &signature.Registry{}
	r.Register(stripe.New())
	r.Register(github.New())
	return r
}

const stripeSecret = "whsec_test"

var stripeBody = []byte("{\n  \"id\": \"evt_1\",\n  \"type\": \"payment_intent.succeeded\",\n  \"description\": \"Caf\\u00e9\"\n}\n")

func stripeRequest(id string, receivedAt time.Time) webhook.Request {
	h, _ := stripe.New().Sign(stripeBody, []byte(stripeSecret), receivedAt)
	h = append(webhook.Headers{
		{Name: "Host", Value: "localhost:7777"},
		{Name: "Content-Type", Value: "application/json"},
		{Name: "Content-Length", Value: fmt.Sprint(len(stripeBody))},
		{Name: "Connection", Value: "keep-alive"},
	}, h...)
	r := webhook.NewRequest(id, receivedAt, "POST", "/webhooks/stripe", "", h, stripeBody)
	r.Provider = "stripe"
	r.EventType = "payment_intent.succeeded"
	return r
}
