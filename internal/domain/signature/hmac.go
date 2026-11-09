package signature

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hash"
	"strings"
)

func Compute(algorithm string, key, message []byte) ([]byte, error) {
	var h func() hash.Hash
	switch strings.ToLower(algorithm) {
	case "sha1":
		h = sha1.New
	case "sha256":
		h = sha256.New
	case "sha512":
		h = sha512.New
	default:
		return nil, fmt.Errorf("unsupported algorithm %q", algorithm)
	}
	mac := hmac.New(h, key)
	mac.Write(message)
	return mac.Sum(nil), nil
}

func Encode(encoding string, mac []byte) (string, error) {
	switch strings.ToLower(encoding) {
	case "hex":
		return hex.EncodeToString(mac), nil
	case "base64":
		return base64.StdEncoding.EncodeToString(mac), nil
	default:
		return "", fmt.Errorf("unsupported encoding %q", encoding)
	}
}

func Decode(encoding, s string) ([]byte, error) {
	switch strings.ToLower(encoding) {
	case "hex":
		return hex.DecodeString(s)
	case "base64":
		return base64.StdEncoding.DecodeString(s)
	default:
		return nil, fmt.Errorf("unsupported encoding %q", encoding)
	}
}

func EqualEncoded(encoding string, mac []byte, provided string) bool {
	got, err := Decode(encoding, provided)
	if err != nil {
		return false
	}
	return hmac.Equal(mac, got)
}
