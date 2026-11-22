package devices

import (
	"encoding/json"
	"testing"
)

type closeCounter struct{ closed int }

func (c *closeCounter) Close() error { c.closed++; return nil }

// Adding or editing one connection leaves the others open.
func TestReopenKeepsUnchanged(t *testing.T) {
	opened := map[string]int{}
	impls := map[string]*closeCounter{}
	open := func(c Connection) (any, []string, error) {
		opened[c.ID]++
		cc := &closeCounter{}
		impls[c.ID+string(c.Config)] = cc
		return cc, []string{"tool"}, nil
	}
	a := Connection{ID: "a", Addon: "x", Type: "t", Name: "A", Config: json.RawMessage(`{"k":1}`)}
	b := Connection{ID: "b", Addon: "x", Type: "t", Name: "B", Config: json.RawMessage(`{"k":2}`)}
	s1 := Open(&File{Version: 2, Connections: []Connection{a, b}}, open)

	// rename a, change b's config, add c
	a2 := a
	a2.Name = "A renamed"
	b2 := b
	b2.Config = json.RawMessage(`{"k":3}`)
	c := Connection{ID: "c", Addon: "x", Type: "t", Name: "C", Config: json.RawMessage(`{}`)}
	s2 := Reopen(s1, &File{Version: 2, Connections: []Connection{a2, b2, c}}, open)

	if opened["a"] != 1 {
		t.Fatalf("a was reopened (%d) though only its name changed", opened["a"])
	}
	if impls[`a{"k":1}`].closed != 0 {
		t.Fatal("a was closed")
	}
	if opened["b"] != 2 || impls[`b{"k":2}`].closed != 1 {
		t.Fatalf("b (changed) should close and reopen: opened %d closed %d", opened["b"], impls[`b{"k":2}`].closed)
	}
	if opened["c"] != 1 {
		t.Fatal("c not opened")
	}
	if l := s2.Get("a"); l == nil || l.Name != "A renamed" || l.Impl != s1.Get("a").Impl {
		t.Fatalf("a should keep its connection and take the new name: %+v", l)
	}

	// removing c closes it
	s3 := Reopen(s2, &File{Version: 2, Connections: []Connection{a2, b2}}, open)
	if impls[`c{}`].closed != 1 || s3.Get("c") != nil {
		t.Fatal("removed connection not closed")
	}
}
