package web_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/grintzdel/spool/internal/domain/webhook"
	"github.com/grintzdel/spool/internal/usecase"
)

func do(t *testing.T, h http.Handler, method, target string, body io.Reader, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, body)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return v
}

type listResponse struct {
	Items []struct {
		ID       string `json:"id"`
		Path     string `json:"path"`
		Provider string `json:"provider"`
	} `json:"items"`
	Next string `json:"next"`
}

func TestListRequests(t *testing.T) {
	e := newEnv(t, envOpts{}, stripeRequest("01A"), stripeRequest("01B"), stripeRequest("01C"))

	tests := []struct {
		name     string
		query    string
		wantIDs  []string
		wantNext string
	}{
		{name: "first page", query: "?limit=2", wantIDs: []string{"01C", "01B"}, wantNext: "01B"},
		{name: "second page", query: "?limit=2&before=01B", wantIDs: []string{"01A"}},
		{name: "default limit", query: "", wantIDs: []string{"01C", "01B", "01A"}},
		{name: "provider filter", query: "?provider=github", wantIDs: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, e.h, "GET", "/api/requests"+tt.query, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
			}
			got := decode[listResponse](t, rec)
			var ids []string
			for _, it := range got.Items {
				ids = append(ids, it.ID)
			}
			if diff := cmp.Diff(tt.wantIDs, ids); diff != "" {
				t.Errorf("ids (-want +got):\n%s", diff)
			}
			if got.Next != tt.wantNext {
				t.Errorf("next = %q, want %q", got.Next, tt.wantNext)
			}
		})
	}
}

func TestListRequestsFilters(t *testing.T) {
	e := newEnv(t, envOpts{}, stripeRequest("01A"))
	rec := do(t, e.h, "GET", "/api/requests?provider=stripe&event=x.y&failed=true&limit=900", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	want := usecase.ListFilter{Provider: "stripe", EventType: "x.y", FailedOnly: true, Limit: 501}
	if diff := cmp.Diff(want, e.store.lastFilter); diff != "" {
		t.Errorf("filter (-want +got):\n%s", diff)
	}
}

func TestListRequestsBadParams(t *testing.T) {
	e := newEnv(t, envOpts{})
	for _, q := range []string{"?limit=abc", "?limit=-1", "?failed=maybe"} {
		rec := do(t, e.h, "GET", "/api/requests"+q, nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", q, rec.Code)
		}
	}
}

type detailResponse struct {
	ID           string      `json:"id"`
	Method       string      `json:"method"`
	Path         string      `json:"path"`
	Headers      [][2]string `json:"headers"`
	Body         string      `json:"body"`
	BodyEncoding string      `json:"body_encoding"`
	BodySHA256   string      `json:"body_sha256"`
	Provider     string      `json:"provider"`
	EventType    string      `json:"event_type"`
	Seen         *struct {
		Size      int  `json:"size"`
		Identical bool `json:"identical"`
		Diff      *struct {
			Reason string `json:"reason"`
			Offset int    `json:"offset"`
		} `json:"diff"`
	} `json:"seen"`
	Attempts []struct {
		Kind       string `json:"kind"`
		StatusCode int    `json:"status_code"`
	} `json:"attempts"`
	Verification *struct {
		Reason string `json:"reason"`
	} `json:"verification"`
}

func TestRequestDetail(t *testing.T) {
	seen := stripeRequest("01SEEN")
	seen.SeenBody = []byte(`{"id":"evt_1"}`)
	binary := webhook.NewRequest("01BIN", now, "PUT", "/bin", "", nil, []byte{0xff, 0x00, 0xfe})
	e := newEnv(t, envOpts{}, stripeRequest("01REQ"), seen, binary)
	e.store.attempts = []webhook.Attempt{{ID: "a1", RequestID: "01REQ", Kind: webhook.AttemptForward, StatusCode: 202}}
	e.store.verifications = []webhook.Verification{{ID: "v1", RequestID: "01REQ", Reason: "OK_RAW", OK: true}}

	t.Run("full detail", func(t *testing.T) {
		rec := do(t, e.h, "GET", "/api/requests/01REQ", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
		}
		got := decode[detailResponse](t, rec)
		if got.ID != "01REQ" || got.Method != "POST" || got.Path != "/webhooks/stripe" {
			t.Errorf("request fields = %+v", got)
		}
		if got.Body != string(stripeBody) || got.BodyEncoding != "utf8" || got.BodySHA256 != webhook.SHA256Hex(stripeBody) {
			t.Errorf("body = %q (%s, %s)", got.Body, got.BodyEncoding, got.BodySHA256)
		}
		if got.Provider != "stripe" || got.EventType != "payment_intent.succeeded" {
			t.Errorf("provider/event = %q/%q", got.Provider, got.EventType)
		}
		if len(got.Headers) == 0 || got.Headers[0] != [2]string{"Host", "localhost:7777"} {
			t.Errorf("headers = %v", got.Headers)
		}
		if len(got.Attempts) != 1 || got.Attempts[0].StatusCode != 202 || got.Attempts[0].Kind != "forward" {
			t.Errorf("attempts = %+v", got.Attempts)
		}
		if got.Verification == nil || got.Verification.Reason != "OK_RAW" {
			t.Errorf("verification = %+v", got.Verification)
		}
		if got.Seen != nil {
			t.Errorf("seen = %+v, want null", got.Seen)
		}
	})

	t.Run("seen body diff", func(t *testing.T) {
		got := decode[detailResponse](t, do(t, e.h, "GET", "/api/requests/01SEEN", nil))
		if got.Seen == nil || got.Seen.Identical || got.Seen.Diff == nil {
			t.Fatalf("seen = %+v", got.Seen)
		}
		if got.Seen.Diff.Reason != "BODY_CHANGED" || got.Seen.Size != len(seen.SeenBody) {
			t.Errorf("seen diff = %+v", got.Seen.Diff)
		}
	})

	t.Run("binary body", func(t *testing.T) {
		got := decode[detailResponse](t, do(t, e.h, "GET", "/api/requests/01BIN", nil))
		if got.BodyEncoding != "base64" || got.Body != "/wD+" {
			t.Errorf("body = %q (%s)", got.Body, got.BodyEncoding)
		}
	})

	t.Run("errors", func(t *testing.T) {
		for target, want := range map[string]int{
			"/api/requests/nope": http.StatusNotFound,
			"/api/requests/01":   http.StatusBadRequest,
		} {
			rec := do(t, e.h, "GET", target, nil)
			if rec.Code != want {
				t.Errorf("%s: status = %d, want %d", target, rec.Code, want)
			}
			if got := decode[map[string]string](t, rec); got["error"] == "" {
				t.Errorf("%s: missing error message", target)
			}
		}
	})
}

func TestSeen(t *testing.T) {
	body := []byte(`{"id":"evt_1"}`)
	tests := []struct {
		name     string
		id       string
		sha      string
		want     int
		wantSeen []byte
	}{
		{name: "stored", id: "01REQ", sha: webhook.SHA256Hex(body), want: http.StatusNoContent, wantSeen: body},
		{name: "without sha", id: "01REQ", want: http.StatusNoContent, wantSeen: body},
		{name: "sha mismatch", id: "01REQ", sha: webhook.SHA256Hex([]byte("other")), want: http.StatusBadRequest},
		{name: "unknown id", id: "nope", sha: webhook.SHA256Hex(body), want: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, envOpts{}, stripeRequest("01REQ"))
			var headers []string
			if tt.sha != "" {
				headers = []string{"X-Spool-Seen-Sha256", tt.sha}
			}
			rec := do(t, e.h, "POST", "/api/requests/"+tt.id+"/seen", bytes.NewReader(body), headers...)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tt.want, rec.Body)
			}
			if got := e.store.seenBody("01REQ"); !bytes.Equal(got, tt.wantSeen) {
				t.Errorf("seen body = %q, want %q", got, tt.wantSeen)
			}
		})
	}
}

