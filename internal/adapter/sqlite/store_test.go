package sqlite_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/grintzdel/spool/internal/adapter/sqlite"
	"github.com/grintzdel/spool/internal/domain/webhook"
	"github.com/grintzdel/spool/internal/usecase"
)

var base = time.UnixMilli(1_762_000_000_000)

func open(t *testing.T) (*sqlite.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "nested", "spool.db")
	s, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func request(id string, at time.Time, provider string) webhook.Request {
	r := webhook.NewRequest(id, at, "POST", "/hook", "a=1", webhook.Headers{
		{Name: "B", Value: "2"}, {Name: "A", Value: "1"}, {Name: "b", Value: "3"},
	}, []byte("{\"caf\u00e9\": \"\\u00e9\"}\r\n\x00\xff"))
	r.Provider, r.EventType = provider, provider+".event"
	return r
}

func TestRequestRoundTripIsByteExact(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	want := request("01AAA", base, "stripe")
	if err := s.SaveRequest(ctx, want); err != nil {
		t.Fatal(err)
	}

	got, err := s.FindRequest(ctx, "01AAA")
	if err != nil {
		t.Fatal(err)
	}
	if d := cmp.Diff(want, got); d != "" {
		t.Fatal(d)
	}
	if webhook.SHA256Hex(got.Body) != want.BodySHA256 {
		t.Fatal("sha changed")
	}
}

