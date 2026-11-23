// Package remote is jadeck's door to the internet: a Cloudflare Tunnel
// connects to a listener on 127.0.0.1 that trusts nothing, and every
// request must carry Cloudflare Access's signed sign-in token. The token's
// email picks a person from the list: "everything", or one dashboard.
//
// Nothing here changes the local network: screens on the venue's network
// keep coming in on the normal port, trusted as before.
package remote

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Person is someone allowed in from outside.
type Person struct {
	Email string `json:"email"`
	Name  string `json:"name"`
	// Everything: the whole app, as if they were on the local network.
	// Otherwise only Dashboards (e.g. their phone and their iPad layout).
	Everything bool       `json:"everything"`
	Dashboards []int64    `json:"dashboards"`
	LastSeen   *time.Time `json:"lastSeen,omitempty"`
}

// Config is remote access's settings (data/remote.json).
type Config struct {
	Enabled bool `json:"enabled"`
	// Port: the listener the tunnel connects to, on 127.0.0.1 only.
	Port int `json:"port"`
	// Link is the public address, e.g. https://live.example.com (shown in
	// Settings; the tunnel decides it).
	Link string `json:"link"`
	// Team is the Cloudflare Zero Trust team name (<team>.cloudflareaccess.com).
	Team string `json:"team"`
	// AUD is the Access application's audience tag.
	AUD    string   `json:"aud"`
	People []Person `json:"people"`
}

const DefaultPort = 18980

var (
	teamRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	audRe   = regexp.MustCompile(`^[a-f0-9]{32,128}$`)
	emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
)

// Normalize cleans a config and says what's wrong with it.
func (c *Config) Normalize() error {
	if c.Port == 0 {
		c.Port = DefaultPort
	}
	if c.Port < 1024 || c.Port > 65535 {
		return errors.New("the remote port must be 1024-65535")
	}
	c.Team = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(c.Team), ".cloudflareaccess.com")))
	c.AUD = strings.ToLower(strings.TrimSpace(c.AUD))
	c.Link = strings.TrimRight(strings.TrimSpace(c.Link), "/")
	if c.Link != "" && !strings.HasPrefix(c.Link, "https://") {
		return errors.New("the link must start with https://")
	}
	if c.Team != "" && !teamRe.MatchString(c.Team) {
		return errors.New("the Cloudflare team name is letters, numbers and dashes")
	}
	if c.AUD != "" && !audRe.MatchString(c.AUD) {
		return errors.New("the Access audience tag is a long hex string (Zero Trust → Access → Applications → the app → Overview)")
	}
	if c.Enabled && (c.Team == "" || c.AUD == "") {
		return errors.New("remote access needs the Cloudflare team name and the Access audience tag")
	}
	seen := map[string]bool{}
	for i := range c.People {
		p := &c.People[i]
		p.Email = strings.ToLower(strings.TrimSpace(p.Email))
		p.Name = strings.TrimSpace(p.Name)
		if !emailRe.MatchString(p.Email) {
			return fmt.Errorf("%q isn't an email address", p.Email)
		}
		if seen[p.Email] {
			return fmt.Errorf("%s is on the list twice", p.Email)
		}
		seen[p.Email] = true
		if !p.Everything && len(p.Dashboards) == 0 {
			return fmt.Errorf("pick what %s sees: a dashboard, or everything", p.Email)
		}
	}
	return nil
}

// Find is the person with this email, if listed.
func (c *Config) Find(email string) *Person {
	email = strings.ToLower(strings.TrimSpace(email))
	for i := range c.People {
		if c.People[i].Email == email {
			return &c.People[i]
		}
	}
	return nil
}

// Store keeps the config in a JSON file.
type Store struct {
	path string
	mu   sync.Mutex
	cfg  Config
}

func Open(path string) (*Store, error) {
	s := &Store{path: path, cfg: Config{Port: DefaultPort}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &s.cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if s.cfg.Port == 0 {
		s.cfg.Port = DefaultPort
	}
	return s, nil
}

func (s *Store) Get() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.cfg
	c.People = append([]Person{}, s.cfg.People...) // [] not null: nobody yet
	return c
}

