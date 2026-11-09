package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/grintzdel/spool/internal/adapter/cli"
	"github.com/grintzdel/spool/internal/adapter/sqlite"
	"github.com/grintzdel/spool/internal/domain/signature"
	"github.com/grintzdel/spool/internal/domain/signature/stripe"
	"github.com/grintzdel/spool/internal/domain/webhook"
)

const secret = "whsec_cli_test"

var body = []byte("{\n  \"id\": \"evt_1\",\n  \"type\": \"payment_intent.succeeded\",\n  \"description\": \"Caf\\u00e9 crème\"\n}\n")

type env struct {
	db  string
	dir string
}

func setup(t *testing.T, receivedAt time.Time) env {
	t.Helper()
	dir := t.TempDir()
	e := env{db: filepath.Join(dir, "spool.db"), dir: dir}
	t.Setenv("SPOOL_DB", "")
	t.Setenv("SPOOL_STRIPE_SECRET", secret)
	store, err := sqlite.Open(e.db)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	h, _ := stripe.New().Sign(body, []byte(secret), receivedAt)
	h = append(webhook.Headers{{Name: "Host", Value: "127.0.0.1:7777"}, {Name: "Content-Type", Value: "application/json"}}, h...)
	r := webhook.NewRequest("01JCLITEST00000000000000AA", receivedAt, "POST", "/webhooks/stripe", "", h, body)
	r.Provider, r.EventType = "stripe", "payment_intent.succeeded"
	if err := store.SaveRequest(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e env) run(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := cli.NewRootCommand("test")
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append([]string{"--db", e.db, "--config", filepath.Join(e.dir, "none.toml")}, args...))
	err := cmd.ExecuteContext(context.Background())
	return out.String(), errOut.String(), err
}

func TestListAndShow(t *testing.T) {
	e := setup(t, time.Now())

	out, _, err := e.run(t, "ls")
	if err != nil || !strings.Contains(out, "01JCLITEST00000000000000AA") || !strings.Contains(out, "payment_intent.succeeded") {
		t.Fatalf("ls: %v\n%s", err, out)
	}
	out, _, _ = e.run(t, "ls", "--json", "--provider", "github")
	if strings.TrimSpace(out) != "[]" {
		t.Fatalf("filtered ls: %s", out)
	}

	out, _, err = e.run(t, "show", "01JCLITEST", "--raw")
	if err != nil || out != string(body) {
		t.Fatalf("show --raw is not byte exact: %v %q", err, out)
	}
	out, _, _ = e.run(t, "show", "01jclitest")
	if !strings.Contains(out, "Stripe-Signature") || !strings.Contains(out, webhook.SHA256Hex(body)) {
		t.Fatalf("show: %s", out)
	}
	if _, _, err := e.run(t, "show", "NOPE"); !errors.Is(err, webhook.ErrNotFound) {
		t.Fatalf("show unknown: %v", err)
	}
}

func TestVerifyVerdicts(t *testing.T) {
	e := setup(t, time.Now())

	out, _, err := e.run(t, "verify", "01JCLITEST")
	if err != nil || !strings.Contains(out, "OK_RAW") {
		t.Fatalf("verify ok: %v\n%s", err, out)
	}

	var compact bytes.Buffer
	json.Compact(&compact, body)
	appBody := filepath.Join(e.dir, "app.json")
	os.WriteFile(appBody, compact.Bytes(), 0o600)
	out, _, err = e.run(t, "verify", "01JCLITEST", "--body-file", appBody)
	if err == nil || !strings.Contains(out, string(signature.BodyReformatted)) || !strings.Contains(out, "express.raw") {
		t.Fatalf("verify reformatted: %v\n%s", err, out)
	}

	t.Setenv("OTHER_SECRET", "whsec_wrong")
	out, _, err = e.run(t, "verify", "01JCLITEST", "--secret-env", "OTHER_SECRET", "--json")
	var rep map[string]any
	if err == nil || json.Unmarshal([]byte(out), &rep) != nil || rep["reason"] != string(signature.SecretMismatch) {
		t.Fatalf("verify mismatch: %v\n%s", err, out)
	}

	shown, _, _ := e.run(t, "ls")
	if !strings.Contains(shown, string(signature.SecretMismatch)) {
		t.Fatalf("latest verification not listed:\n%s", shown)
	}
}

