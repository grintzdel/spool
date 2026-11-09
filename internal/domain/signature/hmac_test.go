package signature_test

import (
	"testing"

	"github.com/grintzdel/spool/internal/domain/signature"
)

func TestComputeAndEncode(t *testing.T) {
	tests := []struct {
		algo, enc, want string
	}{
		{"sha256", "hex", "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8"},
		{"sha1", "hex", "de7c9b85b8b78aa6bc8a7a36f70a90701c9db4d9"},
		{"sha256", "base64", "97yD9DBThCSxMpjmqm+xQ+9NWaFJRhdZl0edvC0aPNg="},
	}
	for _, tt := range tests {
		t.Run(tt.algo+"/"+tt.enc, func(t *testing.T) {
			mac, err := signature.Compute(tt.algo, []byte("key"), []byte("The quick brown fox jumps over the lazy dog"))
			if err != nil {
				t.Fatal(err)
			}
			got, err := signature.Encode(tt.enc, mac)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestComputeRejectsUnknownAlgorithm(t *testing.T) {
	if _, err := signature.Compute("md5", nil, nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestEqualEncodedIsConstantTimeSafeOnBadInput(t *testing.T) {
	mac, _ := signature.Compute("sha256", []byte("k"), []byte("m"))
	good, _ := signature.Encode("hex", mac)
	if !signature.EqualEncoded("hex", mac, good) {
		t.Fatal("expected equal")
	}
	if signature.EqualEncoded("hex", mac, "zz") {
		t.Fatal("garbage must not match")
	}
}
