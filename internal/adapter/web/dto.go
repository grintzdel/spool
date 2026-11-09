package web

import (
	"encoding/base64"
	"encoding/json"
	"time"
	"unicode/utf8"

	"github.com/grintzdel/spool/internal/domain/diagnose"
	"github.com/grintzdel/spool/internal/domain/signature"
	"github.com/grintzdel/spool/internal/domain/webhook"
	"github.com/grintzdel/spool/internal/usecase"
)

type requestJSON struct {
	ID         string    `json:"id"`
	ReceivedAt time.Time `json:"received_at"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	Query      string    `json:"query"`
	Provider   string    `json:"provider"`
	EventType  string    `json:"event_type"`
	BodySize   int       `json:"body_size"`
	BodySHA256 string    `json:"body_sha256"`
}

type summaryJSON struct {
	requestJSON
	LastAttempt  *attemptJSON      `json:"last_attempt"`
	Verification *verificationJSON `json:"verification"`
}

type listJSON struct {
	Items []summaryJSON `json:"items"`
	Next  string        `json:"next"`
}

type detailJSON struct {
	requestJSON
	Headers      [][2]string       `json:"headers"`
	Body         string            `json:"body"`
	BodyEncoding string            `json:"body_encoding"`
	Seen         *seenJSON         `json:"seen"`
	Attempts     []attemptJSON     `json:"attempts"`
	Verification *verificationJSON `json:"verification"`
}

type seenJSON struct {
	Size         int       `json:"size"`
	SHA256       string    `json:"sha256"`
	Body         string    `json:"body"`
	BodyEncoding string    `json:"body_encoding"`
	Identical    bool      `json:"identical"`
	Diff         *diffJSON `json:"diff"`
}

type diffJSON struct {
	Reason    string `json:"reason"`
	Meaning   string `json:"meaning"`
	Offset    int    `json:"offset"`
	Raw       string `json:"raw"`
	App       string `json:"app"`
	RawSize   int    `json:"raw_size"`
	AppSize   int    `json:"app_size"`
	RawSHA256 string `json:"raw_sha256"`
	AppSHA256 string `json:"app_sha256"`
}

type attemptJSON struct {
	ID                   string    `json:"id"`
	Kind                 string    `json:"kind"`
	TargetURL            string    `json:"target_url"`
	Resigned             bool      `json:"resigned"`
	StartedAt            time.Time `json:"started_at"`
	DurationMS           float64   `json:"duration_ms"`
	StatusCode           int       `json:"status_code"`
	Error                string    `json:"error"`
	Failed               bool      `json:"failed"`
	ResponseBody         string    `json:"response_body"`
	ResponseBodyEncoding string    `json:"response_body_encoding"`
}

type verificationJSON struct {
	ID        string          `json:"id"`
	Provider  string          `json:"provider"`
	OK        bool            `json:"ok"`
	Reason    string          `json:"reason"`
	Meaning   string          `json:"meaning"`
	Details   json.RawMessage `json:"details,omitempty"`
	CheckedAt time.Time       `json:"checked_at"`
}

type replayJSON struct {
	Attempts []attemptJSON `json:"attempts"`
	Warnings []string      `json:"warnings"`
}

type fixJSON struct {
	Framework string `json:"framework"`
	Snippet   string `json:"snippet"`
}

type verifyJSON struct {
	VerificationID string    `json:"verification_id"`
	Provider       string    `json:"provider"`
	OK             bool      `json:"ok"`
	Reason         string    `json:"reason"`
	Meaning        string    `json:"meaning"`
	Hints          []string  `json:"hints"`
	Expected       string    `json:"expected"`
	Got            string    `json:"got"`
	Diff           *diffJSON `json:"diff"`
	Fixes          []fixJSON `json:"fixes"`
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func encodeBody(b []byte) (string, string) {
	if utf8.Valid(b) {
		return string(b), "utf8"
	}
	return base64.StdEncoding.EncodeToString(b), "base64"
}

func toRequestJSON(r webhook.Request) requestJSON {
	return requestJSON{
		ID: r.ID, ReceivedAt: r.ReceivedAt, Method: r.Method, Path: r.Path, Query: r.Query,
		Provider: r.Provider, EventType: r.EventType, BodySize: len(r.Body), BodySHA256: r.BodySHA256,
	}
}

func toSummaryJSON(s usecase.RequestSummary) summaryJSON {
	out := summaryJSON{requestJSON: toRequestJSON(s.Request), Verification: toVerificationJSON(s.Verification)}
	if s.LastAttempt != nil {
		a := toAttemptJSON(*s.LastAttempt)
		out.LastAttempt = &a
	}
	return out
}

func toDetailJSON(d usecase.Detail) detailJSON {
	out := detailJSON{
		requestJSON:  toRequestJSON(d.Request),
		Headers:      make([][2]string, 0, len(d.Request.Headers)),
		Attempts:     make([]attemptJSON, 0, len(d.Attempts)),
		Verification: toVerificationJSON(d.Verification),
	}
	out.Body, out.BodyEncoding = encodeBody(d.Request.Body)
	for _, h := range d.Request.Headers {
		out.Headers = append(out.Headers, [2]string{h.Name, h.Value})
	}
	for _, a := range d.Attempts {
		out.Attempts = append(out.Attempts, toAttemptJSON(a))
	}
	if d.Request.SeenBody != nil {
		seen := &seenJSON{Size: len(d.Request.SeenBody), SHA256: webhook.SHA256Hex(d.Request.SeenBody), Identical: true}
		seen.Body, seen.BodyEncoding = encodeBody(d.Request.SeenBody)
		if d.SeenDiff != nil && !d.SeenDiff.Identical() {
			seen.Identical, seen.Diff = false, toDiffJSON(*d.SeenDiff)
		}
		out.Seen = seen
	}
	return out
}

func toDiffJSON(d diagnose.Diff) *diffJSON {
	return &diffJSON{
		Reason: string(d.Reason), Meaning: d.Reason.Meaning(), Offset: d.Offset, Raw: d.RawContext, App: d.SeenContext,
		RawSize: d.RawSize, AppSize: d.SeenSize, RawSHA256: d.RawSHA256, AppSHA256: d.SeenSHA256,
	}
}

func toAttemptJSON(a webhook.Attempt) attemptJSON {
	out := attemptJSON{
		ID: a.ID, Kind: string(a.Kind), TargetURL: a.TargetURL, Resigned: a.Resigned, StartedAt: a.StartedAt,
		DurationMS: float64(a.Duration.Microseconds()) / 1000, StatusCode: a.StatusCode, Error: a.Error, Failed: a.Failed(),
	}
	out.ResponseBody, out.ResponseBodyEncoding = encodeBody(a.ResponseBody)
	return out
}

func toVerificationJSON(v *webhook.Verification) *verificationJSON {
	if v == nil {
		return nil
	}
	out := &verificationJSON{
		ID: v.ID, Provider: v.Provider, OK: v.OK, Reason: v.Reason,
		Meaning: signature.Reason(v.Reason).Meaning(), CheckedAt: v.CheckedAt,
	}
	if json.Valid([]byte(v.Details)) {
		out.Details = json.RawMessage(v.Details)
	}
	return out
}

func toVerifyJSON(r usecase.Report) verifyJSON {
	out := verifyJSON{
		VerificationID: r.Verification.ID, Provider: r.Provider, OK: r.Result.OK, Reason: string(r.Reason()),
		Meaning: r.Reason().Meaning(), Hints: nonNil(r.Result.Hints), Expected: r.Result.Expected, Got: r.Result.Got,
		Fixes: make([]fixJSON, 0, len(r.Fixes)),
	}
	if r.Diff != nil {
		out.Diff = toDiffJSON(*r.Diff)
	}
	for _, f := range r.Fixes {
		out.Fixes = append(out.Fixes, fixJSON{Framework: f.Framework, Snippet: f.Snippet})
	}
	return out
}
