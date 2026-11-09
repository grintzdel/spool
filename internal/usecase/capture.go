package usecase

import (
	"context"
	"fmt"

	"github.com/grintzdel/spool/internal/domain/signature"
	"github.com/grintzdel/spool/internal/domain/webhook"
)

type Incoming struct {
	Method  string
	Path    string
	Query   string
	Headers webhook.Headers
	Body    []byte
}

type Capturer struct {
	Store     Store
	Registry  *signature.Registry
	Publisher Publisher
	Clock     Clock
	IDs       IDs
}

func (c Capturer) Capture(ctx context.Context, in Incoming) (webhook.Request, error) {
	req := webhook.NewRequest(c.IDs.NewID(), c.Clock.Now(), in.Method, in.Path, in.Query, in.Headers, in.Body)
	if p, ok := c.Registry.Detect(in.Headers); ok {
		req.Provider = p.Name()
		req.EventType = p.EventType(in.Headers, in.Body)
	}
	if err := c.Store.SaveRequest(ctx, req); err != nil {
		return webhook.Request{}, fmt.Errorf("save request: %w", err)
	}
	c.Publisher.Publish(Event{Kind: EventRequest, RequestID: req.ID})
	return req, nil
}

func resolveProvider(reg *signature.Registry, req webhook.Request, name string) (signature.Provider, error) {
	if name == "" {
		name = req.Provider
	}
	if name != "" {
		p, ok := reg.Lookup(name)
		if !ok {
			return nil, fmt.Errorf("%w %q (known: %v)", ErrUnknownProvider, name, reg.Names())
		}
		return p, nil
	}
	if p, ok := reg.Detect(req.Headers); ok {
		return p, nil
	}
	return nil, ErrNoProvider
}
