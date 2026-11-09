package web

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/grintzdel/spool/internal/domain/diagnose"
	"github.com/grintzdel/spool/internal/domain/signature"
	"github.com/grintzdel/spool/internal/domain/webhook"
	"github.com/grintzdel/spool/internal/usecase"
)

const (
	textLimit     = 1 << 20
	hexLimit      = 64 << 10
	responseLimit = 400
	shortIDLen    = 12
	timeLayout    = "Jan 02 15:04:05"
)

type badge struct {
	Text  string
	Class string
	Title string
}

type stamp struct {
	Text string
	ISO  string
}

func newStamp(t time.Time) stamp {
	if t.IsZero() {
		return stamp{}
	}
	return stamp{Text: t.Local().Format(timeLayout), ISO: t.UTC().Format(time.RFC3339Nano)}
}

type rowView struct {
	ID           string
	ShortID      string
	Time         stamp
	Method       string
	Target       string
	Provider     string
	EventType    string
	Verification badge
	Status       badge
}

type listView struct {
	Rows      []rowView
	Provider  string
	EventType string
	Failed    bool
	Filtered  bool
	Refresh   string
	Older     string
}

type bodyView struct {
	Size         int
	Empty        bool
	Binary       bool
	Pretty       string
	Raw          string
	Truncated    bool
	Hex          string
	HexTruncated bool
}

type diffView struct {
	Badge     badge
	Meaning   string
	Offset    int
	Raw       string
	App       string
	RawSize   int
	AppSize   int
	RawSHA256 string
	AppSHA256 string
}

type verificationView struct {
	Badge    badge
	Provider string
	Meaning  string
	Time     stamp
	Hints    []string
	Expected string
	Got      string
	Diff     *diffView
	Fixes    []diagnose.Fix
}

type seenView struct {
	Identical bool
	Size      int
	SHA256    string
	Diff      *diffView
}

type attemptView struct {
	Time      stamp
	Kind      string
	Target    string
	Status    badge
	Duration  string
	Resigned  bool
	Error     string
	Response  string
	Truncated bool
}

type liveView struct {
	ID           string
	Verification *verificationView
	Seen         *seenView
	Attempts     []attemptView
}

type detailView struct {
	ID        string
	Method    string
	Target    string
	Time      stamp
	Provider  string
	EventType string
	SHA256    string
	Headers   webhook.Headers
	Body      bodyView
	Curl      string
	Live      liveView
}

type errorView struct {
	Status  string
	Message string
}

func target(r webhook.Request) string {
	if r.Query == "" {
		return r.Path
	}
	return r.Path + "?" + r.Query
}

func reasonBadge(reason string) badge {
	r := signature.Reason(reason)
	b := badge{Text: reason, Title: r.Meaning()}
	switch {
	case reason == "":
		b.Text, b.Class, b.Title = "unverified", "muted", "signature not checked yet"
	case r == signature.OKRaw:
		b.Class = "ok"
	case r.IsBody(), r == signature.TimestampExpired:
		b.Class = "warn"
	default:
		b.Class = "err"
	}
	return b
}

func statusBadge(a *webhook.Attempt) badge {
	switch {
	case a == nil:
		return badge{Text: "—", Class: "muted", Title: "not delivered"}
	case a.Error != "":
		return badge{Text: "error", Class: "err", Title: a.Error}
	}
	b := badge{Text: strconv.Itoa(a.StatusCode), Title: a.TargetURL}
	switch {
	case a.StatusCode < 300:
		b.Class = "ok"
	case a.StatusCode < 400:
		b.Class = "info"
	default:
		b.Class = "err"
	}
	return b
}

func newListView(items []usecase.RequestSummary, f usecase.ListFilter, next string, q url.Values) listView {
	v := listView{
		Provider: f.Provider, EventType: f.EventType, Failed: f.FailedOnly,
		Filtered: f.Provider != "" || f.EventType != "" || f.FailedOnly,
		Refresh:  "/",
	}
	if len(q) > 0 {
		v.Refresh += "?" + q.Encode()
	}
	if next != "" {
		older := url.Values{}
		for k, vals := range q {
			older[k] = vals
		}
		older.Set("before", next)
		v.Older = "/?" + older.Encode()
	}
	for _, it := range items {
		r := it.Request
		row := rowView{
			ID: r.ID, ShortID: r.ID[:min(len(r.ID), shortIDLen)], Time: newStamp(r.ReceivedAt),
			Method: r.Method, Target: target(r), Provider: r.Provider, EventType: r.EventType,
			Status: statusBadge(it.LastAttempt), Verification: reasonBadge(""),
		}
		if it.Verification != nil {
			row.Verification = reasonBadge(it.Verification.Reason)
		}
		v.Rows = append(v.Rows, row)
	}
	return v
}

