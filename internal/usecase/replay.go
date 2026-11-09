package usecase

import (
	"context"
	"fmt"
	"time"

	"github.com/grintzdel/spool/internal/domain/signature"
	"github.com/grintzdel/spool/internal/domain/webhook"
)

type ReplayInput struct {
	ID         string
	To         string
	Times      int
	Resign     bool
	Marker     bool
	Provider   string
	SetHeaders map[string]string
}

type ReplayResult struct {
	Request  webhook.Request
	Attempts []webhook.Attempt
	Warnings []string
}

type Replayer struct {
	Store         Store
	Forwarder     *Forwarder
	Registry      *signature.Registry
	Clock         Clock
	DefaultTarget string
	Tolerance     time.Duration
}

func (r Replayer) Replay(ctx context.Context, in ReplayInput) (ReplayResult, error) {
	req, err := r.Store.FindRequest(ctx, in.ID)
	if err != nil {
		return ReplayResult{}, err
	}
	target := in.To
	if target == "" {
		target = r.DefaultTarget
	}
	if target == "" {
		return ReplayResult{}, ErrNoTarget
	}

	res := ReplayResult{Request: req}
	if !in.Resign {
		if w := r.staleWarning(req, in.Provider); w != "" {
			res.Warnings = append(res.Warnings, w)
		}
	}
	for range max(in.Times, 1) {
		a, err := r.Forwarder.Deliver(ctx, req, DeliverOptions{
			Kind: webhook.AttemptReplay, Target: target, Resign: in.Resign, Marker: in.Marker,
			Provider: in.Provider, SetHeaders: in.SetHeaders,
		})
		if err != nil {
			return res, err
		}
		res.Attempts = append(res.Attempts, a)
	}
	return res, nil
}

func (r Replayer) staleWarning(req webhook.Request, providerName string) string {
	p, err := resolveProvider(r.Registry, req, providerName)
	if err != nil {
		return ""
	}
	ts, ok := p.(signature.Timestamped)
	if !ok {
		return ""
	}
	signedAt, ok := ts.SignedAt(req.Headers)
	if !ok {
		return ""
	}
	age := r.Clock.Now().Sub(signedAt)
	if r.Tolerance <= 0 || age <= r.Tolerance {
		return ""
	}
	return fmt.Sprintf("%s signature is %s old, beyond the %s tolerance: the app will likely reject it, use --resign",
		p.Name(), age.Round(time.Second), r.Tolerance)
}
