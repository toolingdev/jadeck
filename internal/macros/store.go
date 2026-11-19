package macros

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Store keeps macros in their own SQLite file (never the preset DB).
// Same schema as the Python build, so an existing macros.db opens as-is.
type Store struct {
	mu sync.Mutex
	db *sql.DB
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
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS macros (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		name        TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		steps       TEXT NOT NULL,
		sort        INTEGER NOT NULL,
		last_run    TEXT,
		created_at  REAL NOT NULL,
		updated_at  REAL NOT NULL)`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func now() float64 { return float64(time.Now().UnixNano()) / 1e9 }

const mcols = `id, name, description, steps, sort, last_run, created_at, updated_at`

type scanner interface{ Scan(...any) error }

func scanMacro(r scanner) (*Macro, error) {
	var m Macro
	var steps string
	var last sql.NullString
	if err := r.Scan(&m.ID, &m.Name, &m.Description, &steps, &m.Sort, &last, &m.CreatedAt, &m.UpdatedAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(steps), &m.Steps); err != nil {
		return nil, fmt.Errorf("macro %d: bad steps JSON: %w", m.ID, err)
	}
	for i := range m.Steps {
		if m.Steps[i].Type == "wing_mute" { // Python-era macros
			m.Steps[i].Type = "mixer_mute"
		}
	}
	if last.Valid && last.String != "" {
		json.Unmarshal([]byte(last.String), &m.LastRun)
	}
	return &m, nil
}

func (s *Store) List() ([]Macro, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listLocked()
}

func (s *Store) listLocked() ([]Macro, error) {
	rows, err := s.db.Query(`SELECT ` + mcols + ` FROM macros ORDER BY sort, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Macro{}
	for rows.Next() {
		m, err := scanMacro(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// Get returns nil, nil when absent.
func (s *Store) Get(id int64) (*Macro, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getLocked(id)
}

func (s *Store) getLocked(id int64) (*Macro, error) {
	m, err := scanMacro(s.db.QueryRow(`SELECT `+mcols+` FROM macros WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return m, err
}

// ByName finds a macro by case-insensitive name (nil if none).
func (s *Store) ByName(name string) (*Macro, error) {
	l, err := s.List()
	if err != nil {
		return nil, err
	}
	for _, m := range l {
		if strings.EqualFold(m.Name, strings.TrimSpace(name)) {
			mm := m
			return &mm, nil
		}
	}
	return nil, nil
}

func (s *Store) Create(m *Macro) (*Macro, error) {
	if c, _ := s.ByName(m.Name); c != nil {
		return nil, fmt.Errorf("%w: a macro named %q already exists", ErrConflict, m.Name)
	}
	steps, _ := json.Marshal(m.Steps)
	s.mu.Lock()
	defer s.mu.Unlock()
	t := now()
	res, err := s.db.Exec(`INSERT INTO macros (name, description, steps, sort, created_at, updated_at)
		VALUES (?, ?, ?, (SELECT COALESCE(MAX(sort), -1) + 1 FROM macros), ?, ?)`,
		m.Name, m.Description, string(steps), t, t)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.getLocked(id)
}

func (s *Store) Update(id int64, m *Macro) (*Macro, error) {
	if c, _ := s.ByName(m.Name); c != nil && c.ID != id {
		return nil, fmt.Errorf("%w: a macro named %q already exists", ErrConflict, m.Name)
	}
	steps, _ := json.Marshal(m.Steps)
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`UPDATE macros SET name=?, description=?, steps=?, updated_at=? WHERE id=?`,
		m.Name, m.Description, string(steps), now(), id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, nil
	}
	return s.getLocked(id)
}

func (s *Store) Delete(id int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`DELETE FROM macros WHERE id=?`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *Store) RecordRun(id int64, report *Report) error {
	b, _ := json.Marshal(report)
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE macros SET last_run=? WHERE id=?`, string(b), id)
	return err
}