func newBodyView(body []byte) bodyView {
	v := bodyView{Size: len(body), Empty: len(body) == 0, Binary: !utf8.Valid(body)}
	if v.Empty {
		return v
	}
	v.Hex = hex.Dump(body[:min(len(body), hexLimit)])
	v.HexTruncated = len(body) > hexLimit
	if v.Binary {
		return v
	}
	v.Truncated = len(body) > textLimit
	v.Raw = string(body[:min(len(body), textLimit)])
	if !v.Truncated && json.Valid(body) {
		var buf bytes.Buffer
		if json.Indent(&buf, body, "", "  ") == nil {
			v.Pretty = buf.String()
		}
	}
	return v
}

func newDiffView(d diagnose.Diff) *diffView {
	return &diffView{
		Badge: reasonBadge(string(d.Reason)), Meaning: d.Reason.Meaning(), Offset: d.Offset,
		Raw: d.RawContext, App: d.SeenContext, RawSize: d.RawSize, AppSize: d.SeenSize,
		RawSHA256: d.RawSHA256, AppSHA256: d.SeenSHA256,
	}
}

type storedDetails struct {
	Expected string   `json:"expected"`
	Got      string   `json:"got"`
	Hints    []string `json:"hints"`
	Offset   *int     `json:"offset"`
	Raw      string   `json:"raw"`
	App      string   `json:"app"`
}

func newVerificationView(v *webhook.Verification) *verificationView {
	if v == nil {
		return nil
	}
	reason := signature.Reason(v.Reason)
	var d storedDetails
	_ = json.Unmarshal([]byte(v.Details), &d)
	out := &verificationView{
		Badge: reasonBadge(v.Reason), Provider: v.Provider, Meaning: reason.Meaning(), Time: newStamp(v.CheckedAt),
		Hints: d.Hints, Expected: d.Expected, Got: d.Got,
	}
	if len(out.Hints) == 0 {
		out.Hints = reason.Hints()
	}
	if d.Offset != nil {
		out.Diff = &diffView{Offset: *d.Offset, Raw: d.Raw, App: d.App}
	}
	if reason.IsBody() {
		out.Fixes = diagnose.Fixes()
	}
	return out
}

func newAttemptView(a webhook.Attempt) attemptView {
	resp := strings.ToValidUTF8(string(a.ResponseBody[:min(len(a.ResponseBody), responseLimit)]), "�")
	return attemptView{
		Time: newStamp(a.StartedAt), Kind: string(a.Kind), Target: a.TargetURL, Status: statusBadge(&a),
		Duration: a.Duration.Round(time.Millisecond / 10).String(), Resigned: a.Resigned, Error: a.Error,
		Response: resp, Truncated: len(a.ResponseBody) > responseLimit,
	}
}

func newLiveView(d usecase.Detail) liveView {
	v := liveView{ID: d.Request.ID, Verification: newVerificationView(d.Verification)}
	if d.Request.SeenBody != nil {
		seen := &seenView{Identical: true, Size: len(d.Request.SeenBody), SHA256: webhook.SHA256Hex(d.Request.SeenBody)}
		if d.SeenDiff != nil && !d.SeenDiff.Identical() {
			seen.Identical, seen.Diff = false, newDiffView(*d.SeenDiff)
		}
		v.Seen = seen
	}
	for i := len(d.Attempts) - 1; i >= 0; i-- {
		v.Attempts = append(v.Attempts, newAttemptView(d.Attempts[i]))
	}
	return v
}

func newDetailView(d usecase.Detail, curl string) detailView {
	r := d.Request
	return detailView{
		ID: r.ID, Method: r.Method, Target: target(r), Time: newStamp(r.ReceivedAt), Provider: r.Provider,
		EventType: r.EventType, SHA256: r.BodySHA256, Headers: r.Headers, Body: newBodyView(r.Body),
		Curl: curl, Live: newLiveView(d),
	}
}
