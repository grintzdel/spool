package github_test

import (
	"testing"
	"time"

	"github.com/grintzdel/spool/internal/domain/signature"
	"github.com/grintzdel/spool/internal/domain/signature/github"
	"github.com/grintzdel/spool/internal/domain/webhook"
)

func request(headers webhook.Headers, body string) webhook.Request {
	return webhook.NewRequest("r1", time.Now(), "POST", "/", "", headers, []byte(body))
}

func TestOfficialTestVector(t *testing.T) {
	req := request(webhook.Headers{{
		Name:  "X-Hub-Signature-256",
		Value: "sha256=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17",
	}}, "Hello, World!")

	got := github.New().Verify(req, []byte("It's a Secret to Everybody"), signature.Options{})
	if got.Reason != signature.OKRaw {
		t.Fatalf("reason = %s (%+v)", got.Reason, got)
	}
}

func TestSignProducesTheOfficialVector(t *testing.T) {
	h, err := github.New().Sign([]byte("Hello, World!"), []byte("It's a Secret to Everybody"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	want := "sha256=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17"
	if got := h.Get("X-Hub-Signature-256"); got != want {
		t.Fatalf("got %s", got)
	}
}

func TestVerifyFailures(t *testing.T) {
	tests := []struct {
		name    string
		headers webhook.Headers
		want    signature.Reason
	}{
		{"missing", nil, signature.HeaderMissing},
		{"legacy sha1 only is ignored", webhook.Headers{{Name: "X-Hub-Signature", Value: "sha1=abc"}}, signature.HeaderMissing},
		{"no prefix", webhook.Headers{{Name: "X-Hub-Signature-256", Value: "757107ea"}}, signature.HeaderMalformed},
		{"bad hex", webhook.Headers{{Name: "X-Hub-Signature-256", Value: "sha256=zz"}}, signature.HeaderMalformed},
		{"wrong secret", webhook.Headers{{Name: "X-Hub-Signature-256", Value: "sha256=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17"}}, signature.SecretMismatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := github.New().Verify(request(tt.headers, "Hello, World!"), []byte("other"), signature.Options{})
			if got.Reason != tt.want {
				t.Fatalf("reason = %s, want %s", got.Reason, tt.want)
			}
		})
	}
}

func TestDetectAndEventType(t *testing.T) {
	p := github.New()
	h := webhook.Headers{{Name: "X-GitHub-Event", Value: "issues"}}
	if !p.Detect(h) {
		t.Fatal("not detected")
	}
	if got := p.EventType(h, []byte(`{"action":"opened"}`)); got != "issues.opened" {
		t.Fatalf("event = %q", got)
	}
	if got := p.EventType(webhook.Headers{{Name: "X-GitHub-Event", Value: "push"}}, []byte(`{}`)); got != "push" {
		t.Fatalf("event = %q", got)
	}
}