func TestSeenTooLarge(t *testing.T) {
	e := newEnv(t, envOpts{}, stripeRequest("01REQ"))
	rec := do(t, e.h, "POST", "/api/requests/01REQ/seen", bytes.NewReader(make([]byte, 10<<20+1)))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", rec.Code)
	}
}

type replayResponse struct {
	Attempts []struct {
		Kind       string `json:"kind"`
		TargetURL  string `json:"target_url"`
		StatusCode int    `json:"status_code"`
		Resigned   bool   `json:"resigned"`
		Response   string `json:"response_body"`
	} `json:"attempts"`
	Warnings []string `json:"warnings"`
}

func TestReplay(t *testing.T) {
	tests := []struct {
		name       string
		target     string
		body       string
		wantCode   int
		wantSent   int
		wantURL    string
		wantResign bool
	}{
		{name: "default target", target: "http://app.local", body: "", wantCode: 200, wantSent: 1, wantURL: "http://app.local/webhooks/stripe"},
		{name: "explicit target and times", body: `{"to":"http://other","times":2}`, wantCode: 200, wantSent: 2, wantURL: "http://other/webhooks/stripe"},
		{name: "resigned", target: "http://app.local", body: `{"resign":true}`, wantCode: 200, wantSent: 1, wantURL: "http://app.local/webhooks/stripe", wantResign: true},
		{name: "no target", body: `{}`, wantCode: http.StatusUnprocessableEntity},
		{name: "bad json", target: "http://app.local", body: `{`, wantCode: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, envOpts{target: tt.target}, stripeRequest("01REQ"))
			rec := do(t, e.h, "POST", "/api/requests/01REQ/replay", strings.NewReader(tt.body))
			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tt.wantCode, rec.Body)
			}
			sent := e.sender.outgoing()
			if len(sent) != tt.wantSent {
				t.Fatalf("sent %d requests, want %d", len(sent), tt.wantSent)
			}
			if tt.wantSent == 0 {
				return
			}
			got := decode[replayResponse](t, rec)
			if len(got.Attempts) != tt.wantSent {
				t.Fatalf("attempts = %+v", got.Attempts)
			}
			a := got.Attempts[0]
			if a.Kind != "replay" || a.StatusCode != 200 || a.TargetURL != tt.wantURL || a.Resigned != tt.wantResign {
				t.Errorf("attempt = %+v", a)
			}
			if a.Response != `{"received":true}` {
				t.Errorf("response body = %q", a.Response)
			}
			if sent[0].URL != tt.wantURL || sent[0].Headers.Get(usecase.HeaderReplay) != "01REQ" {
				t.Errorf("outgoing = %s %v", sent[0].URL, sent[0].Headers)
			}
			if !bytes.Equal(sent[0].Body, stripeBody) {
				t.Errorf("outgoing body = %q", sent[0].Body)
			}
		})
	}
}

