package generic_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/grintzdel/spool/internal/domain/signature"
	"github.com/grintzdel/spool/internal/domain/signature/generic"
	"github.com/grintzdel/spool/internal/domain/webhook"
)

var receivedAt = time.Unix(1_700_000_000, 0)

func TestRoundTripAcrossConfigurations(t *testing.T) {
	configs := []generic.Config{
		{Header: "X-Signature", Algorithm: "sha256", Encoding: "hex"},
		{Header: "X-Signature", Algorithm: "sha1", Encoding: "base64", Prefix: "sha1="},
		{Header: "X-Signature", Algorithm: "sha512", Encoding: "hex", Template: "{ts}.{body}", TimestampHeader: "X-Timestamp"},
	}
	body := []byte(`{"a":1}`)
	for _, cfg := range configs {
		t.Run(cfg.Algorithm+"/"+cfg.Encoding, func(t *testing.T) {
			p, err := generic.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			h, err := p.Sign(body, []byte("s3cret"), receivedAt)
			if err != nil {
				t.Fatal(err)
			}
			req := webhook.NewRequest("r", receivedAt, "POST", "/", "", h, body)
			if got := p.Verify(req, []byte("s3cret"), signature.Options{Tolerance: time.Minute}); got.Reason != signature.OKRaw {
				t.Fatalf("reason = %s (%+v)", got.Reason, got)
			}
			if got := p.Verify(req, []byte("nope"), signature.Options{Tolerance: time.Minute}); got.Reason != signature.SecretMismatch {
				t.Fatalf("reason = %s", got.Reason)
			}
		})
	}
}

func TestVerifyFailures(t *testing.T) {
	p, _ := generic.New(generic.Config{
		Header: "X-Sig", Algorithm: "sha256", Encoding: "hex", Prefix: "v1=",
		Template: "{ts}.{body}", TimestampHeader: "X-Ts",
	})
	ts := strconv.FormatInt(receivedAt.Unix(), 10)
	old := strconv.FormatInt(receivedAt.Add(-time.Hour).Unix(), 10)
	tests := []struct {
		name    string
		headers webhook.Headers
		want    signature.Reason
	}{
		{"missing", nil, signature.HeaderMissing},
		{"missing prefix", webhook.Headers{{Name: "X-Sig", Value: "abcd"}, {Name: "X-Ts", Value: ts}}, signature.HeaderMalformed},
		{"missing timestamp", webhook.Headers{{Name: "X-Sig", Value: "v1=abcd"}}, signature.HeaderMalformed},
		{"expired", webhook.Headers{{Name: "X-Sig", Value: "v1=abcd"}, {Name: "X-Ts", Value: old}}, signature.TimestampExpired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := webhook.NewRequest("r", receivedAt, "POST", "/", "", tt.headers, []byte("{}"))
			if got := p.Verify(req, []byte("k"), signature.Options{Tolerance: 5 * time.Minute}); got.Reason != tt.want {
				t.Fatalf("reason = %s, want %s", got.Reason, tt.want)
			}
		})
	}
}

func TestNewValidatesConfig(t *testing.T) {
	bad := []generic.Config{
		{},
		{Header: "X", Algorithm: "md5"},
		{Header: "X", Encoding: "base32"},
		{Header: "X", Template: "{ts}.{body}"},
	}
	for _, cfg := range bad {
		if _, err := generic.New(cfg); err == nil {
			t.Errorf("expected error for %+v", cfg)
		}
	}
	p, err := generic.New(generic.Config{Header: "X-Sig"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "generic" || !p.Detect(webhook.Headers{{Name: "x-sig", Value: "1"}}) {
		t.Fatal("defaults not applied")
	}
}
