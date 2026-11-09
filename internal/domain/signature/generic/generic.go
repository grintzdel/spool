package generic

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/grintzdel/spool/internal/domain/signature"
	"github.com/grintzdel/spool/internal/domain/webhook"
)

type Config struct {
	Name            string `toml:"name"`
	Header          string `toml:"header"`
	TimestampHeader string `toml:"timestamp_header"`
	Template        string `toml:"template"`
	Algorithm       string `toml:"algorithm"`
	Encoding        string `toml:"encoding"`
	Prefix          string `toml:"prefix"`
	EventHeader     string `toml:"event_header"`
}

type Provider struct{ cfg Config }

func New(cfg Config) (Provider, error) {
	if cfg.Name == "" {
		cfg.Name = "generic"
	}
	if cfg.Template == "" {
		cfg.Template = "{body}"
	}
	if cfg.Algorithm == "" {
		cfg.Algorithm = "sha256"
	}
	if cfg.Encoding == "" {
		cfg.Encoding = "hex"
	}
	if cfg.Header == "" {
		return Provider{}, errors.New("generic provider: header is required")
	}
	if _, err := signature.Compute(cfg.Algorithm, nil, nil); err != nil {
		return Provider{}, err
	}
	if _, err := signature.Encode(cfg.Encoding, nil); err != nil {
		return Provider{}, err
	}
	if strings.Contains(cfg.Template, "{ts}") && cfg.TimestampHeader == "" {
		return Provider{}, errors.New("generic provider: template uses {ts} but timestamp_header is empty")
	}
	return Provider{cfg: cfg}, nil
}

func (p Provider) Name() string { return p.cfg.Name }

func (p Provider) Detect(h webhook.Headers) bool { return h.Has(p.cfg.Header) }

func (p Provider) EventType(h webhook.Headers, _ []byte) string {
	if p.cfg.EventHeader == "" {
		return ""
	}
	return h.Get(p.cfg.EventHeader)
}

func (p Provider) usesTimestamp() bool { return strings.Contains(p.cfg.Template, "{ts}") }

func (p Provider) Verify(req webhook.Request, secret []byte, opts signature.Options) signature.Result {
	raw := req.Headers.Get(p.cfg.Header)
	if raw == "" {
		return signature.Fail(signature.HeaderMissing, "", "")
	}
	sig, ok := strings.CutPrefix(raw, p.cfg.Prefix)
	if !ok {
		return signature.Fail(signature.HeaderMalformed, p.cfg.Prefix+"<"+p.cfg.Encoding+">", raw)
	}
	var ts string
	if p.usesTimestamp() {
		ts = req.Headers.Get(p.cfg.TimestampHeader)
		sec, err := strconv.ParseInt(ts, 10, 64)
		if err != nil {
			return signature.Fail(signature.HeaderMalformed, p.cfg.TimestampHeader+": <unix seconds>", ts)
		}
		if !signature.CheckTimestamp(time.Unix(sec, 0), req.ReceivedAt, opts.Tolerance) {
			return signature.Fail(signature.TimestampExpired, "within "+opts.Tolerance.String(), ts)
		}
	}
	mac := p.mac(req.Body, secret, ts)
	expected, _ := signature.Encode(p.cfg.Encoding, mac)
	if signature.EqualEncoded(p.cfg.Encoding, mac, sig) {
		return signature.Pass(p.cfg.Prefix+expected, raw)
	}
	return signature.Fail(signature.SecretMismatch, p.cfg.Prefix+expected, raw)
}

func (p Provider) Sign(body []byte, secret []byte, now time.Time) (webhook.Headers, error) {
	var ts string
	h := webhook.Headers{}
	if p.usesTimestamp() {
		ts = strconv.FormatInt(now.Unix(), 10)
		h = append(h, webhook.Header{Name: p.cfg.TimestampHeader, Value: ts})
	}
	sig, err := signature.Encode(p.cfg.Encoding, p.mac(body, secret, ts))
	if err != nil {
		return nil, err
	}
	return append(h, webhook.Header{Name: p.cfg.Header, Value: p.cfg.Prefix + sig}), nil
}

func (p Provider) mac(body, secret []byte, ts string) []byte {
	before, after, hasBody := strings.Cut(p.cfg.Template, "{body}")
	var msg []byte
	msg = append(msg, strings.ReplaceAll(before, "{ts}", ts)...)
	if hasBody {
		msg = append(msg, body...)
		msg = append(msg, strings.ReplaceAll(after, "{ts}", ts)...)
	}
	mac, _ := signature.Compute(p.cfg.Algorithm, secret, msg)
	return mac
}

func (p Provider) SignedAt(h webhook.Headers) (time.Time, bool) {
	if !p.usesTimestamp() {
		return time.Time{}, false
	}
	sec, err := strconv.ParseInt(h.Get(p.cfg.TimestampHeader), 10, 64)
	return time.Unix(sec, 0), err == nil
}
