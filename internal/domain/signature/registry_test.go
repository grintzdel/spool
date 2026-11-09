package signature_test

import (
	"testing"
	"time"

	"github.com/grintzdel/spool/internal/domain/signature"
	"github.com/grintzdel/spool/internal/domain/webhook"
)

type fakeProvider struct {
	name   string
	header string
}

func (f fakeProvider) Name() string                                            { return f.name }
func (f fakeProvider) Detect(h webhook.Headers) bool                           { return h.Has(f.header) }
func (f fakeProvider) EventType(webhook.Headers, []byte) string                { return "" }
func (f fakeProvider) Sign([]byte, []byte, time.Time) (webhook.Headers, error) { return nil, nil }
func (f fakeProvider) Verify(webhook.Request, []byte, signature.Options) signature.Result {
	return signature.Result{}
}

func TestRegistryDetectsInRegistrationOrder(t *testing.T) {
	var r signature.Registry
	r.Register(fakeProvider{name: "first", header: "X-A"})
	r.Register(fakeProvider{name: "second", header: "X-A"})

	p, ok := r.Detect(webhook.Headers{{Name: "x-a", Value: "1"}})
	if !ok || p.Name() != "first" {
		t.Fatalf("Detect = %v, %v", p, ok)
	}
	if _, ok := r.Detect(webhook.Headers{{Name: "X-B", Value: "1"}}); ok {
		t.Fatal("detected unknown provider")
	}
}

func TestRegistryRegisterReplacesSameName(t *testing.T) {
	var r signature.Registry
	r.Register(fakeProvider{name: "p", header: "X-A"})
	r.Register(fakeProvider{name: "p", header: "X-B"})

	if got := r.Names(); len(got) != 1 {
		t.Fatalf("Names = %v", got)
	}
	if _, ok := r.Detect(webhook.Headers{{Name: "X-B", Value: "1"}}); !ok {
		t.Fatal("replacement not used")
	}
	if _, ok := r.Lookup("P"); !ok {
		t.Fatal("Lookup should be case-insensitive")
	}
}
