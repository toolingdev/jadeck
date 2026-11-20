package logx

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRingFileRedactionLevels(t *testing.T) {
	var console bytes.Buffer
	l := New(&console)
	dir := t.TempDir()
	if err := l.AttachDir(dir); err != nil {
		t.Fatal(err)
	}
	log := l.Logger()
	log.Info("camera added", "host", "10.0.0.5", "pass", "hunter2", slog.Group("update", "token", "secret_x"))
	log.Debug("hidden at info")
	log.Warn("vMix not answering", "err", "connection refused")
	l.Close()

	b, _ := os.ReadFile(filepath.Join(dir, "ptz.log"))
	for _, out := range []string{console.String(), string(b)} {
		if strings.Contains(out, "hunter2") || strings.Contains(out, "secret_x") {
			t.Fatalf("secret written: %s", out)
		}
		if !strings.Contains(out, "pass=[redacted]") || !strings.Contains(out, "update.token=[redacted]") || !strings.Contains(out, "host=10.0.0.5") {
			t.Fatalf("missing attrs: %s", out)
		}
		if strings.Contains(out, "hidden at info") {
			t.Fatal("debug line written at info level")
		}
	}
	all := l.Query(0, slog.LevelDebug, "", 0)
	if len(all) != 2 || all[1].Level != "WARN" {
		t.Fatalf("ring: %+v", all)
	}
	if w := l.Query(0, slog.LevelWarn, "", 0); len(w) != 1 {
		t.Fatalf("warn filter: %+v", w)
	}
	if q := l.Query(0, slog.LevelDebug, "REFUSED", 0); len(q) != 1 {
		t.Fatalf("text filter: %+v", q)
	}
	if q := l.Query(all[0].Seq, slog.LevelDebug, "", 0); len(q) != 1 {
		t.Fatalf("after: %+v", q)
	}
	if err := l.SetLevel("debug"); err != nil || l.Level() != "debug" {
		t.Fatal(err, l.Level())
	}
	if l.SetLevel("loud") == nil {
		t.Fatal("bad level accepted")
	}
}

func TestRingWrapsAndFilesRotate(t *testing.T) {
	l := New(nil)
	dir := t.TempDir()
	l.AttachDir(dir)
	log := l.Logger()
	big := strings.Repeat("x", 1500)
	for i := 0; i < ringSize+500; i++ {
		log.Info("line", "i", i, "pad", big)
	}
	q := l.Query(0, slog.LevelDebug, "", 0)
	if len(q) != ringSize || q[0].Attrs["i"] != "500" || q[len(q)-1].Attrs["i"] != "4499" {
		t.Fatalf("ring: %d first=%v last=%v", len(q), q[0].Attrs["i"], q[len(q)-1].Attrs["i"])
	}
	if f := l.Files(); len(f) < 2 {
		t.Fatalf("no rotation: %v", f)
	}
	l.Close()
}
