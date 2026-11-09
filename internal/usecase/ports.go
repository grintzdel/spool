package usecase

import (
	"context"
	"errors"
	"time"

	"github.com/grintzdel/spool/internal/domain/webhook"
)

var (
	ErrNoSecret        = errors.New("no secret configured for this provider")
	ErrUnknownProvider = errors.New("unknown provider")
	ErrNoProvider      = errors.New("provider not detected: pass --provider")
	ErrNoTarget        = errors.New("no target URL: pass --to or configure a forward target")
	ErrSeenMismatch    = errors.New("seen body does not match its announced sha256")
)

type ListFilter struct {
	Provider   string
	EventType  string
	FailedOnly bool
	Limit      int
	BeforeID   string
}

type RequestSummary struct {
	Request      webhook.Request
	LastAttempt  *webhook.Attempt
	Verification *webhook.Verification
}

type Store interface {
	SaveRequest(ctx context.Context, r webhook.Request) error
	FindRequest(ctx context.Context, idOrPrefix string) (webhook.Request, error)
	ListRequests(ctx context.Context, f ListFilter) ([]RequestSummary, error)
	SetSeenBody(ctx context.Context, id string, body []byte) error
	SaveAttempt(ctx context.Context, a webhook.Attempt) error
	ListAttempts(ctx context.Context, requestID string) ([]webhook.Attempt, error)
	SaveVerification(ctx context.Context, v webhook.Verification) error
	LatestVerification(ctx context.Context, requestID string) (*webhook.Verification, error)
	Prune(ctx context.Context, before time.Time, keep int) (int64, error)
}

type Outgoing struct {
	Method  string
	URL     string
	Headers webhook.Headers
	Body    []byte
}

type Response struct {
	StatusCode int
	Headers    webhook.Headers
	Body       []byte
}

type Sender interface {
	Send(ctx context.Context, out Outgoing) (Response, error)
}

type EventKind string

const (
	EventRequest      EventKind = "request"
	EventAttempt      EventKind = "attempt"
	EventVerification EventKind = "verification"
)

type Event struct {
	Kind      EventKind `json:"kind"`
	RequestID string    `json:"request_id"`
}

type Publisher interface {
	Publish(e Event)
}

type Secrets interface {
	Secret(provider, envOverride string) ([]byte, error)
}

type Clock interface {
	Now() time.Time
}

type IDs interface {
	NewID() string
}

type ClockFunc func() time.Time

func (f ClockFunc) Now() time.Time { return f() }

type NopPublisher struct{}

func (NopPublisher) Publish(Event) {}