func TestFindRequestByPrefix(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	for _, id := range []string{"01ABC", "01ABD", "01XYZ"} {
		if err := s.SaveRequest(ctx, request(id, base, "")); err != nil {
			t.Fatal(err)
		}
	}
	if r, err := s.FindRequest(ctx, "01x"); err != nil || r.ID != "01XYZ" {
		t.Fatalf("prefix: %v %v", r.ID, err)
	}
	if _, err := s.FindRequest(ctx, "01AB"); !errors.Is(err, webhook.ErrAmbiguous) {
		t.Fatalf("ambiguous: %v", err)
	}
	if _, err := s.FindRequest(ctx, "zz"); !errors.Is(err, webhook.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if _, err := s.FindRequest(ctx, "01%"); !errors.Is(err, webhook.ErrNotFound) {
		t.Fatalf("wildcards must not match: %v", err)
	}
}

func TestListRequestsJoinsLatestAttemptAndVerificationAndFilters(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	must(t, s.SaveRequest(ctx, request("01A", base, "stripe")))
	must(t, s.SaveRequest(ctx, request("01B", base.Add(time.Second), "github")))
	must(t, s.SaveRequest(ctx, request("01C", base.Add(2*time.Second), "stripe")))
	must(t, s.SaveAttempt(ctx, webhook.Attempt{ID: "a1", RequestID: "01A", Kind: webhook.AttemptForward, TargetURL: "http://t", StartedAt: base, StatusCode: 200}))
	must(t, s.SaveAttempt(ctx, webhook.Attempt{ID: "a2", RequestID: "01A", Kind: webhook.AttemptReplay, TargetURL: "http://t", StartedAt: base.Add(time.Minute), StatusCode: 400, Duration: 15 * time.Millisecond, ResponseBody: []byte("bad")}))
	must(t, s.SaveVerification(ctx, webhook.Verification{ID: "v1", RequestID: "01B", Provider: "github", OK: true, Reason: "OK_RAW", CheckedAt: base}))

	all, err := s.ListRequests(ctx, usecase.ListFilter{Limit: 10})
	must(t, err)
	if ids(all) != "01C,01B,01A" {
		t.Fatalf("order = %s", ids(all))
	}
	if a := all[2].LastAttempt; a == nil || a.ID != "a2" || a.StatusCode != 400 || a.Duration != 15*time.Millisecond {
		t.Fatalf("last attempt = %+v", a)
	}
	if v := all[1].Verification; v == nil || v.Reason != "OK_RAW" || !v.OK {
		t.Fatalf("verification = %+v", v)
	}
	if all[0].Request.Body != nil || all[0].Request.BodySHA256 == "" {
		t.Fatal("list should carry the sha but not the body")
	}

	cases := map[string]usecase.ListFilter{
		"01C,01A": {Provider: "stripe", Limit: 10},
		"01B":     {EventType: "github.event", Limit: 10},
		"01A":     {FailedOnly: true, Limit: 10},
		"01B,01A": {BeforeID: "01C", Limit: 10},
		"01C":     {Limit: 1},
	}
	for want, f := range cases {
		got, err := s.ListRequests(ctx, f)
		must(t, err)
		if ids(got) != want {
			t.Errorf("%+v: got %s, want %s", f, ids(got), want)
		}
	}
}

func TestAttemptsVerificationsAndSeenBody(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	must(t, s.SaveRequest(ctx, request("01A", base, "stripe")))
	want := webhook.Attempt{ID: "a1", RequestID: "01A", Kind: webhook.AttemptReplay, TargetURL: "http://t/x", Resigned: true, StartedAt: base, Duration: 3 * time.Millisecond, Error: "refused"}
	must(t, s.SaveAttempt(ctx, want))
	got, err := s.ListAttempts(ctx, "01A")
	must(t, err)
	if d := cmp.Diff([]webhook.Attempt{want}, got); d != "" {
		t.Fatal(d)
	}

	if v, err := s.LatestVerification(ctx, "01A"); err != nil || v != nil {
		t.Fatalf("expected no verification, got %v %v", v, err)
	}
	must(t, s.SaveVerification(ctx, webhook.Verification{ID: "v1", RequestID: "01A", Provider: "stripe", Reason: "SECRET_MISMATCH", CheckedAt: base}))
	must(t, s.SaveVerification(ctx, webhook.Verification{ID: "v2", RequestID: "01A", Provider: "stripe", OK: true, Reason: "OK_RAW", Details: `{"x":1}`, CheckedAt: base.Add(time.Second)}))
	v, err := s.LatestVerification(ctx, "01A")
	must(t, err)
	if v.ID != "v2" || v.Details != `{"x":1}` {
		t.Fatalf("latest = %+v", v)
	}

	must(t, s.SetSeenBody(ctx, "01A", []byte("seen")))
	r, _ := s.FindRequest(ctx, "01A")
	if string(r.SeenBody) != "seen" {
		t.Fatal("seen body not stored")
	}
	if err := s.SetSeenBody(ctx, "nope", nil); !errors.Is(err, webhook.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestPruneByAgeAndCountCascades(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	for i, id := range []string{"01A", "01B", "01C", "01D"} {
		must(t, s.SaveRequest(ctx, request(id, base.Add(time.Duration(i)*time.Hour), "")))
	}
	must(t, s.SaveAttempt(ctx, webhook.Attempt{ID: "a", RequestID: "01A", Kind: webhook.AttemptForward, TargetURL: "x", StartedAt: base}))

	n, err := s.Prune(ctx, base.Add(30*time.Minute), 0)
	must(t, err)
	if n != 1 {
		t.Fatalf("pruned %d", n)
	}
	if a, _ := s.ListAttempts(ctx, "01A"); len(a) != 0 {
		t.Fatal("attempts not cascaded")
	}
	n, err = s.Prune(ctx, time.Time{}, 2)
	must(t, err)
	left, _ := s.ListRequests(ctx, usecase.ListFilter{Limit: 10})
	if n != 1 || ids(left) != "01D,01C" {
		t.Fatalf("n=%d left=%s", n, ids(left))
	}
}

func TestDatabaseFileIsPrivateAndReopenable(t *testing.T) {
	s, path := open(t)
	must(t, s.SaveRequest(context.Background(), request("01A", base, "")))
	s.Close()

	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		must(t, err)
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %v", info.Mode().Perm())
		}
	}
	again, err := sqlite.Open(path)
	must(t, err)
	defer again.Close()
	if _, err := again.FindRequest(context.Background(), "01A"); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentWritersFromTwoConnections(t *testing.T) {
	s, path := open(t)
	other, err := sqlite.Open(path)
	must(t, err)
	defer other.Close()

	var wg sync.WaitGroup
	for i := range 50 {
		store := s
		if i%2 == 1 {
			store = other
		}
		wg.Go(func() {
			id := "01" + string(rune('A'+i/26)) + string(rune('A'+i%26))
			if err := store.SaveRequest(context.Background(), request(id, base, "")); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	all, _ := s.ListRequests(context.Background(), usecase.ListFilter{Limit: 100})
	if len(all) != 50 {
		t.Fatalf("rows = %d", len(all))
	}
}

func ids(list []usecase.RequestSummary) string {
	out := ""
	for i, r := range list {
		if i > 0 {
			out += ","
		}
		out += r.Request.ID
	}
	return out
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
