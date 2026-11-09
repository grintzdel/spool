package signature

type Reason string

const (
	OKRaw            Reason = "OK_RAW"
	HeaderMissing    Reason = "HEADER_MISSING"
	HeaderMalformed  Reason = "HEADER_MALFORMED"
	TimestampExpired Reason = "TIMESTAMP_EXPIRED"
	SecretMismatch   Reason = "SECRET_MISMATCH"
	BodyReformatted  Reason = "BODY_REFORMATTED"
	BodyWhitespace   Reason = "BODY_WHITESPACE"
	BodyUnicode      Reason = "BODY_UNICODE"
	BodyChanged      Reason = "BODY_CHANGED"
)

var reasonInfo = map[Reason]struct {
	meaning string
	hints   []string
}{
	OKRaw: {
		"Signature is valid on the raw body",
		[]string{"Sender and secret are correct. If your app still rejects it, it alters the body before verifying or uses another secret."},
	},
	HeaderMissing: {
		"Signature header is missing",
		[]string{"The secret is not configured on the provider side, or a proxy strips the header."},
	},
	HeaderMalformed: {
		"Signature header cannot be parsed",
		[]string{"The wrong provider is probably selected (try --provider)."},
	},
	TimestampExpired: {
		"Signed timestamp is outside the tolerance window",
		[]string{"This is a replay of an old event: send it again with --resign."},
	},
	SecretMismatch: {
		"HMAC differs on the raw body",
		[]string{"Wrong secret: test vs live, or the dashboard secret instead of the one printed by `stripe listen`."},
	},
	BodyReformatted: {
		"Same JSON value, different bytes",
		[]string{"The body was parsed then re-serialized before verification. Verify against the raw bytes."},
	},
	BodyWhitespace: {
		"Difference limited to line endings, BOM or trailing newline",
		[]string{"A text or encoding middleware rewrote the body."},
	},
	BodyUnicode: {
		"Unicode escapes rewritten (\\u00e9 vs é)",
		[]string{"The body went through a JSON re-serialization."},
	},
	BodyChanged: {
		"Content actually differs",
		[]string{"A middleware modifies or truncates the body; see the first divergent byte below."},
	},
}

func (r Reason) Meaning() string { return reasonInfo[r].meaning }

func (r Reason) Hints() []string { return append([]string(nil), reasonInfo[r].hints...) }

func (r Reason) IsBody() bool {
	switch r {
	case BodyReformatted, BodyWhitespace, BodyUnicode, BodyChanged:
		return true
	}
	return false
}

func AllReasons() []Reason {
	return []Reason{
		OKRaw, HeaderMissing, HeaderMalformed, TimestampExpired, SecretMismatch,
		BodyReformatted, BodyWhitespace, BodyUnicode, BodyChanged,
	}
}
