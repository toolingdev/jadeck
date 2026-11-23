// Package remotetest stands in for Cloudflare Access in tests: a signing
// key, its certs endpoint, and tokens signed like Access signs them.
package remotetest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ptz/internal/remote"
)

const (
	Team = "church"
	AUD  = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

type Keys struct {
	key *rsa.PrivateKey
	srv *httptest.Server
}

func New(t *testing.T) *Keys {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ks := &Keys{key: k}
	ks.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{"kid": "k1", "kty": "RSA",
			"n": base64.RawURLEncoding.EncodeToString(k.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(k.E)).Bytes())}}})
	}))
	t.Cleanup(ks.srv.Close)
	return ks
}

func (ks *Keys) Verifier() *remote.Verifier {
	return &remote.Verifier{CertsURL: func(string) string { return ks.srv.URL }}
}

// Token is a valid Access sign-in for email.
func (ks *Keys) Token(email string) string {
	h, _ := json.Marshal(map[string]any{"alg": "RS256", "kid": "k1"})
	c, _ := json.Marshal(map[string]any{"email": email, "iss": "https://" + Team + ".cloudflareaccess.com", "aud": []string{AUD},
		"exp": time.Now().Add(time.Hour).Unix(), "type": "app"})
	in := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(c)
	sum := sha256.Sum256([]byte(in))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, ks.key, crypto.SHA256, sum[:])
	return in + "." + base64.RawURLEncoding.EncodeToString(sig)
}
