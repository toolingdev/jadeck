// Package dashboards stores custom dashboards. Each dashboard is made for
// one kind of screen (phone, tablet or desktop) and holds one grid layout
// for it. Every kind has a default dashboard, so a device that hasn't
// picked one always opens something; any device may show any dashboard.
package dashboards

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Kinds of screen. Columns: phone 4, tablet 12, desktop 12.
var Kinds = []string{"phone", "tablet", "desktop"}

func Cols(kind string) int {
	if kind == "phone" {
		return 4
	}
	return 12
}

func validKind(k string) bool {
	for _, x := range Kinds {
		if x == k {
			return true
		}
	}
	return false
}

type Widget struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Settings map[string]any `json:"settings"`
}

type Item struct {
	I string `json:"i"`
	X int    `json:"x"`
	Y int    `json:"y"`
	W int    `json:"w"`
	H int    `json:"h"`
}

type Dashboard struct {
	ID        int64    `json:"id"`
	Name      string   `json:"name"`
	Kind      string   `json:"kind"`
	Sort      int      `json:"sort"`
	Widgets   []Widget `json:"widgets"`
	Layout    []Item   `json:"layout"`
	UpdatedAt string   `json:"updatedAt"`
}

var (
	ErrNotFound = errors.New("dashboard not found")
	ErrInvalid  = errors.New("invalid dashboard")
)

const (
	maxWidgets     = 120
	maxSettingsLen = 16 << 10
	maxGrid        = 400
)

var (
	typeRe = regexp.MustCompile(`^[a-z][a-z0-9._:-]{0,79}$`) // add-on widgets: addon:<id>:<widget>
	idRe   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)
)

func bad(f string, a ...any) error { return fmt.Errorf("%w: "+f, append([]any{ErrInvalid}, a...)...) }

// Validate checks structure; widget types belong to the UI, so unknown
// types are kept (a newer UI may know them). Layout entries for missing
// widgets are dropped; widgets missing from the layout are placed at the
// bottom.
func Validate(d *Dashboard) error {
	d.Name = strings.TrimSpace(d.Name)
	if d.Name == "" || len([]rune(d.Name)) > 60 {
		return bad("name must be 1-60 characters")
	}
	if !validKind(d.Kind) {
		return bad("made for must be phone, tablet or desktop")
	}
	if len(d.Widgets) > maxWidgets {
		return bad("%d widgets; the limit is %d", len(d.Widgets), maxWidgets)
	}
	ids := map[string]bool{}
	for i := range d.Widgets {
		w := &d.Widgets[i]
		if !idRe.MatchString(w.ID) {
			return bad("widget %d: bad id %q", i+1, w.ID)
		}
		if ids[w.ID] {
			return bad("widget id %q used twice", w.ID)
		}
		ids[w.ID] = true
		if !typeRe.MatchString(w.Type) {
			return bad("widget %q: bad type %q", w.ID, w.Type)
		}
		if w.Settings == nil {
			w.Settings = map[string]any{}
		}
		if b, _ := json.Marshal(w.Settings); len(b) > maxSettingsLen {
			return bad("widget %q: settings too large", w.ID)
		}
	}
	cols := Cols(d.Kind)
	seen := map[string]bool{}
	kept := []Item{}
	for _, it := range d.Layout {
		if !ids[it.I] || seen[it.I] {
			continue
		}
		seen[it.I] = true
		if it.X < 0 || it.Y < 0 || it.W < 1 || it.H < 1 || it.Y > maxGrid*4 || it.H > maxGrid {
			return bad("widget %q: position out of range", it.I)
		}
		if it.W > cols {
			it.W = cols
		}
		if it.X+it.W > cols {
			it.X = cols - it.W
		}
		kept = append(kept, it)
	}
	bottom := 0
	for _, it := range kept {
		bottom = max(bottom, it.Y+it.H)
	}
	for _, w := range d.Widgets {
		if !seen[w.ID] {
			kept = append(kept, Item{I: w.ID, X: 0, Y: bottom, W: min(cols, 4), H: 3})
			bottom += 3
		}
	}
	d.Layout = kept
	return nil
}

