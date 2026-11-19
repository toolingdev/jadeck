package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStore(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	l, _ := s.List()
	if len(l) != 8 || l[0].Name != "Main" || l[7].Slot != 8 {
		t.Fatalf("seed: %+v", l)
	}
	p, err := s.Create("Pulpit", 0)
	if err != nil || p.Slot != 9 || p.Sort != 8 {
		t.Fatalf("create: %+v %v", p, err)
	}
	if _, err := s.Create("Dup", 9); err == nil {
		t.Fatal("duplicate slot accepted")
	}
	n := "Pulpit Wide"
	p, _ = s.Update(p.ID, &n, map[string]any{"iris": "F2.8"}, true)
	if p.Name != n || p.Exposure["iris"] != "F2.8" {
		t.Fatalf("update: %+v", p)
	}
	l, _ = s.Reorder([]int64{p.ID, l[0].ID})
	if l[0].ID != p.ID {
		t.Fatalf("reorder: %+v", l[0])
	}
	if ok, _ := s.Delete(p.ID); !ok {
		t.Fatal("delete")
	}
	if g, _ := s.Get(p.ID); g != nil {
		t.Fatal("still there")
	}
}

// The Python build's presets.db must open as-is (same schema).
func TestOpensPythonDB(t *testing.T) {
	src, err := os.ReadFile("../../server/data/presets.db")
	if err != nil {
		t.Skip("no Python presets.db in this checkout")
	}
	dst := filepath.Join(t.TempDir(), "presets.db")
	if err := os.WriteFile(dst, src, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	l, err := s.List()
	if err != nil || len(l) == 0 {
		t.Fatalf("list: %v %v", l, err)
	}
	t.Logf("python db: %d presets, first %q slot %d", len(l), l[0].Name, l[0].Slot)
}