func TestReplayUnknown(t *testing.T) {
	e := newEnv(t, envOpts{target: "http://app.local"})
	if rec := do(t, e.h, "POST", "/api/requests/nope/replay", nil); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

type verifyResponse struct {
	VerificationID string   `json:"verification_id"`
	Provider       string   `json:"provider"`
	OK             bool     `json:"ok"`
	Reason         string   `json:"reason"`
	Meaning        string   `json:"meaning"`
	Hints          []string `json:"hints"`
	Expected       string   `json:"expected"`
	Got            string   `json:"got"`
	Diff           *struct {
		Offset int    `json:"offset"`
		Raw    string `json:"raw"`
		App    string `json:"app"`
	} `json:"diff"`
	Fixes []struct {
		Framework string `json:"framework"`
		Snippet   string `json:"snippet"`
	} `json:"fixes"`
}

func TestVerify(t *testing.T) {
	tests := []struct {
		name      string
		secrets   mapSecrets
		seen      []byte
		body      string
		wantCode  int
		wantOK    bool
		reason    string
		wantDiff  bool
		wantFixes bool
	}{
		{name: "valid", wantCode: 200, wantOK: true, reason: "OK_RAW"},
		{name: "wrong secret", secrets: mapSecrets{"stripe": "whsec_nope"}, wantCode: 200, reason: "SECRET_MISMATCH"},
		{name: "explicit provider", body: `{"provider":"stripe"}`, wantCode: 200, wantOK: true, reason: "OK_RAW"},
		{name: "seen body changed", seen: stripeBody[:10], wantCode: 200, wantOK: true, reason: "BODY_CHANGED", wantDiff: true, wantFixes: true},
		{name: "unknown provider", body: `{"provider":"acme"}`, wantCode: http.StatusUnprocessableEntity},
		{name: "no secret", secrets: mapSecrets{}, wantCode: http.StatusUnprocessableEntity},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := stripeRequest("01REQ")
			req.SeenBody = tt.seen
			e := newEnv(t, envOpts{secrets: tt.secrets}, req)
			rec := do(t, e.h, "POST", "/api/requests/01REQ/verify", strings.NewReader(tt.body))
			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tt.wantCode, rec.Body)
			}
			if tt.wantCode != 200 {
				if got := decode[map[string]string](t, rec); got["error"] == "" {
					t.Error("missing error message")
				}
				return
			}
			got := decode[verifyResponse](t, rec)
			if got.OK != tt.wantOK || got.Reason != tt.reason || got.Provider != "stripe" {
				t.Errorf("result = %+v", got)
			}
			if got.Meaning == "" || len(got.Hints) == 0 || got.Expected == "" || got.VerificationID == "" {
				t.Errorf("incomplete result = %+v", got)
			}
			if (got.Diff != nil) != tt.wantDiff || (len(got.Fixes) > 0) != tt.wantFixes {
				t.Errorf("diff = %+v, fixes = %d", got.Diff, len(got.Fixes))
			}
			if tt.wantDiff && (got.Diff.Offset != 10 || got.Diff.Raw == "" || got.Diff.App == "") {
				t.Errorf("diff = %+v", got.Diff)
			}
		})
	}
}

func TestCrossOriginPostRejected(t *testing.T) {
	e := newEnv(t, envOpts{target: "http://app.local"}, stripeRequest("01REQ"))
	rec := do(t, e.h, "POST", "/api/requests/01REQ/replay", nil, "Sec-Fetch-Site", "cross-site")
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	if n := len(e.sender.outgoing()); n != 0 {
		t.Errorf("sent %d requests, want 0", n)
	}
	seen := do(t, e.h, "POST", "/api/requests/01REQ/seen", strings.NewReader("{}"), "Origin", "null")
	if seen.Code != http.StatusNoContent {
		t.Errorf("seen status = %d, want 204", seen.Code)
	}
}
