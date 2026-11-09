package usecase

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/grintzdel/spool/internal/domain/signature"
	"github.com/grintzdel/spool/internal/domain/webhook"
)

const (
	MaxStoredResponse = 64 << 10
	HeaderID          = "X-Spool-Id"
	HeaderReplay      = "X-Spool-Replay"
)

var hopByHop = []string{"Connection", "Keep-Alive", "Transfer-Encoding", "TE", "Trailer", "Upgrade", "Host", "Content-Length"}

func outgoingHeaders(h webhook.Headers) webhook.Headers {
	drop := slices.Clone(hopByHop)
	for _, v := range h.Values("Connection") {
		for name := range strings.SplitSeq(v, ",") {
			if name = strings.TrimSpace(name); name != "" {
				drop = append(drop, name)
			}
		}
	}
	out := h.Without(drop...)
	return slices.DeleteFunc(out, func(kv webhook.Header) bool {
		return strings.HasPrefix(strings.ToLower(kv.Name), "proxy-")
	})
}

type DeliverOptions struct {
	Kind       webhook.AttemptKind
	Target     string
	Resign     bool
	Marker     bool
	Provider   string
	SetHeaders map[string]string
}

type Forwarder struct {
	Store     Store
	Sender    Sender
	Publisher Publisher
	Clock     Clock
	IDs       IDs
	Registry  *signature.Registry
	Secrets   Secrets
}

func (f *Forwarder) Deliver(ctx context.Context, req webhook.Request, opts DeliverOptions) (webhook.Attempt, error) {
	if opts.Kind == "" {
		opts.Kind = webhook.AttemptForward
	}
	headers := outgoingHeaders(req.Headers).Set(HeaderID, req.ID)
	if opts.Kind == webhook.AttemptReplay && opts.Marker {
		headers = headers.Set(HeaderReplay, req.ID)
	}
	if opts.Resign {
		signed, err := f.resign(req, opts.Provider)
		if err != nil {
			return webhook.Attempt{}, err
		}
		for _, h := range signed {
			headers = headers.Set(h.Name, h.Value)
		}
	}
	for _, k := range slices.Sorted(maps.Keys(opts.SetHeaders)) {
		headers = headers.Set(k, opts.SetHeaders[k])
	}

	attempt := webhook.Attempt{
		ID:        f.IDs.NewID(),
		RequestID: req.ID,
		Kind:      opts.Kind,
		TargetURL: req.URLOn(opts.Target),
		Resigned:  opts.Resign,
		StartedAt: f.Clock.Now(),
	}
	start := time.Now()
	resp, err := f.Sender.Send(ctx, Outgoing{Method: req.Method, URL: attempt.TargetURL, Headers: headers, Body: req.Body})
	attempt.Duration = time.Since(start)
	if err != nil {
		attempt.Error = err.Error()
	} else {
		attempt.StatusCode = resp.StatusCode
		attempt.ResponseBody = resp.Body[:min(len(resp.Body), MaxStoredResponse)]
	}

	if err := f.Store.SaveAttempt(context.WithoutCancel(ctx), attempt); err != nil {
		return attempt, fmt.Errorf("save attempt: %w", err)
	}
	f.Publisher.Publish(Event{Kind: EventAttempt, RequestID: req.ID})
	return attempt, nil
}

func (f *Forwarder) resign(req webhook.Request, providerName string) (webhook.Headers, error) {
	p, err := resolveProvider(f.Registry, req, providerName)
	if err != nil {
		return nil, err
	}
	secret, err := f.Secrets.Secret(p.Name(), "")
	if err != nil {
		return nil, fmt.Errorf("resign with %s: %w", p.Name(), err)
	}
	return p.Sign(req.Body, secret, f.Clock.Now())
}

type Dispatcher struct {
	forwarder *Forwarder
	target    string
	queue     chan webhook.Request
	wg        sync.WaitGroup
	mu        sync.RWMutex
	closed    bool
}

func NewDispatcher(f *Forwarder, target string, workers int) *Dispatcher {
	d := &Dispatcher{forwarder: f, target: target, queue: make(chan webhook.Request, 1024)}
	for range max(workers, 1) {
		d.wg.Go(func() {
			for req := range d.queue {
				_, _ = f.Deliver(context.Background(), req, DeliverOptions{Kind: webhook.AttemptForward, Target: target})
			}
		})
	}
	return d
}

func (d *Dispatcher) Target() string { return d.target }

func (d *Dispatcher) Enqueue(req webhook.Request) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.closed {
		return
	}
	select {
	case d.queue <- req:
	default:
		d.wg.Go(func() {
			_, _ = d.forwarder.Deliver(context.Background(), req, DeliverOptions{Kind: webhook.AttemptForward, Target: d.target})
		})
	}
}

func (d *Dispatcher) Close(ctx context.Context) error {
	d.mu.Lock()
	if !d.closed {
		d.closed = true
		close(d.queue)
	}
	d.mu.Unlock()

	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("forward queue not drained: %w", ctx.Err())
	}
}
