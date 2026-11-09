package sender_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/grintzdel/spool/internal/adapter/sender"
	"github.com/grintzdel/spool/internal/domain/webhook"
	"github.com/grintzdel/spool/internal/usecase"
)

func TestSendPreservesBodyHeadersAndAddsNothing(t *testing.T) {
	var got *http.Request
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, gotBody = r, mustReadAll(r.Body)
		w.WriteHeader(418)
		w.Write([]byte("teapot"))
	}))
	defer srv.Close()
	body := []byte("{\n \"a\": \"\\u00e9\"}\r\n")

	resp, err := sender.New(time.Second, 2).Send(context.Background(), usecase.Outgoing{
		Method: "POST", URL: srv.URL + "/hook?x=1", Body: body,
		Headers: webhook.Headers{{Name: "X-Multi", Value: "1"}, {Name: "X-Multi", Value: "2"}, {Name: "Stripe-Signature", Value: "t=1,v1=a"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if resp.StatusCode != 418 || string(resp.Body) != "teapot" {
		t.Fatalf("resp %+v", resp)
	}
	if string(gotBody) != string(body) || got.URL.RequestURI() != "/hook?x=1" {
		t.Fatalf("body/uri altered: %q %s", gotBody, got.URL)
	}
	if strings.Join(got.Header.Values("X-Multi"), ",") != "1,2" || got.Header.Get("Stripe-Signature") != "t=1,v1=a" {
		t.Fatalf("headers %v", got.Header)
	}
	if got.Header.Get("User-Agent") != "" || got.Header.Get("Accept-Encoding") != "" {
		t.Fatalf("client added headers: %v", got.Header)
	}
}

func TestSendDoesNotFollowRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	defer srv.Close()
	resp, err := sender.New(time.Second, 1).Send(context.Background(), usecase.Outgoing{Method: "POST", URL: srv.URL})
	if err != nil || resp.StatusCode != http.StatusFound {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
}

func TestSendTimesOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	_, err := sender.New(50*time.Millisecond, 1).Send(context.Background(), usecase.Outgoing{Method: "POST", URL: srv.URL})
	if err == nil {
		t.Fatal("expected timeout")
	}
}

func TestSendLimitsConcurrency(t *testing.T) {
	var inFlight, peak atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := inFlight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		inFlight.Add(-1)
	}))
	defer srv.Close()
	s := sender.New(time.Second, 2)
	done := make(chan struct{})
	for range 8 {
		go func() {
			s.Send(context.Background(), usecase.Outgoing{Method: "POST", URL: srv.URL})
			done <- struct{}{}
		}()
	}
	for range 8 {
		<-done
	}
	if peak.Load() > 2 {
		t.Fatalf("peak concurrency = %d", peak.Load())
	}
}

func mustReadAll(r io.Reader) []byte {
	b, _ := io.ReadAll(r)
	return b
}
