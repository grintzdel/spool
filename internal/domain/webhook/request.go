package webhook

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

var (
	ErrNotFound  = errors.New("request not found")
	ErrAmbiguous = errors.New("id prefix matches several requests")
)

type Request struct {
	ID         string
	ReceivedAt time.Time
	Method     string
	Path       string
	Query      string
	Headers    Headers
	Body       []byte
	BodySHA256 string
	SeenBody   []byte
	Provider   string
	EventType  string
}

func NewRequest(id string, receivedAt time.Time, method, path, query string, headers Headers, body []byte) Request {
	if body == nil {
		body = []byte{}
	}
	return Request{
		ID:         id,
		ReceivedAt: receivedAt,
		Method:     method,
		Path:       path,
		Query:      query,
		Headers:    headers,
		Body:       body,
		BodySHA256: SHA256Hex(body),
	}
}

func SHA256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (r Request) URLOn(base string) string {
	u := strings.TrimSuffix(base, "/") + r.Path
	if r.Query != "" {
		u += "?" + r.Query
	}
	return u
}

type AttemptKind string

const (
	AttemptForward AttemptKind = "forward"
	AttemptReplay  AttemptKind = "replay"
)

type Attempt struct {
	ID           string
	RequestID    string
	Kind         AttemptKind
	TargetURL    string
	Resigned     bool
	StartedAt    time.Time
	Duration     time.Duration
	StatusCode   int
	ResponseBody []byte
	Error        string
}

func (a Attempt) Failed() bool {
	return a.Error != "" || a.StatusCode >= 400
}

type Verification struct {
	ID        string
	RequestID string
	Provider  string
	OK        bool
	Reason    string
	Details   string
	CheckedAt time.Time
}
