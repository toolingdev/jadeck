package dashboards

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestStore(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	l, _ := s.List()
	if len(l) != 5 || l[0].Name != "Booth" || l[0].Kind != "tablet" || len(l[0].Layout) != len(l[0].Widgets) || l[3].Name != "Camera" {
		t.Fatalf("seed: %+v", l)
	}
	defs, _ := s.Defaults()
	if defs["phone"] != l[0].ID || defs["tablet"] != l[0].ID || defs["desktop"] != l[0].ID {
		t.Fatalf("defaults: %v", defs)
	}
	ph, err := s.Create(&Dashboard{Name: " Audio volunteer ", Kind: "phone",
		Widgets: []Widget{{ID: "m1", Type: "audio.mute"}, {ID: "m2", Type: "other.clock"}},
		Layout:  []Item{{I: "m1", W: 9, H: 3}, {I: "ghost", W: 1, H: 1}}})
	if err != nil || ph.Name != "Audio volunteer" || len(ph.Layout) != 2 || ph.Layout[0].W != 4 {
		t.Fatalf("%+v %v", ph, err) // width clamped to 4 cols; missing widget placed
	}
	for _, b := range []*Dashboard{{Name: "", Kind: "phone"}, {Name: "x", Kind: "watch"}, {Name: "x", Kind: "phone", Widgets: []Widget{{ID: "a", Type: "Bad"}}}} {
		if _, err := s.Create(b); !errors.Is(err, ErrInvalid) {
			t.Errorf("accepted %+v", b)
		}
	}
	if err := s.SetDefault("phone", ph.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ph.ID); !errors.Is(err, ErrInvalid) {
		t.Fatal("deleted a default")
	}
	// Duplicate a tablet dashboard as a phone one: stacked full width.
	cp, err := s.Duplicate(l[0].ID, "", "phone")
	if err != nil || cp.Kind != "phone" || cp.Name != "Booth copy" {
		t.Fatalf("%+v %v", cp, err)
	}
	for i, it := range cp.Layout {
		if it.W != 4 || it.X != 0 || (i > 0 && it.Y < cp.Layout[i-1].Y+cp.Layout[i-1].H) {
			t.Fatalf("not stacked: %+v", cp.Layout)
		}
	}
	if err := s.Delete(cp.ID); err != nil {
		t.Fatal(err)
	}
	s.SetDefault("phone", l[0].ID)
	if err := s.Delete(ph.ID); err != nil {
		t.Fatal(err)
	}
}

func TestMigratesOldMultiLayoutStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, _ := sql.Open("sqlite", path)
	db.Exec(`CREATE TABLE dashboards (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, sort INTEGER NOT NULL, widgets TEXT NOT NULL, layouts TEXT NOT NULL, updated_at TEXT NOT NULL)`)
	db.Exec(`INSERT INTO dashboards (name, sort, widgets, layouts, updated_at) VALUES ('Old', 0, '[{"id":"a","type":"other.clock","settings":{}}]', '{"tablet":[{"i":"a","x":3,"y":1,"w":5,"h":4}],"phone":[{"i":"a","x":0,"y":0,"w":4,"h":3}]}', 'x')`)
	db.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	l, _ := s.List()
	if len(l) != 5 || l[1].Name != "Stage — iPad" || l[2].Name != "Stage — phone" || l[3].Name != "Camera" {
		t.Fatalf("stage starters not added once: %+v", l)
	}
	if l[0].Kind != "tablet" || l[0].Layout[0].X != 3 || l[0].Layout[0].W != 5 {
		t.Fatalf("%+v", l)
	}
	if d, _ := s.Defaults(); d["phone"] != l[0].ID {
		t.Fatalf("defaults %v", d)
	}
}

func TestStageStartersSeededOnceAndNotDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "d.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	l, _ := s.List()
	if len(l) != 5 {
		t.Fatalf("fresh store: %d dashboards", len(l))
	}
	d, _ := s.Defaults()
	for k, id := range d {
		if id != l[0].ID {
			t.Fatalf("%s default is %d, want Booth", k, id)
		}
	}
	s.Delete(l[2].ID)
	s.Close()
	s, _ = Open(path)
	defer s.Close()
	if l, _ := s.List(); len(l) != 4 {
		t.Fatalf("deleted starter came back: %d", len(l))
	}
}
