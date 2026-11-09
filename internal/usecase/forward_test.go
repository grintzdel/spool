package usecase_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/grintzdel/spool/internal/domain/signature"
	"github.com/grintzdel/spool/internal/domain/signature/stripe"
	"github.com/grintzdel/spool/internal/domain/webhook"
	"github.com/grintzdel/spool/internal/usecase"
)

func newForwarder(store *memStore, sender *fakeSender, pub usecase.Publisher) *usecase.Forwarder {
	return &usecase.Forwarder{
		Store: store, Sender: sender, Publisher: pub, Clock: fixedClock(), IDs: &seqIDs{},
		Registry: registry(), Secrets: mapSecrets{"stripe": stripeSecret},
	}
}

func TestDeliverRewritesHeadersAndRecordsAttempt(t *testing.T) {
	store, pub := &memStore{}, &recorder{}
	sender := &fakeSender{response: usecase.Response{StatusCode: 400, Body: []byte("invalid signature")}}
	req := stripeRequest("req1", now)

	a, err := newForwarder(store, sender, pub).Deliver(context.Background(), req, usecase.DeliverOptions{
		Kind: webhook.AttemptForward, Target: "http://localhost:3000",
	})
	if err != nil {
		t.Fatal(err)
	}

	out := sender.sent[0]
	if out.URL != "http://localhost:3000/webhooks/stripe" || out.Method != "POST" || string(out.Body) != string(stripeBody) {
		t.Fatalf("bad outgoing %+v", out)
	}
	for _, h := range []string{"Host", "Content-Length", "Connection"} {
		if out.Headers.Has(h) {
			t.Errorf("%s should be stripped", h)
		}
	}
	if out.Headers.Get("X-Spool-Id") != "req1" || out.Headers.Has("X-Spool-Replay") {
		t.Fatalf("markers wrong: %v", out.Headers)
	}
	if out.Headers.Get("Stripe-Signature") != req.Headers.Get("Stripe-Signature") {
		t.Fatal("signature must be untouched without resign")
	}
	if a.StatusCode != 400 || string(a.ResponseBody) != "invalid signature" || !a.Failed() || a.Kind != webhook.AttemptForward {
		t.Fatalf("attempt %+v", a)
	}
	if len(store.attempts) != 1 || pub.kinds()[0] != usecase.EventAttempt {
		t.Fatal("attempt not stored or not published")
	}
}

func TestDeliverDropsHeadersNamedInConnection(t *testing.T) {
	sender := &fakeSender{response: usecase.Response{StatusCode: 200}}
	req := webhook.NewRequest("r", now, "POST", "/", "", webhook.Headers{
		{Name: "Connection", Value: "X-Hop, close"}, {Name: "X-Hop", Value: "1"}, {Name: "Proxy-Authorization", Value: "x"},
		{Name: "Keep-Alive", Value: "1"}, {Name: "X-Keep", Value: "1"},
	}, nil)

	if _, err := newForwarder(&memStore{}, sender, usecase.NopPublisher{}).Deliver(context.Background(), req, usecase.DeliverOptions{Target: "http://t"}); err != nil {
		t.Fatal(err)
	}
	h := sender.sent[0].Headers
	if h.Has("X-Hop") || h.Has("Proxy-Authorization") || h.Has("Keep-Alive") || !h.Has("X-Keep") {
		t.Fatalf("headers %v", h)
	}
}

func TestDeliverReplayResignsAndMarks(t *testing.T) {
	sender := &fakeSender{response: usecase.Response{StatusCode: 200}}
	old := stripeRequest("req1", now.Add(-time.Hour))

	a, err := newForwarder(&memStore{}, sender, usecase.NopPublisher{}).Deliver(context.Background(), old, usecase.DeliverOptions{
		Kind: webhook.AttemptReplay, Target: "http://t", Resign: true, Marker: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	out := sender.sent[0]
	if out.Headers.Get("X-Spool-Replay") != "req1" || !a.Resigned {
		t.Fatal("replay marker or resigned flag missing")
	}
	if n := len(out.Headers.Values("Stripe-Signature")); n != 1 {
		t.Fatalf("expected one signature header, got %d", n)
	}
	resent := webhook.NewRequest("x", now, "POST", "/", "", out.Headers, out.Body)
	if r := stripe.New().Verify(resent, []byte(stripeSecret), signature.Options{Tolerance: 5 * time.Minute}); r.Reason != signature.OKRaw {
		t.Fatalf("resigned request does not verify now: %s", r.Reason)
	}
}

func TestDeliverResignNeedsProviderAndSecret(t *testing.T) {
	f := newForwarder(&memStore{}, &fakeSender{}, usecase.NopPublisher{})
	f.Secrets = mapSecrets{}
	_, err := f.Deliver(context.Background(), stripeRequest("r", now), usecase.DeliverOptions{Target: "http://t", Resign: true})
	if !errors.Is(err, usecase.ErrNoSecret) {
		t.Fatalf("err = %v", err)
	}
	_, err = f.Deliver(context.Background(), webhook.NewRequest("r", now, "POST", "/", "", nil, nil), usecase.DeliverOptions{Target: "http://t", Resign: true})
	if !errors.Is(err, usecase.ErrNoProvider) {
		t.Fatalf("err = %v", err)
	}
}

func TestDeliverRecordsTransportErrorAndTruncatesResponse(t *testing.T) {
	store := &memStore{}
	f := newForwarder(store, &fakeSender{err: errors.New("connection refused")}, usecase.NopPublisher{})
	a, err := f.Deliver(context.Background(), stripeRequest("r", now), usecase.DeliverOptions{Target: "http://t"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Error != "connection refused" || !a.Failed() {
		t.Fatalf("attempt %+v", a)
	}

	f = newForwarder(store, &fakeSender{response: usecase.Response{StatusCode: 200, Body: []byte(strings.Repeat("x", 100_000))}}, usecase.NopPublisher{})
	a, _ = f.Deliver(context.Background(), stripeRequest("r", now), usecase.DeliverOptions{Target: "http://t"})
	if len(a.ResponseBody) != usecase.MaxStoredResponse {
		t.Fatalf("response not truncated: %d", len(a.ResponseBody))
	}
}

func TestDispatcherDrainsQueueOnClose(t *testing.T) {
	store := &memStore{}
	sender := &fakeSender{response: usecase.Response{StatusCode: 200}}
	d := usecase.NewDispatcher(newForwarder(store, sender, usecase.NopPublisher{}), "http://t", 3)
	for i := range 20 {
		d.Enqueue(stripeRequest(string(rune('a'+i)), now))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if len(store.attempts) != 20 {
		t.Fatalf("attempts = %d", len(store.attempts))
	}
}
