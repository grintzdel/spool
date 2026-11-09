package usecase_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/grintzdel/spool/internal/domain/signature"
	"github.com/grintzdel/spool/internal/domain/webhook"
	"github.com/grintzdel/spool/internal/usecase"
)

func newVerifier(store *memStore, secrets mapSecrets) *usecase.Verifier {
	return &usecase.Verifier{
		Store: store, Registry: registry(), Secrets: secrets, Clock: fixedClock(), IDs: &seqIDs{},
		Publisher: usecase.NopPublisher{}, Tolerance: 300 * time.Second,
	}
}

func compact(t *testing.T, b []byte) []byte {
	t.Helper()
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(v)
	return out
}

func TestVerify(t *testing.T) {
	tests := []struct {
		name    string
		secrets mapSecrets
		input   func(t *testing.T) usecase.VerifyInput
		seen    func(t *testing.T) []byte
		want    signature.Reason
		ok      bool
	}{
		{
			name:    "raw body verifies",
			secrets: mapSecrets{"stripe": stripeSecret},
			input:   func(*testing.T) usecase.VerifyInput { return usecase.VerifyInput{ID: "01REQ"} },
			want:    signature.OKRaw, ok: true,
		},
		{
			name:    "wrong secret",
			secrets: mapSecrets{"stripe": "whsec_nope"},
			input:   func(*testing.T) usecase.VerifyInput { return usecase.VerifyInput{ID: "01REQ"} },
			want:    signature.SecretMismatch,
		},
		{
			name:    "secret from --secret-env",
			secrets: mapSecrets{"env:MY_SECRET": stripeSecret},
			input:   func(*testing.T) usecase.VerifyInput { return usecase.VerifyInput{ID: "01REQ", SecretEnv: "MY_SECRET"} },
			want:    signature.OKRaw, ok: true,
		},
		{
			name:    "app body from --body-file reformatted",
			secrets: mapSecrets{"stripe": stripeSecret},
			input: func(t *testing.T) usecase.VerifyInput {
				return usecase.VerifyInput{ID: "01REQ", AppBody: compact(t, stripeBody)}
			},
			want: signature.BodyReformatted, ok: true,
		},
		{
			name:    "app body stored by the middleware",
			secrets: mapSecrets{"stripe": stripeSecret},
			input:   func(*testing.T) usecase.VerifyInput { return usecase.VerifyInput{ID: "01REQ"} },
			seen:    func(*testing.T) []byte { return stripeBody[:20] },
			want:    signature.BodyChanged, ok: true,
		},
		{
			name:    "identical app body stays OK_RAW",
			secrets: mapSecrets{"stripe": stripeSecret},
			input:   func(*testing.T) usecase.VerifyInput { return usecase.VerifyInput{ID: "01REQ"} },
			seen:    func(*testing.T) []byte { return stripeBody },
			want:    signature.OKRaw, ok: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &memStore{}
			req := stripeRequest("01REQ", now)
			if tt.seen != nil {
				req.SeenBody = tt.seen(t)
			}
			store.requests = append(store.requests, req)

			rep, err := newVerifier(store, tt.secrets).Verify(context.Background(), tt.input(t))
			if err != nil {
				t.Fatal(err)
			}
			if rep.Reason() != tt.want || rep.Result.OK != tt.ok {
				t.Fatalf("reason = %s ok=%v, want %s ok=%v", rep.Reason(), rep.Result.OK, tt.want, tt.ok)
			}
			if len(store.verifications) != 1 || store.verifications[0].Reason != string(tt.want) {
				t.Fatalf("verification not recorded: %+v", store.verifications)
			}
			if tt.want.IsBody() && (rep.Diff == nil || len(rep.Fixes) == 0) {
				t.Fatal("body reasons must carry the diff and fixes")
			}
		})
	}
}

func TestVerifyErrors(t *testing.T) {
	store := &memStore{}
	store.requests = append(store.requests,
		stripeRequest("01REQ", now),
		webhook.NewRequest("02RAW", now, "POST", "/", "", nil, []byte("x")),
	)
	v := newVerifier(store, mapSecrets{})

	if _, err := v.Verify(context.Background(), usecase.VerifyInput{ID: "01REQ"}); !errors.Is(err, usecase.ErrNoSecret) {
		t.Errorf("no secret: %v", err)
	}
	if _, err := v.Verify(context.Background(), usecase.VerifyInput{ID: "02RAW"}); !errors.Is(err, usecase.ErrNoProvider) {
		t.Errorf("undetected: %v", err)
	}
	if _, err := v.Verify(context.Background(), usecase.VerifyInput{ID: "01REQ", Provider: "nope"}); !errors.Is(err, usecase.ErrUnknownProvider) {
		t.Errorf("unknown: %v", err)
	}
	if _, err := v.Verify(context.Background(), usecase.VerifyInput{ID: "zz"}); !errors.Is(err, webhook.ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
}

func TestRecordSeenStoresBodyAndDiagnosesAutomatically(t *testing.T) {
	store := &memStore{}
	store.requests = append(store.requests, stripeRequest("01REQ", now))
	pub := &recorder{}
	verifier := newVerifier(store, mapSecrets{"stripe": stripeSecret})
	verifier.Publisher = pub
	rec := usecase.SeenRecorder{Store: store, Verifier: verifier, Publisher: pub}
	appBody := compact(t, stripeBody)

	if err := rec.RecordSeen(context.Background(), "01REQ", appBody, "deadbeef"); !errors.Is(err, usecase.ErrSeenMismatch) {
		t.Fatalf("sha mismatch accepted: %v", err)
	}
	if err := rec.RecordSeen(context.Background(), "01REQ", appBody, webhook.SHA256Hex(appBody)); err != nil {
		t.Fatal(err)
	}

	if string(store.requests[0].SeenBody) != string(appBody) {
		t.Fatal("seen body not stored")
	}
	if len(store.verifications) != 1 || store.verifications[0].Reason != string(signature.BodyReformatted) {
		t.Fatalf("verifications = %+v", store.verifications)
	}
}
