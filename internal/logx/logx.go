// Package logx is the controller's logging: one slog.Logger that writes
// to the console, to rotating files in the data folder (so a Windows
// service, which has no console, still leaves a trail that survives the
// PC being switched off), and to an in-memory ring that the diagnostics
// API serves over the network. Values of attributes that look like
// secrets are never written anywhere.
package logx

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	ringSize = 4000
	fileMax  = 5 << 20 // bytes per file
	fileKeep = 6       // ptz.log + ptz.1.log … ptz.5.log
)

// Entry is one log line, as the diagnostics API returns it.
type Entry struct {
	Seq   uint64            `json:"seq"`
	Time  time.Time         `json:"time"`
	Level string            `json:"level"`
	Msg   string            `json:"msg"`
	Attrs map[string]string `json:"attrs,omitempty"`
}

// Logs owns the outputs. Create one with New, use Logger(), and call
// AttachDir once the data folder is known.
type Logs struct {
	level   *slog.LevelVar
	started time.Time

	mu   sync.Mutex
	ring []Entry
	next int
	seq  uint64
	out  io.Writer // console
	file *rotator
}

func New(console io.Writer) *Logs {
	l := &Logs{level: new(slog.LevelVar), started: time.Now(), ring: make([]Entry, 0, ringSize), out: console}
	l.level.Set(slog.LevelInfo)
	return l
}

func (l *Logs) Logger() *slog.Logger { return slog.New(&handler{l: l}) }

func (l *Logs) Started() time.Time { return l.started }

// SetLevel changes how much is kept: debug, info, warn, error.
func (l *Logs) SetLevel(s string) error {
	var lv slog.Level
	if err := lv.UnmarshalText([]byte(s)); err != nil {
		return fmt.Errorf("level must be debug, info, warn or error")
	}
	l.level.Set(lv)
	return nil
}

func (l *Logs) Level() string { return strings.ToLower(l.level.Level().String()) }

// AttachDir starts writing <dir>/ptz.log (rotated by size).
func (l *Logs) AttachDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	r := &rotator{path: filepath.Join(dir, "ptz.log")}
	if err := r.open(); err != nil {
		return err
	}
	l.mu.Lock()
	l.file = r
	l.mu.Unlock()
	return nil
}

// Dir is where the log files are ("" before AttachDir).
func (l *Logs) Dir() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return ""
	}
	return filepath.Dir(l.file.path)
}

// Query returns ring entries, oldest first: after seq, at or above
// level, containing text (case-insensitive, message or attributes),
// at most limit (the newest ones).
func (l *Logs) Query(after uint64, min slog.Level, text string, limit int) []Entry {
	text = strings.ToLower(text)
	l.mu.Lock()
	defer l.mu.Unlock()
	n := len(l.ring)
	out := []Entry{}
	start := 0
	if n == ringSize {
		start = l.next
	}
	for i := 0; i < n; i++ {
		e := l.ring[(start+i)%n]
		if e.Seq <= after {
			continue
		}
		var lv slog.Level
		lv.UnmarshalText([]byte(e.Level))
		if lv < min {
			continue
		}
		if text != "" && !strings.Contains(strings.ToLower(line(e)), text) {
			continue
		}
		out = append(out, e)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// Close flushes and closes the file.
func (l *Logs) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		return l.file.f.Close()
	}
	return nil
}

func (l *Logs) write(e Entry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	e.Seq = l.seq
	if len(l.ring) < ringSize {
		l.ring = append(l.ring, e)
	} else {
		l.ring[l.next] = e
		l.next = (l.next + 1) % ringSize
	}
	s := line(e) + "\n"
	if l.out != nil {
		io.WriteString(l.out, s)
	}
	if l.file != nil {
		l.file.write(s)
	}
}

