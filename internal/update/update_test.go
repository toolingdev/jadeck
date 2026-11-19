package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v1.2.3", "v1.2.2", true}, {"v1.10.0", "v1.9.9", true}, {"v1.2.3", "v1.2.3", false},
		{"v1.2.3", "dev", true}, {"dev", "v1.0.0", false}, {"v1.2.3", "v1.2.3-rc1", true},
		{"v1.2.3-rc2", "v1.2.3-rc1", true}, {"v2.0.0", "v10.0.0", false}, {"1.0.0", "v0.9.0", true},
	}
	for _, c := range cases {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%s,%s)=%v", c.a, c.b, got)
		}
	}
}

type fakeGH struct {
	pub    ed25519.PublicKey
	srv    *httptest.Server
	assets map[string][]byte
	tag    string
}

func newFakeGH(t *testing.T, tag string, bin []byte, tamper func(map[string][]byte)) *fakeGH {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	h := sha256.Sum256(bin)
	sums := fmt.Sprintf("%s  %s\n%s  ptz-other-arch\n", hex.EncodeToString(h[:]), AssetName(), strings.Repeat("0", 64))
	f := &fakeGH{pub: pub, tag: tag, assets: map[string][]byte{
		AssetName():      bin,
		"SHA256SUMS":     []byte(sums),
		"SHA256SUMS.sig": []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(sums)))),
	}}
	if tamper != nil {
		tamper(f.assets)
	}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/o/r/releases/latest" {
			rel := Release{Tag: f.tag, Body: "notes"}
			for n := range f.assets {
				rel.Assets = append(rel.Assets, Asset{Name: n, URL: f.srv.URL + "/dl/" + n})
			}
			json.NewEncoder(w).Encode(rel)
			return
		}
		if b, ok := f.assets[strings.TrimPrefix(r.URL.Path, "/dl/")]; ok && r.Header.Get("Accept") == "application/octet-stream" {
			w.Write(b)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func mgr(t *testing.T, f *fakeGH, current string) *Manager {
	return &Manager{Current: current, PubKey: f.pub, Dir: t.TempDir(),
		Source: &Source{Repo: "o/r", API: f.srv.URL}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func TestCheckStagesVerifiedRelease(t *testing.T) {
	f := newFakeGH(t, "v1.1.0", []byte("new binary"), nil)
	m := mgr(t, f, "v1.0.0")
	if err := m.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := m.Status()
	if st.State != "available" || st.Version != "v1.1.0" || st.Notes != "notes" {
		t.Fatalf("%+v", st)
	}
	b, _ := os.ReadFile(filepath.Join(m.Dir, "staged", AssetName()))
	if string(b) != "new binary" {
		t.Fatal("staged file wrong")
	}
	// A fresh manager (restart) finds the staged update again.
	m2 := &Manager{Current: "v1.0.0", PubKey: f.pub, Dir: m.Dir, Source: m.Source, Log: m.Log}
	m2.loadStaged()
	if m2.Status().State != "available" {
		t.Fatal("staged update lost across restart")
	}
}

func TestNotNewerIsNone(t *testing.T) {
	f := newFakeGH(t, "v1.0.0", []byte("x"), nil)
	m := mgr(t, f, "v1.0.0")
	m.Check(context.Background())
	if st := m.Status(); st.State != "none" {
		t.Fatalf("%+v", st)
	}
}

func TestTamperingIsRefused(t *testing.T) {
	cases := map[string]func(map[string][]byte){
		"binary swapped":   func(a map[string][]byte) { a[AssetName()] = []byte("evil") },
		"sums rewritten":   func(a map[string][]byte) { a["SHA256SUMS"] = append(a["SHA256SUMS"], '\n') },
		"signature junk":   func(a map[string][]byte) { a["SHA256SUMS.sig"] = []byte("AAAA") },
		"no signature":     func(a map[string][]byte) { delete(a, "SHA256SUMS.sig") },
		"no binary for us": func(a map[string][]byte) { delete(a, AssetName()) },
	}
	for name, tamper := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFakeGH(t, "v9.9.9", []byte("good"), tamper)
			m := mgr(t, f, "v1.0.0")
			err := m.Check(context.Background())
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("expected ErrInvalid, got %v", err)
			}
			if st := m.Status(); st.State != "invalid" {
				t.Fatalf("%+v", st)
			}
			if _, err := os.Stat(filepath.Join(m.Dir, "staged", AssetName())); err == nil {
				t.Fatal("unverified binary was staged")
			}
		})
	}
}

func TestWrongKeyRefused(t *testing.T) {
	f := newFakeGH(t, "v2.0.0", []byte("good"), nil)
	m := mgr(t, f, "v1.0.0")
	m.PubKey, _, _ = ed25519.GenerateKey(rand.Reader) // someone else's key
	if err := m.Check(context.Background()); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestDisabledWithoutKey(t *testing.T) {
	m := &Manager{Current: "v1", Source: &Source{Repo: "o/r"}, Dir: t.TempDir()}
	if st := m.Status(); st.State != "disabled" || !strings.Contains(st.Reason, "unsigned") {
		t.Fatalf("%+v", st)
	}
}

func TestApplyReverifies(t *testing.T) {
	f := newFakeGH(t, "v1.1.0", []byte("new binary"), nil)
	m := mgr(t, f, "v1.0.0")
	m.Check(context.Background())
	os.WriteFile(filepath.Join(m.Dir, "staged", AssetName()), []byte("swapped on disk"), 0o755)
	if _, err := m.Apply(ApplyRequest{Mode: "exec"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("apply of a modified staged file: %v", err)
	}
	if m.Status().State != "invalid" {
		t.Fatal("not marked invalid")
	}
}

func TestRolledBackVersionIsNeverReoffered(t *testing.T) {
	f := newFakeGH(t, "v1.1.0", []byte("bad build"), nil)
	m := mgr(t, f, "v1.0.0")
	blockVersion(m.Dir, "v1.1.0")
	b, _ := json.Marshal(HelperState{State: "rolled_back", Version: "v1.1.0", From: "v1.0.0", Reason: "unhealthy"})
	os.WriteFile(m.StatePath(), b, 0o644)
	m.Check(context.Background())
	if st := m.Status(); st.State != "rolled_back" || st.Reason != "unhealthy" {
		t.Fatalf("%+v", st)
	}
	m.DismissOutcome()
	if st := m.Status(); st.State != "none" {
		t.Fatalf("blocked version re-offered after dismiss: %+v", st)
	}
	f.tag = "v1.1.1"                            // a fixed release is offered
	f.assets[AssetName()] = []byte("bad build") // same bytes, still matches sums
	if err := m.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := m.Status(); st.State != "available" || st.Version != "v1.1.1" {
		t.Fatalf("%+v", st)
	}
}
