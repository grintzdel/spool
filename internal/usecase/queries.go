package usecase

import (
	"context"
	"time"

	"github.com/grintzdel/spool/internal/domain/diagnose"
	"github.com/grintzdel/spool/internal/domain/webhook"
)

type Detail struct {
	Request      webhook.Request
	Attempts     []webhook.Attempt
	Verification *webhook.Verification
	SeenDiff     *diagnose.Diff
}

type Queries struct {
	Store Store
}

func (q Queries) List(ctx context.Context, f ListFilter) ([]RequestSummary, error) {
	if f.Limit <= 0 {
		f.Limit = 20
	}
	return q.Store.ListRequests(ctx, f)
}

func (q Queries) Show(ctx context.Context, id string) (Detail, error) {
	req, err := q.Store.FindRequest(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	attempts, err := q.Store.ListAttempts(ctx, req.ID)
	if err != nil {
		return Detail{}, err
	}
	ver, err := q.Store.LatestVerification(ctx, req.ID)
	if err != nil {
		return Detail{}, err
	}
	d := Detail{Request: req, Attempts: attempts, Verification: ver}
	if req.SeenBody != nil {
		diff := diagnose.Compare(req.Body, req.SeenBody)
		d.SeenDiff = &diff
	}
	return d, nil
}

type Pruner struct {
	Store Store
	Clock Clock
}

func (p Pruner) Prune(ctx context.Context, olderThan time.Duration, keep int) (int64, error) {
	var before time.Time
	if olderThan > 0 {
		before = p.Clock.Now().Add(-olderThan)
	}
	return p.Store.Prune(ctx, before, keep)
}
