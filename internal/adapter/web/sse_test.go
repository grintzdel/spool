package web_test

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/grintzdel/spool/internal/usecase"
)

func TestEventsStream(t *testing.T) {
	e := newEnv(t, envOpts{})
	srv := httptest.NewServer(e.h)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}

	lines := make(chan string)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			select {
			case lines <- sc.Text():
			case <-ctx.Done():
				return
			}
		}
	}()

	next := func() string {
		t.Helper()
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatal("stream closed")
			}
			return l
		case <-ctx.Done():
			t.Fatal("timed out reading the stream")
		}
		return ""
	}

	if l := next(); !strings.HasPrefix(l, ":") {
		t.Fatalf("first line = %q, want a comment", l)
	}
	e.bus.Publish(usecase.Event{Kind: usecase.EventAttempt, RequestID: "01REQ"})

	var got []string
	for len(got) < 2 {
		if l := next(); l != "" {
			got = append(got, l)
		}
	}
	if got[0] != "event: attempt" || got[1] != `data: {"kind":"attempt","request_id":"01REQ"}` {
		t.Errorf("event = %q", got)
	}

	cancel()
	deadline := time.Now().Add(2 * time.Second)
	for e.bus.count() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("subscription not released after the client left")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