func (s *Store) Set(c Config) error {
	if err := c.Normalize(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// keep when each person was last seen
	for i := range c.People {
		if old := s.cfg.Find(c.People[i].Email); old != nil && c.People[i].LastSeen == nil {
			c.People[i].LastSeen = old.LastSeen
		}
	}
	s.cfg = c
	return s.saveLocked()
}

// Seen notes that someone came in (at most once a minute to disk).
func (s *Store) Seen(email string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.cfg.Find(email)
	if p == nil {
		return
	}
	if p.LastSeen != nil && at.Sub(*p.LastSeen) < time.Minute {
		return
	}
	t := at.UTC()
	p.LastSeen = &t
	s.saveLocked()
}

func (s *Store) saveLocked() error {
	b, _ := json.MarshalIndent(s.cfg, "", "  ")
	tmp := s.path + ".tmp"
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// ------------------------------------------------------------- tokens

// Verifier checks Cloudflare Access tokens (RS256 JWTs) against the
// team's published keys, cached for an hour.
type Verifier struct {
	Client *http.Client
	Now    func() time.Time
	// CertsURL overrides https://<team>.cloudflareaccess.com/cdn-cgi/access/certs (tests).
	CertsURL func(team string) string

	mu   sync.Mutex
	keys map[string]map[string]*rsa.PublicKey // team → kid → key
	at   map[string]time.Time
}

var (
	ErrNoToken  = errors.New("no Cloudflare Access sign-in on this request")
	ErrBadToken = errors.New("the Cloudflare Access sign-in didn't check out")
)

// Claims is what jadeck reads from a token.
type Claims struct {
	Email string `json:"email"`
	Iss   string `json:"iss"`
	Aud   any    `json:"aud"`
	Exp   int64  `json:"exp"`
	Nbf   int64  `json:"nbf"`
	Type  string `json:"type"`
}

func (v *Verifier) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

// Verify returns the signed-in email, or why the token isn't good.
func (v *Verifier) Verify(ctx context.Context, token, team, aud string) (string, error) {
	if token == "" {
		return "", ErrNoToken
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", ErrBadToken
	}
	var hdr struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := decodeSeg(parts[0], &hdr); err != nil || hdr.Alg != "RS256" || hdr.Kid == "" {
		return "", ErrBadToken
	}
	key, err := v.key(ctx, team, hdr.Kid)
	if err != nil {
		return "", err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", ErrBadToken
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sig) != nil {
		return "", ErrBadToken
	}
	var c Claims
	if err := decodeSeg(parts[1], &c); err != nil {
		return "", ErrBadToken
	}
	now := v.now().Unix()
	if c.Exp == 0 || now > c.Exp+30 || (c.Nbf != 0 && now+30 < c.Nbf) {
		return "", fmt.Errorf("%w: expired", ErrBadToken)
	}
	if c.Iss != "https://"+team+".cloudflareaccess.com" {
		return "", fmt.Errorf("%w: wrong issuer", ErrBadToken)
	}
	if !hasAud(c.Aud, aud) {
		return "", fmt.Errorf("%w: wrong application", ErrBadToken)
	}
	if c.Email == "" {
		return "", fmt.Errorf("%w: no email (service tokens aren't people)", ErrBadToken)
	}
	return strings.ToLower(c.Email), nil
}

func hasAud(a any, want string) bool {
	switch v := a.(type) {
	case string:
		return v == want
	case []any:
		for _, x := range v {
			if s, _ := x.(string); s == want {
				return true
			}
		}
	}
	return false
}

func decodeSeg(seg string, out any) error {
	b, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

// key is the team's public key with this id, fetched when missing or
// older than an hour (a kid we don't know refetches at most every 30 s).
func (v *Verifier) key(ctx context.Context, team, kid string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	if v.keys == nil {
		v.keys, v.at = map[string]map[string]*rsa.PublicKey{}, map[string]time.Time{}
	}
	k := v.keys[team][kid]
	age := v.now().Sub(v.at[team])
	v.mu.Unlock()
	if k != nil && age < time.Hour {
		return k, nil
	}
	if k == nil && age < 30*time.Second {
		return nil, ErrBadToken
	}
	keys, err := v.fetch(ctx, team)
	if err != nil {
		if k != nil {
			return k, nil // Cloudflare unreachable: the cached key still checks signatures
		}
		return nil, err
	}
	v.mu.Lock()
	v.keys[team], v.at[team] = keys, v.now()
	v.mu.Unlock()
	if k = keys[kid]; k == nil {
		return nil, ErrBadToken
	}
	return k, nil
}

func (v *Verifier) fetch(ctx context.Context, team string) (map[string]*rsa.PublicKey, error) {
	url := "https://" + team + ".cloudflareaccess.com/cdn-cgi/access/certs"
	if v.CertsURL != nil {
		url = v.CertsURL(team)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	cl := v.Client
	if cl == nil {
		cl = http.DefaultClient
	}
	res, err := cl.Do(req)
	if err != nil {
		return nil, fmt.Errorf("can't read Cloudflare's sign-in keys: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("Cloudflare's sign-in keys: %s", res.Status)
	}
	var set struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(res.Body).Decode(&set); err != nil {
		return nil, err
	}
	out := map[string]*rsa.PublicKey{}
	for _, k := range set.Keys {
		if k.Kty != "RSA" {
			continue
		}
		n, err1 := base64.RawURLEncoding.DecodeString(k.N)
		e, err2 := base64.RawURLEncoding.DecodeString(k.E)
		if err1 != nil || err2 != nil || len(e) == 0 || len(e) > 4 {
			continue
		}
		out[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
	}
	if len(out) == 0 {
		return nil, errors.New("Cloudflare sent no sign-in keys")
	}
	return out, nil
}

// Sees reports whether p may open dashboard id.
func (p *Person) Sees(id int64) bool {
	if p.Everything {
		return true
	}
	for _, d := range p.Dashboards {
		if d == id {
			return true
		}
	}
	return false
}
