package webhook_test

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/grintzdel/spool/internal/domain/webhook"
)

func TestNewRequestHashesTheExactBody(t *testing.T) {
	body := []byte("{\"description\":\"café\"}\n")
	sum := sha256.Sum256(body)

	r := webhook.NewRequest("01ABC", time.UnixMilli(1000), "POST", "/hook", "a=1", nil, body)

	if r.BodySHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("sha = %s", r.BodySHA256)
	}
	if string(r.Body) != string(body) {
		t.Fatal("body altered")
	}
}

func TestRequestURLJoinsTargetPathAndQuery(t *testing.T) {
	r := webhook.Request{Path: "/webhooks/stripe", Query: "x=1"}
	tests := map[string]string{
		"http://localhost:3000":      "http://localhost:3000/webhooks/stripe?x=1",
		"http://localhost:3000/":     "http://localhost:3000/webhooks/stripe?x=1",
		"http://localhost:3000/api":  "http://localhost:3000/api/webhooks/stripe?x=1",
		"http://localhost:3000/api/": "http://localhost:3000/api/webhooks/stripe?x=1",
	}
	for base, want := range tests {
		if got := r.URLOn(base); got != want {
			t.Errorf("URLOn(%q) = %q, want %q", base, got, want)
		}
	}
}

func TestAttemptFailed(t *testing.T) {
	tests := []struct {
		name string
		a    webhook.Attempt
		want bool
	}{
		{"2xx", webhook.Attempt{StatusCode: 204}, false},
		{"4xx", webhook.Attempt{StatusCode: 400}, true},
		{"transport error", webhook.Attempt{Error: "connection refused"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.Failed(); got != tt.want {
				t.Fatalf("Failed() = %v, want %v", got, tt.want)
			}
		})
	}
}
