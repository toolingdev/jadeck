package auth

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestTrust(t *testing.T) {
	tr, _ := NewTrust(true, "203.0.113.0/24")
	for addr, want := range map[string]bool{
		"127.0.0.1:5000": true, "[::1]:80": true, "192.168.1.50:1": true, "10.1.10.20:9": true,
		"172.20.1.1:1": true, "100.101.102.103:443": true, "[fd7a:115c:a1e0::5]:1": true,
		"[::ffff:192.168.1.5]:1": true, "203.0.113.7:1": true,
		"8.8.8.8:53": false, "172.32.0.1:1": false, "100.128.0.1:1": false, "garbage": false,
	} {
		if got := tr.Trusted(addr); got != want {
			t.Errorf("%s: got %v", addr, got)
		}
	}
	local, _ := NewTrust(false, "")
	if local.Trusted("192.168.1.50:1") || !local.Trusted("127.0.0.1:1") {
		t.Fatal("trustLAN=false")
	}
	if _, err := NewTrust(true, "not-a-cidr"); err == nil {
		t.Fatal("bad CIDR accepted")
	}
}

func TestKeyLifecycle(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "keys.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	k, secret, err := s.Create("Claude — camera", []string{"camera.move", "camera.read", "camera.read"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(secret, "ptz_") || len(secret) < 40 || !strings.HasPrefix(secret[4:], k.Prefix) {
		t.Fatalf("secret %q prefix %q", secret, k.Prefix)
	}
	if len(k.Scopes) != 2 || !k.Has("camera.move") || k.Has("mixer") || !k.Active() {
		t.Fatalf("%+v", k)
	}
	if _, _, err := s.Create("x", []string{"root"}); !errors.Is(err, ErrInput) {
		t.Fatal("unknown scope accepted")
	}
	got, err := s.Lookup(secret)
	if err != nil || got.ID != k.ID {
		t.Fatal("lookup")
	}
	if _, err := s.Lookup(secret + "x"); !errors.Is(err, ErrNotFound) {
		t.Fatal("wrong secret matched")
	}
	off := false
	k, _ = s.Update(k.ID, nil, []string{"mixer"}, true, &off)
	if k.Active() || !k.Has("mixer") || k.Has("camera.move") {
		t.Fatalf("%+v", k)
	}
	on := true
	k, _ = s.Update(k.ID, nil, nil, false, &on)
	if !k.Active() {
		t.Fatal("re-enable")
	}
	k, _ = s.Revoke(k.ID)
	if k.Active() || k.RevokedAt == nil {
		t.Fatal("revoke")
	}
	if _, err := s.Update(k.ID, nil, nil, false, &on); !errors.Is(err, ErrInput) {
		t.Fatal("revoked key re-enabled")
	}
	s.Touch(k, "100.64.0.9")
	s.Audit(k, "100.64.0.9", "mcp camera_home", "ok")
	log, _ := s.AuditLog(10)
	if len(log) != 1 || log[0].Action != "mcp camera_home" {
		t.Fatal(log)
	}
	if err := s.Delete(k.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lookup(secret); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted key still works")
	}
	if log, _ := s.AuditLog(10); len(log) != 1 {
		t.Fatal("audit should survive key deletion")
	}
}
