package stripe_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/grintzdel/spool/internal/domain/signature"
	"github.com/grintzdel/spool/internal/domain/signature/stripe"
	"github.com/grintzdel/spool/internal/domain/webhook"
)

const secret = "whsec_test_secret"

var (
	body       = []byte("{\n  \"id\": \"evt_1\",\n  \"type\": \"payment_intent.succeeded\"\n}")
	receivedAt = time.Unix(1_700_000_000, 0)
)

func signedRequest(t *testing.T, ts time.Time, key string) webhook.Request {
	t.Helper()
	h, err := stripe.New().Sign(body, []byte(key), ts)
	if err != nil {
		t.Fatal(err)
	}
	return webhook.NewRequest("r1", receivedAt, "POST", "/", "", h, body)
}

func withHeader(value string) webhook.Request {
	return webhook.NewRequest("r1", receivedAt, "POST", "/", "",
		webhook.Headers{{Name: "Stripe-Signature", Value: value}}, body)
}

func TestVerify(t *testing.T) {
	opts := signature.Options{Tolerance: 300 * time.Second}
	valid := signedRequest(t, receivedAt, secret)
	goodSig := strings.TrimPrefix(strings.Split(valid.Headers.Get("Stripe-Signature"), ",")[1], "v1=")
	ts := receivedAt.Unix()

	tests := []struct {
		name string
		req  webhook.Request
		want signature.Reason
	}{
		{"valid signature", valid, signature.OKRaw},
		{"signed 4 minutes before reception", signedRequest(t, receivedAt.Add(-4*time.Minute), secret), signature.OKRaw},
		{"wrong secret", signedRequest(t, receivedAt, "whsec_other"), signature.SecretMismatch},
		{"timestamp outside tolerance", signedRequest(t, receivedAt.Add(-301*time.Second), secret), signature.TimestampExpired},
		{"header missing", webhook.NewRequest("r1", receivedAt, "POST", "/", "", nil, body), signature.HeaderMissing},
		{"no t=", withHeader("v1=" + goodSig), signature.HeaderMalformed},
		{"no v1=", withHeader(fmt.Sprintf("t=%d,v0=abc", ts)), signature.HeaderMalformed},
		{"non numeric t", withHeader("t=abc,v1=" + goodSig), signature.HeaderMalformed},
		{"rotation: second v1 matches", withHeader(fmt.Sprintf("t=%d,v1=%s,v1=%s", ts, strings.Repeat("0", 64), goodSig)), signature.OKRaw},
		{"rotation: no v1 matches", withHeader(fmt.Sprintf("t=%d,v1=%s,v1=%s", ts, strings.Repeat("0", 64), strings.Repeat("1", 64))), signature.SecretMismatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripe.New().Verify(tt.req, []byte(secret), opts)
			if got.Reason != tt.want {
				t.Fatalf("reason = %s, want %s (%+v)", got.Reason, tt.want, got)
			}
			if got.OK != (tt.want == signature.OKRaw) {
				t.Fatalf("OK = %v for %s", got.OK, got.Reason)
			}
		})
	}
}

func TestVerifyJudgesTimestampAgainstReceptionNotNow(t *testing.T) {
	old := time.Unix(1_000_000_000, 0)
	h, _ := stripe.New().Sign(body, []byte(secret), old)
	req := webhook.NewRequest("r1", old.Add(10*time.Second), "POST", "/", "", h, body)

	got := stripe.New().Verify(req, []byte(secret), signature.Options{Tolerance: 300 * time.Second})
	if got.Reason != signature.OKRaw {
		t.Fatalf("reason = %s", got.Reason)
	}
}

func TestDetectAndEventType(t *testing.T) {
	p := stripe.New()
	h := webhook.Headers{{Name: "stripe-signature", Value: "t=1,v1=a"}}
	if !p.Detect(h) {
		t.Fatal("not detected")
	}
	if p.Detect(webhook.Headers{{Name: "X-Hub-Signature-256", Value: "x"}}) {
		t.Fatal("false positive")
	}
	if got := p.EventType(h, body); got != "payment_intent.succeeded" {
		t.Fatalf("event type = %q", got)
	}
	if got := p.EventType(h, []byte("not json")); got != "" {
		t.Fatalf("event type on garbage = %q", got)
	}
}

func FuzzParseHeader(f *testing.F) {
	f.Add("t=1700000000,v1=abc,v1=def")
	f.Add("t=,v1=")
	f.Add(",,,=,t=1=2")
	f.Add("v1=" + strings.Repeat("a", 64))
	f.Fuzz(func(t *testing.T, header string) {
		ts, sigs, err := stripe.ParseHeader(header)
		if err != nil {
			return
		}
		if len(sigs) == 0 {
			t.Fatalf("no error but no signature for %q", header)
		}
		again, sigs2, err := stripe.ParseHeader(stripe.FormatHeader(ts, sigs))
		if err != nil || !again.Equal(ts) || len(sigs2) != len(sigs) {
			t.Fatalf("round trip failed for %q", header)
		}
	})
}
