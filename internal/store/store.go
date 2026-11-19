// Package store keeps preset names, service order and exposure in
// SQLite. Preset *positions* live in the camera's VISCA slots; this
// store only maps slots to names. Same schema as the Python build, so
// an existing presets.db opens unchanged.
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"
)

var SeedPresets = []string{"Main", "Whole Venue", "Specials", "Piano",
	"Special Piano", "Band", "Whole TV View", "Announcements"}

var ErrSlotsFull = errors.New("all 127 preset slots are in use")

type Preset struct {
	ID       int64          `json:"id"`
	Name     string         `json:"name"`
	Slot     int            `json:"slot"`
	Sort     int            `json:"sort"`
	Exposure map[string]any `json:"exposure"`
}

type Store struct {
	mu sync.Mutex
	db *sql.DB
}

// Open opens (and seeds, when empty) the preset store.
func Open(path string) (*Store, error) { return OpenSeeded(path, true) }

// OpenSeeded opens path; seed=false leaves a new store empty (a second
// camera starts with no presets).
func OpenSeeded(path string, seed bool) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS presets (
		id       INTEGER PRIMARY KEY AUTOINCREMENT,
		name     TEXT NOT NULL,
		slot     INTEGER NOT NULL UNIQUE,
		sort     INTEGER NOT NULL,
		exposure TEXT)`); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM presets`).Scan(&n); err != nil {
		return nil, err
	}
	if n == 0 && seed {
		for i, name := range SeedPresets {
			if _, err := db.Exec(`INSERT INTO presets (name, slot, sort) VALUES (?,?,?)`, name, i+1, i); err != nil {
				return nil, err
			}
		}
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

type scanner interface{ Scan(...any) error }

func scanPreset(r scanner) (*Preset, error) {
	var p Preset
	var exp sql.NullString
	if err := r.Scan(&p.ID, &p.Name, &p.Slot, &p.Sort, &exp); err != nil {
		return nil, err
	}
	if exp.Valid && exp.String != "" {
		_ = json.Unmarshal([]byte(exp.String), &p.Exposure)
	}
	return &p, nil
}

const cols = `id, name, slot, sort, exposure`

func (s *Store) List() ([]Preset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listLocked()
}

func (s *Store) listLocked() ([]Preset, error) {
	rows, err := s.db.Query(`SELECT ` + cols + ` FROM presets ORDER BY sort, slot`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Preset{}
	for rows.Next() {
		p, err := scanPreset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// Get returns nil, nil when the preset doesn't exist.
func (s *Store) Get(id int64) (*Preset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getLocked(id)
}

func (s *Store) getLocked(id int64) (*Preset, error) {
	p, err := scanPreset(s.db.QueryRow(`SELECT `+cols+` FROM presets WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

// Create adds a preset; slot 0 means "next free slot".
func (s *Store) Create(name string, slot int) (*Preset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if slot == 0 {
		used := map[int]bool{}
		all, err := s.listLocked()
		if err != nil {
			return nil, err
		}
		for _, p := range all {
			used[p.Slot] = true
		}
		for c := 1; c <= 127; c++ {
			if !used[c] {
				slot = c
				break
			}
		}
		if slot == 0 {
			return nil, ErrSlotsFull
		}
	}
	res, err := s.db.Exec(`INSERT INTO presets (name, slot, sort)
		VALUES (?, ?, (SELECT COALESCE(MAX(sort), -1) + 1 FROM presets))`, name, slot)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.getLocked(id)
}

// Update renames and/or sets exposure (setExposure with nil clears it).
func (s *Store) Update(id int64, name *string, exposure map[string]any, setExposure bool) (*Preset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if name != nil {
		if _, err := tx.Exec(`UPDATE presets SET name=? WHERE id=?`, *name, id); err != nil {
			return nil, err
		}
	}
	if setExposure {
		var v any
		if len(exposure) > 0 {
			b, _ := json.Marshal(exposure)
			v = string(b)
		}
		if _, err := tx.Exec(`UPDATE presets SET exposure=? WHERE id=?`, v, id); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.getLocked(id)
}

func (s *Store) Delete(id int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`DELETE FROM presets WHERE id=?`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *Store) Reorder(ids []int64) ([]Preset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for i, id := range ids {
		if _, err := tx.Exec(`UPDATE presets SET sort=? WHERE id=?`, i, id); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.listLocked()
}