func line(e Entry) string {
	var b strings.Builder
	b.WriteString(e.Time.Format("2006-01-02 15:04:05.000"))
	b.WriteByte(' ')
	b.WriteString(fmt.Sprintf("%-5s", e.Level))
	b.WriteByte(' ')
	b.WriteString(e.Msg)
	keys := make([]string, 0, len(e.Attrs))
	for k := range e.Attrs {
		keys = append(keys, k)
	}
	sortStrings(keys)
	for _, k := range keys {
		v := e.Attrs[k]
		if strings.ContainsAny(v, " \t\"=") {
			v = fmt.Sprintf("%q", v)
		}
		b.WriteString(" " + k + "=" + v)
	}
	return b.String()
}

// ---------------------------------------------------------------- handler

type handler struct {
	l      *Logs
	attrs  []slog.Attr
	groups []string
}

func (h *handler) Enabled(_ context.Context, lv slog.Level) bool { return lv >= h.l.level.Level() }

func (h *handler) Handle(_ context.Context, r slog.Record) error {
	e := Entry{Time: r.Time, Level: r.Level.String(), Msg: r.Message, Attrs: map[string]string{}}
	prefix := strings.Join(h.groups, ".")
	add := func(a slog.Attr) { flatten(e.Attrs, prefix, a) }
	for _, a := range h.attrs {
		add(a)
	}
	r.Attrs(func(a slog.Attr) bool {
		add(a)
		return true
	})
	if len(e.Attrs) == 0 {
		e.Attrs = nil
	}
	h.l.write(e)
	return nil
}

func (h *handler) WithAttrs(as []slog.Attr) slog.Handler {
	n := *h
	n.attrs = append(append([]slog.Attr{}, h.attrs...), as...)
	return &n
}

func (h *handler) WithGroup(name string) slog.Handler {
	n := *h
	n.groups = append(append([]string{}, h.groups...), name)
	return &n
}

var secretRe = regexp.MustCompile(`(?i)(pass|secret|token|authorization|api.?key|cookie|signing)`)

func flatten(m map[string]string, prefix string, a slog.Attr) {
	a.Value = a.Value.Resolve()
	k := a.Key
	if prefix != "" {
		k = prefix + "." + k
	}
	if a.Value.Kind() == slog.KindGroup {
		for _, g := range a.Value.Group() {
			flatten(m, k, g)
		}
		return
	}
	if k == "" {
		return
	}
	v := a.Value.String()
	if secretRe.MatchString(a.Key) && v != "" {
		v = "[redacted]"
	}
	if len(v) > 2000 {
		v = v[:2000] + "…"
	}
	m[k] = v
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// ---------------------------------------------------------------- rotator

type rotator struct {
	path string
	f    *os.File
	size int64
}

func (r *rotator) open() error {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	st, _ := f.Stat()
	r.f, r.size = f, 0
	if st != nil {
		r.size = st.Size()
	}
	return nil
}

func (r *rotator) write(s string) {
	if r.f == nil {
		return
	}
	if r.size+int64(len(s)) > fileMax {
		r.rotate()
	}
	n, _ := r.f.WriteString(s)
	r.size += int64(n)
}

func (r *rotator) rotate() {
	r.f.Close()
	base := strings.TrimSuffix(r.path, ".log")
	os.Remove(fmt.Sprintf("%s.%d.log", base, fileKeep-1))
	for i := fileKeep - 2; i >= 1; i-- {
		os.Rename(fmt.Sprintf("%s.%d.log", base, i), fmt.Sprintf("%s.%d.log", base, i+1))
	}
	os.Rename(r.path, base+".1.log")
	if err := r.open(); err != nil {
		r.f = nil
	}
}

// Files lists the log files, newest first.
func (l *Logs) Files() []string {
	dir := l.Dir()
	if dir == "" {
		return nil
	}
	out := []string{"ptz.log"}
	for i := 1; i < fileKeep; i++ {
		n := fmt.Sprintf("ptz.%d.log", i)
		if _, err := os.Stat(filepath.Join(dir, n)); err == nil {
			out = append(out, n)
		}
	}
	return out
}