func TestReplayResignsAgainstTheApp(t *testing.T) {
	e := setup(t, time.Now().Add(-time.Hour))
	var mu sync.Mutex
	var received []*http.Request
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		req := webhook.NewRequest("x", time.Now(), r.Method, r.URL.Path, "", webhook.FromHTTP(r.Header), b)
		res := stripe.New().Verify(req, []byte(secret), signature.Options{Tolerance: 5 * time.Minute})
		mu.Lock()
		received = append(received, r)
		mu.Unlock()
		if !res.OK {
			http.Error(w, string(res.Reason), http.StatusBadRequest)
		}
	}))
	defer app.Close()

	_, stderr, err := e.run(t, "replay", "01JCLITEST", "--to", app.URL)
	if err == nil || !strings.Contains(stderr, "--resign") {
		t.Fatalf("stale replay should fail with a warning: %v %q", err, stderr)
	}

	out, _, err := e.run(t, "replay", "01JCLITEST", "--to", app.URL, "--resign", "--times", "2", "--set-header", "X-Test=1")
	if err != nil {
		t.Fatalf("resigned replay: %v\n%s", err, out)
	}
	if len(received) != 3 || received[2].Header.Get("X-Spool-Replay") == "" || received[2].Header.Get("X-Test") != "1" {
		t.Fatalf("received %d", len(received))
	}

	shown, _, _ := e.run(t, "show", "01JCLITEST")
	if strings.Count(shown, "replay") != 3 || !strings.Contains(shown, "resigned") {
		t.Fatalf("attempts not recorded:\n%s", shown)
	}
}

func TestExportAndPrune(t *testing.T) {
	e := setup(t, time.Now().Add(-48*time.Hour))

	out, _, err := e.run(t, "export", "01JCLITEST", "--to", "http://localhost:3000")
	if err != nil || !strings.HasPrefix(out, "curl -X POST 'http://localhost:3000/webhooks/stripe'") {
		t.Fatalf("export: %v\n%s", err, out)
	}
	out, _, _ = e.run(t, "export", "01JCLITEST", "--format", "http")
	if !strings.HasPrefix(out, "POST /webhooks/stripe HTTP/1.1\r\n") {
		t.Fatalf("export http: %q", out)
	}

	out, _, err = e.run(t, "prune", "--older-than", "1d")
	if err != nil || !strings.Contains(out, "Deleted 1") {
		t.Fatalf("prune: %v %s", err, out)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestServeCapturesForwardsAndExposesTheAPI(t *testing.T) {
	e := setup(t, time.Now())
	appGot := make(chan []byte, 1)
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if r.Header.Get("X-Spool-Id") == "" {
			t.Error("forward without X-Spool-Id")
		}
		appGot <- b
		w.WriteHeader(http.StatusAccepted)
	}))
	defer app.Close()

	capturePort, uiPort := freePort(t), freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		cmd := cli.NewRootCommand("test")
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		cmd.SetArgs([]string{"--db", e.db, "--config", filepath.Join(e.dir, "none.toml"), "serve",
			"--port", fmt.Sprint(capturePort), "--ui-port", fmt.Sprint(uiPort), "--forward", app.URL})
		done <- cmd.ExecuteContext(ctx)
	}()

	captureURL := fmt.Sprintf("http://127.0.0.1:%d/webhooks/github?x=1", capturePort)
	payload := []byte("{\"zen\":\"é\\u00e9\"}\r\n")
	var resp *http.Response
	var err error
	for range 100 {
		resp, err = http.Post(captureURL, "application/json", bytes.NewReader(payload))
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	id := resp.Header.Get("X-Spool-Id")
	if resp.StatusCode != 200 || id == "" {
		t.Fatalf("capture status %d id %q", resp.StatusCode, id)
	}

	select {
	case got := <-appGot:
		if !bytes.Equal(got, payload) {
			t.Fatalf("forwarded body altered: %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("app never received the forward")
	}

	var detail map[string]any
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/requests/%s", uiPort, id))
		if err == nil {
			json.NewDecoder(r.Body).Decode(&detail)
			r.Body.Close()
			if attempts, _ := detail["attempts"].([]any); len(attempts) == 1 {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if attempts, _ := detail["attempts"].([]any); len(attempts) != 1 {
		t.Fatalf("attempt not visible through the UI API: %v", detail)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("serve did not shut down")
	}

	out, _, _ := e.run(t, "show", id, "--raw")
	if out != string(payload) {
		t.Fatalf("stored body differs: %q", out)
	}
}
