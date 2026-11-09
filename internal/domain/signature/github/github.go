package github

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/grintzdel/spool/internal/domain/signature"
	"github.com/grintzdel/spool/internal/domain/webhook"
)

const (
	Header      = "X-Hub-Signature-256"
	EventHeader = "X-GitHub-Event"
	prefix      = "sha256="
)

func init() { signature.Register(New()) }

type Provider struct{}

func New() Provider { return Provider{} }

func (Provider) Name() string { return "github" }

func (Provider) Detect(h webhook.Headers) bool { return h.Has(Header) || h.Has(EventHeader) }

func (Provider) EventType(h webhook.Headers, body []byte) string {
	event := h.Get(EventHeader)
	if event == "" {
		return ""
	}
	var payload struct {
		Action string `json:"action"`
	}
	if json.Unmarshal(body, &payload) == nil && payload.Action != "" {
		return event + "." + payload.Action
	}
	return event
}

func (Provider) Verify(req webhook.Request, secret []byte, _ signature.Options) signature.Result {
	raw := req.Headers.Get(Header)
	if raw == "" {
		return signature.Fail(signature.HeaderMissing, "", "")
	}
	sig, ok := strings.CutPrefix(raw, prefix)
	if !ok {
		return signature.Fail(signature.HeaderMalformed, prefix+"<hex>", raw)
	}
	if _, err := signature.Decode("hex", sig); err != nil {
		return signature.Fail(signature.HeaderMalformed, prefix+"<hex>", raw)
	}
	mac, _ := signature.Compute("sha256", secret, req.Body)
	expected, _ := signature.Encode("hex", mac)
	if signature.EqualEncoded("hex", mac, sig) {
		return signature.Pass(prefix+expected, raw)
	}
	return signature.Fail(signature.SecretMismatch, prefix+expected, raw)
}

func (Provider) Sign(body []byte, secret []byte, _ time.Time) (webhook.Headers, error) {
	mac, _ := signature.Compute("sha256", secret, body)
	sig, _ := signature.Encode("hex", mac)
	return webhook.Headers{{Name: Header, Value: prefix + sig}}, nil
}
