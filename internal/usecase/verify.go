package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/grintzdel/spool/internal/domain/diagnose"
	"github.com/grintzdel/spool/internal/domain/signature"
	"github.com/grintzdel/spool/internal/domain/webhook"
)

type VerifyInput struct {
	ID        string
	Provider  string
	SecretEnv string
	AppBody   []byte
	Tolerance time.Duration
}

type Report struct {
	Request      webhook.Request
	Provider     string
	Result       signature.Result
	Diff         *diagnose.Diff
	Fixes        []diagnose.Fix
	Verification webhook.Verification
}

func (r Report) Reason() signature.Reason { return r.Result.Reason }

type Verifier struct {
	Store     Store
	Registry  *signature.Registry
	Secrets   Secrets
	Clock     Clock
	IDs       IDs
	Publisher Publisher
	Tolerance time.Duration
}

func (v *Verifier) Verify(ctx context.Context, in VerifyInput) (Report, error) {
	req, err := v.Store.FindRequest(ctx, in.ID)
	if err != nil {
		return Report{}, err
	}
	p, err := resolveProvider(v.Registry, req, in.Provider)
	if err != nil {
		return Report{}, err
	}
	secret, err := v.Secrets.Secret(p.Name(), in.SecretEnv)
	if err != nil {
		return Report{}, fmt.Errorf("%s: %w", p.Name(), err)
	}
	tolerance := in.Tolerance
	if tolerance == 0 {
		tolerance = v.Tolerance
	}

	rep := Report{Request: req, Provider: p.Name(), Result: p.Verify(req, secret, signature.Options{Tolerance: tolerance})}
	appBody := in.AppBody
	if appBody == nil {
		appBody = req.SeenBody
	}
	if rep.Result.OK && appBody != nil {
		if d := diagnose.Compare(req.Body, appBody); !d.Identical() {
			rep.Result.Reason = d.Reason
			rep.Result.Hints = d.Reason.Hints()
			rep.Diff = &d
			rep.Fixes = diagnose.Fixes()
		}
	}

	rep.Verification = webhook.Verification{
		ID:        v.IDs.NewID(),
		RequestID: req.ID,
		Provider:  p.Name(),
		OK:        rep.Result.OK,
		Reason:    string(rep.Result.Reason),
		Details:   details(rep),
		CheckedAt: v.Clock.Now(),
	}
	if err := v.Store.SaveVerification(ctx, rep.Verification); err != nil {
		return rep, fmt.Errorf("save verification: %w", err)
	}
	v.Publisher.Publish(Event{Kind: EventVerification, RequestID: req.ID})
	return rep, nil
}

type verificationDetails struct {
	Expected string   `json:"expected,omitempty"`
	Got      string   `json:"got,omitempty"`
	Hints    []string `json:"hints,omitempty"`
	Offset   *int     `json:"offset,omitempty"`
	Raw      string   `json:"raw,omitempty"`
	App      string   `json:"app,omitempty"`
}

func details(r Report) string {
	d := verificationDetails{Expected: r.Result.Expected, Got: r.Result.Got, Hints: r.Result.Hints}
	if r.Diff != nil {
		d.Offset, d.Raw, d.App = &r.Diff.Offset, r.Diff.RawContext, r.Diff.SeenContext
	}
	b, _ := json.Marshal(d)
	return string(b)
}

type SeenRecorder struct {
	Store     Store
	Verifier  *Verifier
	Publisher Publisher
}

func (s SeenRecorder) RecordSeen(ctx context.Context, id string, body []byte, announcedSHA string) error {
	if announcedSHA != "" && !strings.EqualFold(announcedSHA, webhook.SHA256Hex(body)) {
		return ErrSeenMismatch
	}
	req, err := s.Store.FindRequest(ctx, id)
	if err != nil {
		return err
	}
	if body == nil {
		body = []byte{}
	}
	if err := s.Store.SetSeenBody(ctx, req.ID, body); err != nil {
		return err
	}
	s.Publisher.Publish(Event{Kind: EventRequest, RequestID: req.ID})
	if s.Verifier != nil {
		_, _ = s.Verifier.Verify(ctx, VerifyInput{ID: req.ID})
	}
	return nil
}
