package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Scope is one toggleable permission.
type Scope struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Desc string `json:"description"`
}

// Scopes is the full permission list, in display order.
var Scopes = []Scope{
	{"camera.read", "View camera", "Status, position, snapshot, presets list, image settings (read-only)."},
	{"camera.move", "Move camera", "Pan / tilt / zoom, home, stop, focus, recall presets."},
	{"camera.image", "Adjust picture", "Exposure, white balance, image settings; white-point pick (moves the camera briefly)."},
	{"presets.edit", "Edit presets", "Save, rename, reorder and delete presets."},
	{"switcher", "Video switcher", "Read switcher state; preview, cut, fade."},
	{"slides", "Slides", "Send presentation actions (next, prev, black, …)."},
	{"mixer", "Audio mutes", "Read and change channel mutes on the mixer."},
	{"macros.run", "Run macros", "List and run macros."},
	{"macros.edit", "Edit macros", "Create, change, import and delete macros."},
	{"stage", "Stage messages", "Read and send stage messages, replies and timers."},
	{"schedule", "Schedule actions", "See, add and cancel scheduled actions (each step still needs its own permission)."},
	{"feeds", "Manage video feeds", "Add, change and remove video feeds and see their sender keys. An add-on can change only the feeds it added."},
	{"addons", "Add-on actions", "Read add-on state and run add-on actions (e.g. Proclaim service items)."},
	{"addons.admin", "Manage add-ons", "List add-ons and connections, read add-on logs, load an add-on folder in dev mode, restart add-ons (for agents that build add-ons)."},
	{"update.apply", "Apply updates", "Check for and apply signed app updates."},
}

func ValidScope(id string) bool {
	for _, s := range Scopes {
		if s.ID == id {
			return true
		}
	}
	return false
}

// Key is a stored key (never the secret).
type Key struct {
	ID         int64    `json:"id"`
	Name       string   `json:"name"`
	Prefix     string   `json:"prefix"` // shown as ptz_<prefix>…
	Scopes     []string `json:"scopes"`
	Enabled    bool     `json:"enabled"`
	CreatedAt  string   `json:"createdAt"`
	LastUsedAt *string  `json:"lastUsedAt"`
	LastIP     *string  `json:"lastIp"`
	RevokedAt  *string  `json:"revokedAt"`
	// Conn is the add-on connection a Controller add-on's token belongs
	// to ("" for API keys).
	Conn string `json:"-"`
}

// Active is enabled and not revoked.
func (k *Key) Active() bool { return k.Enabled && k.RevokedAt == nil }

func (k *Key) Has(scope string) bool {
	for _, s := range k.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

type AuditEntry struct {
	ID      int64  `json:"id"`
	At      string `json:"at"`
	KeyID   int64  `json:"keyId"`
	KeyName string `json:"keyName"`
	IP      string `json:"ip"`
	Action  string `json:"action"`
	Status  string `json:"status"`
}

var (
	ErrNotFound = errors.New("key not found")
	ErrInput    = errors.New("invalid key request")
)

type Store struct {
	mu       sync.Mutex
	db       *sql.DB
	lastSeen map[int64]time.Time
}

func OpenStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS keys (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			prefix TEXT NOT NULL,
			hash TEXT NOT NULL UNIQUE,
			scopes TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL,
			last_used_at TEXT,
			last_ip TEXT,
			revoked_at TEXT)`,
		`CREATE TABLE IF NOT EXISTS audit (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			at TEXT NOT NULL,
			key_id INTEGER NOT NULL,
			key_name TEXT NOT NULL,
			ip TEXT NOT NULL,
			action TEXT NOT NULL,
			status TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS audit_at ON audit(id DESC)`,
	} {
		if _, err := db.Exec(q); err != nil {
			db.Close()
			return nil, err
		}
	}
	return &Store{db: db, lastSeen: map[int64]time.Time{}}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func hashSecret(secret string) string {
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:])
}

func stamp() string { return time.Now().UTC().Format(time.RFC3339) }

func cleanScopes(in []string) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	for _, sc := range in {
		if !ValidScope(sc) {
			return nil, fmt.Errorf("%w: unknown scope %q", ErrInput, sc)
		}
		if !seen[sc] {
			seen[sc] = true
			out = append(out, sc)
		}
	}
	sort.Strings(out)
	return out, nil
}

// Create makes a key and returns it with its secret. The secret is
// shown once; only its SHA-256 is stored.
func (s *Store) Create(name string, scopes []string) (*Key, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 60 {
		return nil, "", fmt.Errorf("%w: name must be 1-60 characters", ErrInput)
	}
	sc, err := cleanScopes(scopes)
	if err != nil {
		return nil, "", err
	}
	b := make([]byte, 30)
	if _, err := rand.Read(b); err != nil {
		return nil, "", err
	}
	body := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
	secret := "ptz_" + body
	scj, _ := json.Marshal(sc)
	s.mu.Lock()
	res, err := s.db.Exec(`INSERT INTO keys (name, prefix, hash, scopes, created_at) VALUES (?,?,?,?,?)`,
		name, body[:8], hashSecret(secret), string(scj), stamp())
	s.mu.Unlock()
	if err != nil {
		return nil, "", err
	}
	id, _ := res.LastInsertId()
	k, err := s.Get(id)
	return k, secret, err
}

const kcols = `id, name, prefix, scopes, enabled, created_at, last_used_at, last_ip, revoked_at`

type scanner interface{ Scan(...any) error }

func scanKey(r scanner) (*Key, error) {
	var k Key
	var sc string
	var en int
	var lu, ip, rv sql.NullString
	if err := r.Scan(&k.ID, &k.Name, &k.Prefix, &sc, &en, &k.CreatedAt, &lu, &ip, &rv); err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(sc), &k.Scopes)
	if k.Scopes == nil {
		k.Scopes = []string{}
	}
	k.Enabled = en != 0
	if lu.Valid {
		k.LastUsedAt = &lu.String
	}
	if ip.Valid {
		k.LastIP = &ip.String
	}
	if rv.Valid {
		k.RevokedAt = &rv.String
	}
	return &k, nil
}

func (s *Store) Get(id int64) (*Key, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, err := scanKey(s.db.QueryRow(`SELECT `+kcols+` FROM keys WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return k, err
}

func (s *Store) List() ([]Key, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT ` + kcols + ` FROM keys ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Key{}
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *k)
	}
	return out, rows.Err()
}

