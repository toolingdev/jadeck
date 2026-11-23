package remote

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

// TestKeys is a Cloudflare stand-in: a signing key and its certs endpoint.
type TestKeys struct {
	Key *rsa.PrivateKey
	Kid string
	Srv *httptest.Server
}

func NewTestKeys(t *testing.T) *TestKeys {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tk := &TestKeys{Key: k, Kid: "kid-1"}
	tk.Srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{
			"kid": tk.Kid, "kty": "RSA", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(k.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(k.E)).Bytes()),
		}}})
	}))
	t.Cleanup(tk.Srv.Close)
	return tk
}

// Token signs claims like Cloudflare Access does.
func (tk *TestKeys) Token(claims map[string]any) string {
	h, _ := json.Marshal(map[string]any{"alg": "RS256", "kid": tk.Kid, "typ": "JWT"})
	c, _ := json.Marshal(claims)
	in := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(c)
	sum := sha256.Sum256([]byte(in))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, tk.Key, crypto.SHA256, sum[:])
	return in + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func (tk *TestKeys) Verifier() *Verifier {
	return &Verifier{CertsURL: func(string) string { return tk.Srv.URL }}
}

const team, aud = "church", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func good(email string) map[string]any {
	return map[string]any{"email": email, "iss": "https://" + team + ".cloudflareaccess.com", "aud": []string{aud},
		"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "type": "app"}
}

func TestVerify(t *testing.T) {
	tk := NewTestKeys(t)
	v := tk.Verifier()
	ctx := context.Background()
	email, err := v.Verify(ctx, tk.Token(good("Pastor@Example.com")), team, aud)
	if err != nil || email != "pastor@example.com" {
		t.Fatalf("%q %v", email, err)
	}
	bad := map[string]map[string]any{}
	exp := good("a@b.co")
	exp["exp"] = time.Now().Add(-time.Hour).Unix()
	bad["expired"] = exp
	wa := good("a@b.co")
	wa["aud"] = []string{"ffff"}
	bad["other application"] = wa
	wi := good("a@b.co")
	wi["iss"] = "https://evil.cloudflareaccess.com"
	bad["other team"] = wi
	ne := good("")
	bad["no email"] = ne
	for name, c := range bad {
		if _, err := v.Verify(ctx, tk.Token(c), team, aud); !errors.Is(err, ErrBadToken) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// signed by someone else
	other := NewTestKeys(t)
	if _, err := v.Verify(ctx, other.Token(good("a@b.co")), team, aud); !errors.Is(err, ErrBadToken) {
		t.Errorf("forged: %v", err)
	}
	// tampered claims
	tok := tk.Token(good("a@b.co"))
	b, _ := json.Marshal(good("boss@b.co"))
	parts := splitTok(tok)
	if _, err := v.Verify(ctx, parts[0]+"."+base64.RawURLEncoding.EncodeToString(b)+"."+parts[2], team, aud); !errors.Is(err, ErrBadToken) {
		t.Errorf("tampered: %v", err)
	}
	if _, err := v.Verify(ctx, "", team, aud); !errors.Is(err, ErrNoToken) {
		t.Error(err)
	}
	if _, err := v.Verify(ctx, "x.y", team, aud); !errors.Is(err, ErrBadToken) {
		t.Error(err)
	}
}

func splitTok(s string) [3]string {
	var out [3]string
	i := 0
	start := 0
	for j := 0; j < len(s); j++ {
		if s[j] == '.' {
			out[i] = s[start:j]
			i++
			start = j + 1
		}
	}
	out[2] = s[start:]
	return out
}

func TestStore(t *testing.T) {
	p := filepath.Join(t.TempDir(), "remote.json")
	s, err := Open(p)
	if err != nil || s.Get().Port != DefaultPort || s.Get().People == nil {
		t.Fatal(err, "(people must be [] not null: the Settings card reads its length)")
	}
	c := Config{Enabled: true, Team: "Church.cloudflareaccess.com", AUD: aud, Link: "https://live.example.com/",
		People: []Person{{Email: " Pastor@Example.com ", Name: "Pastor", Dashboards: []int64{7, 8}}, {Email: "me@example.com", Name: "Me", Everything: true}}}
	if err := s.Set(c); err != nil {
		t.Fatal(err)
	}
	s.Seen("pastor@example.com", time.Now())
	s2, _ := Open(p)
	g := s2.Get()
	if g.Team != "church" || g.Link != "https://live.example.com" || !g.Find("PASTOR@example.com").Sees(8) || g.Find("pastor@example.com").Sees(9) || !g.Find("me@example.com").Sees(9) || g.Find("pastor@example.com").LastSeen == nil {
		t.Fatalf("%+v", g)
	}
	for _, bad := range []Config{
		{Enabled: true}, // no team / aud
		{People: []Person{{Email: "nope", Everything: true}}},
		{People: []Person{{Email: "a@b.co", Everything: true}, {Email: "A@b.co", Everything: true}}},
		{People: []Person{{Email: "a@b.co"}}}, // sees nothing
		{Link: "http://x"},
		{Team: "bad team"},
	} {
		if err := s.Set(bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}
