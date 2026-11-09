package web_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/grintzdel/spool/internal/domain/webhook"
)

func TestPages(t *testing.T) {
	req := stripeRequest("01REQ")
	req.SeenBody = []byte(`{"id":"evt_1"}`)
	e := newEnv(t, envOpts{}, req, webhook.NewRequest("01BIN", now, "PUT", "/bin/upload", "a=1", nil, []byte{0xff, 0x00}))
	e.store.attempts = []webhook.Attempt{{ID: "a1", RequestID: "01REQ", Kind: webhook.AttemptForward, StatusCode: 503, ResponseBody: []byte("upstream down")}}
	e.store.verifications = []webhook.Verification{{
		ID: "v1", RequestID: "01REQ", Provider: "stripe", Reason: "SECRET_MISMATCH",
		Details: `{"expected":"abc123","got":"def456","hints":["Wrong secret"]}`,
	}}

	tests := []struct {
		name     string
		target   string
		headers  []string
		want     int
		contains []string
		excludes []string
	}{
		{
			name: "list", target: "/", want: 200,
			contains: []string{"<!doctype html>", "01REQ", "/webhooks/stripe", "payment_intent.succeeded", "SECRET_MISMATCH", "503", "/bin/upload?a=1"},
		},
		{
			name: "list rows partial", target: "/?provider=stripe", headers: []string{"HX-Request", "true"}, want: 200,
			contains: []string{"01REQ"},
			excludes: []string{"<html", "/bin/upload"},
		},
		{
			name: "detail", target: "/r/01REQ", want: 200,
			contains: []string{
				"01REQ", "/webhooks/stripe", "Stripe-Signature", "&#34;evt_1&#34;", "00000000  7b",
				"curl -X POST", "http://localhost:3000/webhooks/stripe", "SECRET_MISMATCH", "abc123",
				"BODY_CHANGED", "upstream down", "503",
			},
		},
		{
			name: "detail by prefix", target: "/r/01R", want: 200,
			contains: []string{"01REQ"},
		},
		{
			name: "detail live partial", target: "/r/01REQ", headers: []string{"HX-Request", "true"}, want: 200,
			contains: []string{"SECRET_MISMATCH", "upstream down"},
			excludes: []string{"<html", "curl -X"},
		},
		{
			name: "binary detail", target: "/r/01BIN", want: 200,
			contains: []string{"/bin/upload", "00000000  ff 00"},
		},
		{name: "unknown detail", target: "/r/nope", want: http.StatusNotFound, contains: []string{"not found"}},
		{name: "unknown page", target: "/nope", want: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, e.h, "GET", tt.target, nil, tt.headers...)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", rec.Code, tt.want)
			}
			if tt.want == 200 && !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
				t.Errorf("Content-Type = %q", rec.Header().Get("Content-Type"))
			}
			body := rec.Body.String()
			for _, s := range tt.contains {
				if !strings.Contains(body, s) {
					t.Errorf("body does not contain %q", s)
				}
			}
			for _, s := range tt.excludes {
				if strings.Contains(body, s) {
					t.Errorf("body contains %q", s)
				}
			}
		})
	}
}

func TestAssets(t *testing.T) {
	e := newEnv(t, envOpts{})
	for _, path := range []string{"/assets/app.css", "/assets/app.js", "/assets/htmx.min.js"} {
		rec := do(t, e.h, "GET", path, nil)
		if rec.Code != http.StatusOK || rec.Body.Len() == 0 {
			t.Errorf("%s: status = %d, size %d", path, rec.Code, rec.Body.Len())
		}
	}
}