// Reflow converts a layout between kinds: to a phone it stacks widgets in
// reading order at full width; from a phone it widens proportionally.
func Reflow(items []Item, from, to string) []Item {
	if Cols(from) == Cols(to) {
		return append([]Item(nil), items...)
	}
	sorted := append([]Item(nil), items...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Y != sorted[j].Y {
			return sorted[i].Y < sorted[j].Y
		}
		return sorted[i].X < sorted[j].X
	})
	out := make([]Item, 0, len(sorted))
	if to == "phone" {
		y := 0
		for _, it := range sorted {
			out = append(out, Item{I: it.I, X: 0, Y: y, W: 4, H: it.H})
			y += it.H
		}
		return out
	}
	f := Cols(to) / Cols(from)
	for _, it := range sorted {
		out = append(out, Item{I: it.I, X: it.X * f, Y: it.Y, W: it.W * f, H: it.H})
	}
	return out
}

// ---------------------------------------------------------------- store

type Store struct {
	mu sync.Mutex
	db *sql.DB
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS dashboards (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			sort INTEGER NOT NULL,
			widgets TEXT NOT NULL,
			layouts TEXT NOT NULL,
			updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS defaults (kind TEXT PRIMARY KEY, dashboard_id INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS seeds (name TEXT PRIMARY KEY)`,
	} {
		if _, err := db.Exec(q); err != nil {
			db.Close()
			return nil, err
		}
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM dashboards`).Scan(&n)
	if n == 0 {
		seed := Booth()
		if _, err := s.Create(&seed); err != nil {
			return nil, err
		}
	}
	if err := s.ensureDefaults(); err != nil {
		return nil, err
	}
	// After the defaults: a starter Stage dashboard never becomes a
	// device's default by surprise.
	if err := s.seedOnce("stage-v1", StageTablet, StagePhone); err != nil {
		return nil, err
	}
	// The Camera tab became a dashboard: same controls, editable.
	if err := s.seedOnce("camera-v1", CameraDesktop, CameraPhone); err != nil {
		return nil, err
	}
	if err := s.once("cue-v1", s.toCue); err != nil {
		return nil, err
	}
	return s, nil
}

// once runs a one-time upgrade of the saved dashboards.
func (s *Store) once(name string, run func() error) error {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM seeds WHERE name = ?`, name).Scan(&n)
	if n > 0 {
		return nil
	}
	if err := run(); err != nil {
		return fmt.Errorf("upgrading dashboards (%s): %w", name, err)
	}
	_, err := s.db.Exec(`INSERT INTO seeds (name) VALUES (?)`, name)
	return err
}

// toCue turns the old message widgets into Cue widgets. A big Message
// display plus a reply-pad sender on the same channel become one Cue
// (the cue big, with those replies under it) in the space both took; a
// typing sender becomes a sending Cue; a conversation view stays.
func (s *Store) toCue() error {
	list, err := s.List()
	if err != nil {
		return err
	}
	strs := func(v any) []any {
		if a, ok := v.([]any); ok {
			return a
		}
		return nil
	}
	for _, d := range list {
		changed := false
		at := map[string]int{}
		for i, it := range d.Layout {
			at[it.I] = i
		}
		drop := map[string]bool{}
		for wi := range d.Widgets {
			w := &d.Widgets[wi]
			switch w.Type {
			case "stage.show":
				if st, _ := w.Settings["style"].(string); st == "conversation" {
					continue
				}
				ch, _ := w.Settings["channel"].(string)
				cue := map[string]any{"channels": []any{ch}, "incoming": true, "outgoing": false}
				if e, ok := w.Settings["empty"].(string); ok {
					cue["empty"] = e
				}
				// a reply pad on the same channel: its buttons become the replies
				for pi, p := range d.Widgets {
					if p.Type != "stage.send" || p.Settings["textBox"] != false || drop[p.ID] {
						continue
					}
					chs := strs(p.Settings["channels"])
					if len(chs) != 1 || chs[0] != ch {
						continue
					}
					if q := strs(p.Settings["quick"]); len(q) > 0 {
						cue["replies"] = q
					}
					drop[d.Widgets[pi].ID] = true
					if a, ok := at[w.ID]; ok {
						if b, ok := at[p.ID]; ok {
							A, B := d.Layout[a], d.Layout[b]
							x0, y0 := min(A.X, B.X), min(A.Y, B.Y)
							x1, y1 := max(A.X+A.W, B.X+B.W), max(A.Y+A.H, B.Y+B.H)
							d.Layout[a] = Item{I: A.I, X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
						}
					}
					break
				}
				w.Type, w.Settings, changed = "stage.cue", cue, true
			case "stage.send":
				if w.Settings["textBox"] == false {
					continue // handled with its display; a lone reply pad keeps sending
				}
				cue := map[string]any{"channels": w.Settings["channels"], "incoming": false, "outgoing": true, "textBox": true}
				for _, k := range []string{"quick", "timers"} {
					if v, ok := w.Settings[k]; ok {
						cue[k] = v
					}
				}
				w.Type, w.Settings, changed = "stage.cue", cue, true
			}
		}
		for wi := range d.Widgets {
			w := &d.Widgets[wi]
			if w.Type == "stage.send" && !drop[w.ID] && w.Settings["textBox"] == false {
				// a reply pad with no display: a sending Cue with those buttons
				w.Type = "stage.cue"
				q := w.Settings["quick"]
				if q == nil {
					q = []any{"👍 Got it", "Ready", "Mic problem", "+2 min"}
				}
				w.Settings = map[string]any{"channels": w.Settings["channels"], "incoming": false, "outgoing": true, "quick": q}
				changed = true
			}
		}
		if len(drop) > 0 {
			ws, ls := d.Widgets[:0], d.Layout[:0]
			for _, w := range d.Widgets {
				if !drop[w.ID] {
					ws = append(ws, w)
				}
			}
			for _, it := range d.Layout {
				if !drop[it.I] {
					ls = append(ls, it)
				}
			}
			d.Widgets, d.Layout = ws, ls
		}
		if changed {
			if _, err := s.Update(d.ID, &d); err != nil {
				return err
			}
		}
	}
	return nil
}

// seedOnce adds starter dashboards that came with a later version, once:
// deleting them afterwards is respected.
func (s *Store) seedOnce(name string, make ...func() Dashboard) error {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM seeds WHERE name = ?`, name).Scan(&n)
	if n > 0 {
		return nil
	}
	for _, f := range make {
		d := f()
		if _, err := s.Create(&d); err != nil {
			return fmt.Errorf("starter dashboard %q: %w", d.Name, err)
		}
	}
	_, err := s.db.Exec(`INSERT INTO seeds (name) VALUES (?)`, name)
	return err
}

