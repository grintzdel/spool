package signature

import (
	"time"

	"github.com/grintzdel/spool/internal/domain/webhook"
)

type Provider interface {
	Name() string
	Detect(h webhook.Headers) bool
	EventType(h webhook.Headers, body []byte) string
	Verify(req webhook.Request, secret []byte, opts Options) Result
	Sign(body []byte, secret []byte, ts time.Time) (webhook.Headers, error)
}

type Options struct {
	Tolerance time.Duration
}

type Result struct {
	OK       bool
	Reason   Reason
	Expected string
	Got      string
	Hints    []string
}

func Fail(reason Reason, expected, got string) Result {
	return Result{Reason: reason, Expected: expected, Got: got, Hints: reason.Hints()}
}

func Pass(expected, got string) Result {
	return Result{OK: true, Reason: OKRaw, Expected: expected, Got: got, Hints: OKRaw.Hints()}
}

func CheckTimestamp(signedAt, receivedAt time.Time, tolerance time.Duration) bool {
	if tolerance <= 0 {
		return true
	}
	diff := receivedAt.Sub(signedAt)
	if diff < 0 {
		diff = -diff
	}
	return diff <= tolerance
}

type Timestamped interface {
	SignedAt(h webhook.Headers) (time.Time, bool)
}
