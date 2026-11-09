package usecase_test

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/grintzdel/spool/internal/domain/webhook"
	"github.com/grintzdel/spool/internal/usecase"
)

func TestCaptureStoresBytesDetectsProviderAndPublishes(t *testing.T) {
	store, pub := &memStore{}, &recorder{}
	c := usecase.Capturer{Store: store, Registry: registry(), Publisher: pub, Clock: fixedClock(), IDs: &seqIDs{}}
	src := stripeRequest("x", now)

	got, err := c.Capture(context.Background(), usecase.Incoming{
		Method: "POST", Path: "/webhooks/stripe", Query: "a=1", Headers: src.Headers, Body: stripeBody,
	})
	if err != nil {
		t.Fatal(err)
	}

	if got.ID != "id001" || !got.ReceivedAt.Equal(now) || got.Query != "a=1" {
		t.Fatalf("unexpected request %+v", got)
	}
	if got.Provider != "stripe" || got.EventType != "payment_intent.succeeded" {
		t.Fatalf("provider=%q event=%q", got.Provider, got.EventType)
	}
	if got.BodySHA256 != webhook.SHA256Hex(stripeBody) || string(store.requests[0].Body) != string(stripeBody) {
		t.Fatal("body not stored byte for byte")
	}
	if d := cmp.Diff([]usecase.EventKind{usecase.EventRequest}, pub.kinds()); d != "" {
		t.Fatal(d)
	}
}

func TestCaptureUnknownProviderLeavesItEmpty(t *testing.T) {
	c := usecase.Capturer{Store: &memStore{}, Registry: registry(), Publisher: usecase.NopPublisher{}, Clock: fixedClock(), IDs: &seqIDs{}}
	got, err := c.Capture(context.Background(), usecase.Incoming{Method: "POST", Path: "/", Body: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	if got.Provider != "" || got.EventType != "" {
		t.Fatalf("provider=%q", got.Provider)
	}
}
