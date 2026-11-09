package stripe

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/grintzdel/spool/internal/domain/signature"
	"github.com/grintzdel/spool/internal/domain/webhook"
)

const Header = "Stripe-Signature"

func init() { signature.Register(New()) }

type Provider struct{}

func New() Provider { return Provider{} }

func (Provider) Name() string { return "stripe" }

func (Provider) Detect(h webhook.Headers) bool { return h.Has(Header) }

func (Provider) EventType(_ webhook.Headers, body []byte) string {
	var evt struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(body, &evt) != nil {
		return ""
	}
	return evt.Type
}

func (Provider) Verify(req webhook.Request, secret []byte, opts signature.Options) signature.Result {
	raw := req.Headers.Get(Header)
	if raw == "" {
		return signature.Fail(signature.HeaderMissing, "", "")
	}
	ts, sigs, err := ParseHeader(raw)
	if err != nil {
		return signature.Fail(signature.HeaderMalformed, "t=<unix>,v1=<hex>", raw)
	}
	if !signature.CheckTimestamp(ts, req.ReceivedAt, opts.Tolerance) {
		return signature.Fail(signature.TimestampExpired, "within "+opts.Tolerance.String(), req.ReceivedAt.Sub(ts).Round(time.Second).String()+" apart")
	}
	mac := sign(req.Body, secret, ts)
	expected, _ := signature.Encode("hex", mac)
	for _, s := range sigs {
		if signature.EqualEncoded("hex", mac, s) {
			return signature.Pass(expected, s)
		}
	}
	return signature.Fail(signature.SecretMismatch, expected, strings.Join(sigs, ","))
}

func (Provider) Sign(body []byte, secret []byte, ts time.Time) (webhook.Headers, error) {
	sig, _ := signature.Encode("hex", sign(body, secret, ts))
	return webhook.Headers{{Name: Header, Value: FormatHeader(ts, []string{sig})}}, nil
}

func sign(body, secret []byte, ts time.Time) []byte {
	payload := make([]byte, 0, len(body)+12)
	payload = strconv.AppendInt(payload, ts.Unix(), 10)
	payload = append(payload, '.')
	payload = append(payload, body...)
	mac, _ := signature.Compute("sha256", secret, payload)
	return mac
}

var errMalformed = errors.New("malformed Stripe-Signature header")

func ParseHeader(header string) (time.Time, []string, error) {
	var (
		ts    time.Time
		hasTS bool
		sigs  []string
	)
	for part := range strings.SplitSeq(header, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch key {
		case "t":
			sec, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return time.Time{}, nil, errMalformed
			}
			ts, hasTS = time.Unix(sec, 0), true
		case "v1":
			if value != "" && !strings.ContainsAny(value, ", \t") {
				sigs = append(sigs, value)
			}
		}
	}
	if !hasTS || len(sigs) == 0 {
		return time.Time{}, nil, errMalformed
	}
	return ts, sigs, nil
}

func FormatHeader(ts time.Time, sigs []string) string {
	var b strings.Builder
	b.WriteString("t=")
	b.WriteString(strconv.FormatInt(ts.Unix(), 10))
	for _, s := range sigs {
		b.WriteString(",v1=")
		b.WriteString(s)
	}
	return b.String()
}

func (Provider) SignedAt(h webhook.Headers) (time.Time, bool) {
	ts, _, err := ParseHeader(h.Get(Header))
	return ts, err == nil
}
