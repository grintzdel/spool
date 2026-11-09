package bootstrap_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/grintzdel/spool/internal/adapter/config"
	"github.com/grintzdel/spool/internal/bootstrap"
	"github.com/grintzdel/spool/internal/domain/webhook"
	"github.com/grintzdel/spool/internal/usecase"
)

func newApp(tb testing.TB, providers map[string]config.ProviderConfig) *bootstrap.App {
	tb.Helper()
	cfg := config.Default()
	cfg.DB = filepath.Join(tb.TempDir(), "spool.db")
	cfg.Providers = providers
	app, err := bootstrap.New(cfg)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { app.Close() })
	return app
}

func TestGenericProvidersFromConfigAreDetected(t *testing.T) {
	app := newApp(t, map[string]config.ProviderConfig{"acme": {Header: "X-Acme-Signature"}})
	req, err := app.Capturer.Capture(context.Background(), usecase.Incoming{
		Method: "POST", Path: "/", Headers: webhook.Headers{{Name: "X-Acme-Signature", Value: "abc"}}, Body: []byte("{}"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if req.Provider != "acme" {
		t.Fatalf("provider = %q", req.Provider)
	}
}

var payload = []byte(`{"id":"evt_1","object":"event","type":"payment_intent.succeeded","data":{"object":{"amount":2000}}}`)

func BenchmarkCapture(b *testing.B) {
	app := newApp(b, nil)
	in := usecase.Incoming{Method: "POST", Path: "/webhooks/stripe", Body: payload,
		Headers: webhook.Headers{{Name: "Stripe-Signature", Value: "t=1,v1=abc"}}}
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for b.Loop() {
		if _, err := app.Capturer.Capture(context.Background(), in); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkForward(b *testing.B) {
	app := newApp(b, nil)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer target.Close()
	req, _ := app.Capturer.Capture(context.Background(), usecase.Incoming{Method: "POST", Path: "/", Body: payload})
	b.ResetTimer()
	for b.Loop() {
		if _, err := app.Forwarder.Deliver(context.Background(), req, usecase.DeliverOptions{Target: target.URL}); err != nil {
			b.Fatal(err)
		}
	}
}