// migrate adds kind/layout to stores from the multi-layout version: an
// old dashboard becomes a tablet dashboard using its tablet layout.
func (s *Store) migrate() error {
	cols := map[string]bool{}
	rows, err := s.db.Query(`PRAGMA table_info(dashboards)`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk)
		cols[name] = true
	}
	rows.Close()
	if !cols["kind"] {
		if _, err := s.db.Exec(`ALTER TABLE dashboards ADD COLUMN kind TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	if !cols["layout"] {
		if _, err := s.db.Exec(`ALTER TABLE dashboards ADD COLUMN layout TEXT NOT NULL DEFAULT '[]'`); err != nil {
			return err
		}
	}
	type old struct {
		id      int64
		layouts string
	}
	var todo []old
	r2, err := s.db.Query(`SELECT id, layouts FROM dashboards WHERE kind = ''`)
	if err != nil {
		return err
	}
	for r2.Next() {
		var o old
		r2.Scan(&o.id, &o.layouts)
		todo = append(todo, o)
	}
	r2.Close()
	for _, o := range todo {
		var ls map[string][]Item
		json.Unmarshal([]byte(o.layouts), &ls)
		l := ls["tablet"]
		if len(l) == 0 {
			l = Reflow(ls["desktop"], "desktop", "tablet")
		}
		if len(l) == 0 {
			l = Reflow(ls["phone"], "phone", "tablet")
		}
		b, _ := json.Marshal(l)
		if _, err := s.db.Exec(`UPDATE dashboards SET kind='tablet', layout=? WHERE id=?`, string(b), o.id); err != nil {
			return err
		}
	}
	return nil
}

// ensureDefaults gives every kind a default (the first dashboard made
// for that kind, else the first dashboard).
func (s *Store) ensureDefaults() error {
	l, err := s.List()
	if err != nil || len(l) == 0 {
		return err
	}
	cur, err := s.Defaults()
	if err != nil {
		return err
	}
	exists := map[int64]bool{}
	for _, d := range l {
		exists[d.ID] = true
	}
	for _, k := range Kinds {
		if id, ok := cur[k]; ok && exists[id] {
			continue
		}
		pick := l[0].ID
		for _, d := range l {
			if d.Kind == k {
				pick = d.ID
				break
			}
		}
		if err := s.SetDefault(k, pick); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

func stamp() string { return time.Now().UTC().Format(time.RFC3339) }

type scanner interface{ Scan(...any) error }

func scan(r scanner) (*Dashboard, error) {
	var d Dashboard
	var w, l string
	if err := r.Scan(&d.ID, &d.Name, &d.Kind, &d.Sort, &w, &l, &d.UpdatedAt); err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(w), &d.Widgets)
	json.Unmarshal([]byte(l), &d.Layout)
	if d.Widgets == nil {
		d.Widgets = []Widget{}
	}
	if d.Layout == nil {
		d.Layout = []Item{}
	}
	return &d, nil
}

const cols = `id, name, kind, sort, widgets, layout, updated_at`

func (s *Store) List() ([]Dashboard, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT ` + cols + ` FROM dashboards ORDER BY sort, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Dashboard{}
	for rows.Next() {
		d, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

func (s *Store) Get(id int64) (*Dashboard, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := scan(s.db.QueryRow(`SELECT `+cols+` FROM dashboards WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

func (s *Store) Create(d *Dashboard) (*Dashboard, error) {
	if err := Validate(d); err != nil {
		return nil, err
	}
	w, _ := json.Marshal(d.Widgets)
	l, _ := json.Marshal(d.Layout)
	s.mu.Lock()
	res, err := s.db.Exec(`INSERT INTO dashboards (name, kind, sort, widgets, layout, layouts, updated_at)
		VALUES (?, ?, (SELECT COALESCE(MAX(sort), -1) + 1 FROM dashboards), ?, ?, '{}', ?)`, d.Name, d.Kind, string(w), string(l), stamp())
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.Get(id)
}

func (s *Store) Update(id int64, d *Dashboard) (*Dashboard, error) {
	cur, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	if d.Kind == "" {
		d.Kind = cur.Kind
	}
	if d.Kind != cur.Kind {
		d.Layout = Reflow(d.Layout, cur.Kind, d.Kind)
	}
	if err := Validate(d); err != nil {
		return nil, err
	}
	w, _ := json.Marshal(d.Widgets)
	l, _ := json.Marshal(d.Layout)
	s.mu.Lock()
	_, err = s.db.Exec(`UPDATE dashboards SET name=?, kind=?, widgets=?, layout=?, updated_at=? WHERE id=?`, d.Name, d.Kind, string(w), string(l), stamp(), id)
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return s.Get(id)
}

// Duplicate copies a dashboard, optionally for another kind (reflowed).
func (s *Store) Duplicate(id int64, name, kind string) (*Dashboard, error) {
	src, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(name) == "" {
		name = src.Name + " copy"
	}
	if kind == "" {
		kind = src.Kind
	}
	d := &Dashboard{Name: name, Kind: kind, Widgets: src.Widgets, Layout: Reflow(src.Layout, src.Kind, kind)}
	return s.Create(d)
}

func (s *Store) Delete(id int64) error {
	defs, err := s.Defaults()
	if err != nil {
		return err
	}
	for k, did := range defs {
		if did == id {
			return bad("it's the default for %ss — pick another default first", k)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM dashboards`).Scan(&n)
	if n <= 1 {
		return bad("keep at least one dashboard")
	}
	res, err := s.db.Exec(`DELETE FROM dashboards WHERE id=?`, id)
	if err != nil {
		return err
	}
	if k, _ := res.RowsAffected(); k == 0 {
		return ErrNotFound
	}
	return nil
}

// Defaults maps kind → default dashboard id.
func (s *Store) Defaults() (map[string]int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT kind, dashboard_id FROM defaults`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var k string
		var id int64
		rows.Scan(&k, &id)
		out[k] = id
	}
	return out, rows.Err()
}

func (s *Store) SetDefault(kind string, id int64) error {
	if !validKind(kind) {
		return bad("unknown kind %q", kind)
	}
	if _, err := s.Get(id); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO defaults (kind, dashboard_id) VALUES (?, ?)
		ON CONFLICT(kind) DO UPDATE SET dashboard_id=excluded.dashboard_id`, kind, id)
	return err
}

// Booth is the starter dashboard (a tablet dashboard, like the old fixed
// Dashboard page).
func Booth() Dashboard {
	ws := []Widget{
		{"program", "switcher.program", map[string]any{}},
		{"confidence", "switcher.confidence", map[string]any{}},
		{"inputs", "switcher.inputs", map[string]any{}},
		{"cutfade", "switcher.cutfade", map[string]any{}},
		{"slides", "slides.prevnext", map[string]any{}},
		{"camlive", "camera.live", map[string]any{"quality": "sd"}},
		{"move", "camera.move", map[string]any{"controls": "switchable", "opensIn": "buttons", "home": true, "zoom": true, "speed": true}},
		{"presets", "camera.presets", map[string]any{}},
		{"favorites", "audio.favorites", map[string]any{}},
		{"macros", "macros.list", map[string]any{}},
		{"log", "other.log", map[string]any{}},
		{"stage", "stage.cue", map[string]any{"channels": []any{"pulpit"}, "incoming": false, "outgoing": true, "textBox": true}},
		{"replies", "stage.cue", map[string]any{"channels": []any{"worship"}, "incoming": false, "outgoing": true, "textBox": true}},
	}
	layout := []Item{
		{"program", 0, 0, 7, 8}, {"confidence", 7, 0, 5, 6},
		{"inputs", 7, 6, 5, 4}, {"cutfade", 0, 8, 4, 3}, {"slides", 4, 8, 3, 3},
		{"camlive", 0, 11, 4, 5}, {"move", 4, 11, 4, 9}, {"presets", 8, 11, 4, 6},
		{"favorites", 0, 20, 8, 5}, {"macros", 8, 17, 4, 8}, {"log", 0, 25, 12, 5},
		{"stage", 0, 30, 6, 9}, {"replies", 6, 30, 6, 9},
	}
	return Dashboard{Name: "Booth", Kind: "tablet", Widgets: ws, Layout: layout}
}

// StageTablet is the pulpit iPad: live status, the booth's message big,
// the stream, quick replies. Lock it from the dashboard menu.
func StageTablet() Dashboard {
	ws := []Widget{
		{"live", "stage.live", map[string]any{"mode": "stream"}},
		{"timer", "stage.timer", map[string]any{"channel": "pulpit"}},
		{"msg", "stage.cue", map[string]any{"channels": []any{"pulpit"}, "incoming": true, "outgoing": false}},
		{"stream", "switcher.program", map[string]any{}},
		{"cam", "camera.live", map[string]any{"quality": "sd", "tally": true}},
	}
	layout := []Item{
		{"live", 0, 0, 8, 2}, {"timer", 8, 0, 4, 2},
		{"msg", 0, 2, 8, 13}, {"stream", 8, 2, 4, 5}, {"cam", 8, 7, 4, 5},
	}
	return Dashboard{Name: "Stage — iPad", Kind: "tablet", Widgets: ws, Layout: layout}
}

// StagePhone is the worship leader's phone.
func StagePhone() Dashboard {
	ws := []Widget{
		{"live", "stage.live", map[string]any{"mode": "stream"}},
		{"timer", "stage.timer", map[string]any{"channel": "worship"}},
		{"msg", "stage.cue", map[string]any{"channels": []any{"worship"}, "incoming": true, "outgoing": false}},
		{"stream", "switcher.program", map[string]any{}},
	}
	layout := []Item{
		{"live", 0, 0, 4, 2}, {"timer", 0, 2, 4, 2}, {"msg", 0, 4, 4, 10},
		{"stream", 0, 14, 4, 5},
	}
	return Dashboard{Name: "Stage — phone", Kind: "phone", Widgets: ws, Layout: layout}
}

// CameraDesktop is what the Camera tab was: the picture, Move, presets
// and picture settings, for the camera picked at the top.
func CameraDesktop() Dashboard {
	ws := []Widget{
		{"video", "video", map[string]any{"feed": "cam:", "quality": "auto", "tapToControl": false}},
		{"move", "camera.move", map[string]any{}},
		{"presets", "camera.presets", map[string]any{}},
		{"picture", "camera.picture", map[string]any{}},
	}
	layout := []Item{{"video", 0, 0, 8, 10}, {"move", 8, 0, 4, 10}, {"presets", 0, 10, 8, 7}, {"picture", 8, 10, 4, 9}}
	return Dashboard{Name: "Camera", Kind: "desktop", Widgets: ws, Layout: layout}
}

// CameraPhone is the Camera tab on a phone.
func CameraPhone() Dashboard {
	ws := []Widget{
		{"video", "video", map[string]any{"feed": "cam:", "quality": "auto", "tapToControl": false}},
		{"move", "camera.move", map[string]any{}},
		{"presets", "camera.presets", map[string]any{}},
	}
	layout := []Item{{"video", 0, 0, 4, 5}, {"move", 0, 5, 4, 9}, {"presets", 0, 14, 4, 8}}
	return Dashboard{Name: "Camera — phone", Kind: "phone", Widgets: ws, Layout: layout}
}
