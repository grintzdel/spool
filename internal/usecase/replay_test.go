package usecase_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/grintzdel/spool/internal/usecase"
)

func newReplayer(store *memStore, sender *fakeSender, defaultTarget string) usecase.Replayer {
	return usecase.Replayer{
		Store: store, Forwarder: newForwarder(store, sender, usecase.NopPublisher{}), Registry: registry(),
		Clock: fixedClock(), DefaultTarget: defaultTarget, Tolerance: 5 * time.Minute,
	}
}

func TestReplaySendsNTimesToDefaultTarget(t *testing.T) {
	store := &memStore{}
	store.requests = append(store.requests, stripeRequest("01REQ", now))
	sender := &fakeSender{response: usecase.Response{StatusCode: 200}}

	res, err := newReplayer(store, sender, "http://app").Replay(context.Background(), usecase.ReplayInput{
		ID: "01R", Times: 2, Marker: true, SetHeaders: map[string]string{"X-Test": "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Attempts) != 2 || len(sender.sent) != 2 {
		t.Fatalf("attempts = %d", len(res.Attempts))
	}
	for _, out := range sender.sent {
		if !strings.HasPrefix(out.URL, "http://app/") || out.Headers.Get("X-Test") != "1" {
			t.Fatalf("outgoing %+v", out)
		}
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("unexpected warnings %v", res.Warnings)
	}
}

func TestReplayWarnsAboutStaleTimestampWithoutResign(t *testing.T) {
	store := &memStore{}
	store.requests = append(store.requests, stripeRequest("01OLD", now.Add(-time.Hour)))

	res, err := newReplayer(store, &fakeSender{response: usecase.Response{StatusCode: 200}}, "http://app").
		Replay(context.Background(), usecase.ReplayInput{ID: "01OLD"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "--resign") {
		t.Fatalf("warnings = %v", res.Warnings)
	}

	res, _ = newReplayer(store, &fakeSender{response: usecase.Response{StatusCode: 200}}, "http://app").
		Replay(context.Background(), usecase.ReplayInput{ID: "01OLD", Resign: true})
	if len(res.Warnings) != 0 {
		t.Fatalf("warnings with resign = %v", res.Warnings)
	}
}

func TestReplayWithoutTargetFails(t *testing.T) {
	store := &memStore{}
	store.requests = append(store.requests, stripeRequest("01REQ", now))
	_, err := newReplayer(store, &fakeSender{}, "").Replay(context.Background(), usecase.ReplayInput{ID: "01REQ"})
	if !errors.Is(err, usecase.ErrNoTarget) {
		t.Fatalf("err = %v", err)
	}
}
