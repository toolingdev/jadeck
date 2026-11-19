// Package digest is a minimal HTTP Digest (RFC 7616, MD5 / qop=auth)
// client transport, enough for the camera's param.cgi and snapshot.jpg
// on firmware 6.3.45+. Only body-less requests (GET) are retried.
package digest

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

type Transport struct {
	User, Pass string
	Base       http.RoundTripper

	mu   sync.Mutex
	chal map[string]string // cached challenge, reused with rising nc
	nc   int
}

func (t *Transport) base() http.RoundTripper {
	if t.Base != nil {
		return t.Base
	}
	return http.DefaultTransport
}

func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.User == "" {
		return t.base().RoundTrip(req)
	}
	t.mu.Lock()
	chal := t.chal
	t.mu.Unlock()
	first := req.Clone(req.Context())
	if chal != nil {
		first.Header.Set("Authorization", t.authorize(chal, req))
	}
	resp, err := t.base().RoundTrip(first)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}
	// 401: take the (fresh or stale-nonce) challenge from this reply.
	c := parseChallenge(resp.Header.Get("WWW-Authenticate"))
	resp.Body.Close()
	if c == nil {
		return nil, fmt.Errorf("digest: unsupported auth challenge")
	}
	t.mu.Lock()
	t.chal, t.nc = c, 0
	t.mu.Unlock()
	retry := req.Clone(req.Context())
	retry.Header.Set("Authorization", t.authorize(c, req))
	return t.base().RoundTrip(retry)
}

func parseChallenge(h string) map[string]string {
	scheme, rest, ok := strings.Cut(strings.TrimSpace(h), " ")
	if !ok || !strings.EqualFold(scheme, "Digest") {
		return nil
	}
	out := map[string]string{}
	for _, part := range splitParams(rest) {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		out[strings.ToLower(strings.TrimSpace(k))] = strings.Trim(strings.TrimSpace(v), `"`)
	}
	if out["nonce"] == "" {
		return nil
	}
	if a := out["algorithm"]; a != "" && !strings.EqualFold(a, "MD5") {
		return nil
	}
	return out
}

// splitParams splits on commas outside quotes.
func splitParams(s string) []string {
	var parts []string
	var b strings.Builder
	inQ := false
	for _, r := range s {
		switch {
		case r == '"':
			inQ = !inQ
			b.WriteRune(r)
		case r == ',' && !inQ:
			parts = append(parts, b.String())
			b.Reset()
		default:
			b.WriteRune(r)
		}
	}
	if b.Len() > 0 {
		parts = append(parts, b.String())
	}
	return parts
}

func md5hex(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

func (t *Transport) authorize(c map[string]string, req *http.Request) string {
	t.mu.Lock()
	t.nc++
	nc := fmt.Sprintf("%08x", t.nc)
	t.mu.Unlock()
	uri := req.URL.RequestURI()
	ha1 := md5hex(t.User + ":" + c["realm"] + ":" + t.Pass)
	ha2 := md5hex(req.Method + ":" + uri)
	cb := make([]byte, 8)
	rand.Read(cb)
	cnonce := hex.EncodeToString(cb)
	var resp string
	qop := ""
	if q := c["qop"]; q != "" {
		for _, o := range strings.Split(q, ",") {
			if strings.TrimSpace(o) == "auth" {
				qop = "auth"
			}
		}
	}
	if qop != "" {
		resp = md5hex(ha1 + ":" + c["nonce"] + ":" + nc + ":" + cnonce + ":" + qop + ":" + ha2)
	} else {
		resp = md5hex(ha1 + ":" + c["nonce"] + ":" + ha2)
	}
	s := fmt.Sprintf(`Digest username="%s", realm="%s", nonce="%s", uri="%s", response="%s"`,
		t.User, c["realm"], c["nonce"], uri, resp)
	if c["opaque"] != "" {
		s += fmt.Sprintf(`, opaque="%s"`, c["opaque"])
	}
	if qop != "" {
		s += fmt.Sprintf(`, qop=%s, nc=%s, cnonce="%s"`, qop, nc, cnonce)
	}
	if c["algorithm"] != "" {
		s += ", algorithm=MD5"
	}
	return s
}