// Lookup finds the key for a presented secret. Inactive keys are
// returned too (callers check Active) so the reason can be precise.
func (s *Store) Lookup(secret string) (*Key, error) {
	if !strings.HasPrefix(secret, "ptz_") {
		return nil, ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k, err := scanKey(s.db.QueryRow(`SELECT `+kcols+` FROM keys WHERE hash=?`, hashSecret(secret)))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return k, err
}

// Update changes name, scopes and/or enabled. Revoked keys can't be
// re-enabled.
func (s *Store) Update(id int64, name *string, scopes []string, setScopes bool, enabled *bool) (*Key, error) {
	k, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	if k.RevokedAt != nil && enabled != nil && *enabled {
		return nil, fmt.Errorf("%w: a revoked key can't be re-enabled — create a new one", ErrInput)
	}
	var scj []byte
	if setScopes {
		sc, err := cleanScopes(scopes)
		if err != nil {
			return nil, err
		}
		scj, _ = json.Marshal(sc)
	}
	var n string
	if name != nil {
		n = strings.TrimSpace(*name)
		if n == "" || len(n) > 60 {
			return nil, fmt.Errorf("%w: name must be 1-60 characters", ErrInput)
		}
	}
	err = func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if name != nil {
			if _, err := tx.Exec(`UPDATE keys SET name=? WHERE id=?`, n, id); err != nil {
				return err
			}
		}
		if setScopes {
			if _, err := tx.Exec(`UPDATE keys SET scopes=? WHERE id=?`, string(scj), id); err != nil {
				return err
			}
		}
		if enabled != nil {
			v := 0
			if *enabled {
				v = 1
			}
			if _, err := tx.Exec(`UPDATE keys SET enabled=? WHERE id=?`, v, id); err != nil {
				return err
			}
		}
		return tx.Commit()
	}()
	if err != nil {
		return nil, err
	}
	return s.Get(id)
}

// Revoke permanently disables a key (kept for the audit trail).
func (s *Store) Revoke(id int64) (*Key, error) {
	s.mu.Lock()
	res, err := s.db.Exec(`UPDATE keys SET revoked_at=COALESCE(revoked_at, ?), enabled=0 WHERE id=?`, stamp(), id)
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return s.Get(id)
}

// Delete removes a key entirely (its audit rows stay).
func (s *Store) Delete(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`DELETE FROM keys WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Touch records use (throttled to once per 30 s per key).
func (s *Store) Touch(k *Key, ip string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Since(s.lastSeen[k.ID]) < 30*time.Second {
		return
	}
	s.lastSeen[k.ID] = time.Now()
	s.db.Exec(`UPDATE keys SET last_used_at=?, last_ip=? WHERE id=?`, stamp(), ip, k.ID)
}

// Audit appends one entry and trims the log to the newest 5000.
func (s *Store) Audit(k *Key, ip, action, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.db.Exec(`INSERT INTO audit (at, key_id, key_name, ip, action, status) VALUES (?,?,?,?,?,?)`,
		stamp(), k.ID, k.Name, ip, action, status)
	s.db.Exec(`DELETE FROM audit WHERE id <= (SELECT id FROM audit ORDER BY id DESC LIMIT 1 OFFSET 5000)`)
}

func (s *Store) AuditLog(limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT id, at, key_id, key_name, ip, action, status FROM audit ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.At, &e.KeyID, &e.KeyName, &e.IP, &e.Action, &e.Status); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
