package addons

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/toolingdev/jadeck/sdk/addon"
)

// proc is one running add-on process and the JSON-RPC pipe to it.
type proc struct {
	id   string
	log  *slog.Logger
	argv []string
	dir  string

	mu       sync.Mutex
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	pending  map[int64]chan addon.Message
	alive    bool
	started  time.Time
	lastErr  string
	restarts int

	// connections opened on this process, reopened after a restart
	opens map[string]addon.OpenParams

	next      atomic.Int64
	onChanged func(conn string)
	stopping  bool
}

var errNotRunning = errors.New("add-on is not running")

func (p *proc) start() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.alive {
		return nil
	}
	cmd := exec.Command(p.argv[0], p.argv[1:]...)
	cmd.Dir = p.dir
	hideWindow(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		p.lastErr = err.Error()
		return fmt.Errorf("start add-on %s: %w", p.id, err)
	}
	p.cmd, p.stdin, p.alive, p.started = cmd, stdin, true, time.Now()
	if p.pending == nil {
		p.pending = map[int64]chan addon.Message{}
	}
	p.log.Info("add-on started", "addon", p.id, "pid", cmd.Process.Pid)
	go p.readErr(stderr)
	go p.readOut(stdout)
	go p.wait(cmd)
	return nil
}

func (p *proc) readErr(r io.Reader) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		p.log.Info("add-on: "+sc.Text(), "addon", p.id)
	}
}

func (p *proc) readOut(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var m addon.Message
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			p.log.Warn("add-on wrote something that isn't a message", "addon", p.id, "line", truncate(sc.Text(), 200))
			continue
		}
		if m.ID != nil && m.Method == "" {
			p.mu.Lock()
			ch := p.pending[*m.ID]
			delete(p.pending, *m.ID)
			p.mu.Unlock()
			if ch != nil {
				ch <- m
			}
			continue
		}
		switch m.Method {
		case "log":
			var l addon.LogParams
			json.Unmarshal(m.Params, &l)
			lvl := map[string]slog.Level{"debug": slog.LevelDebug, "warn": slog.LevelWarn, "error": slog.LevelError}[l.Level]
			p.log.Log(context.Background(), lvl, "add-on: "+l.Msg, "addon", p.id)
		case "changed":
			var c struct {
				Conn string `json:"conn"`
			}
			json.Unmarshal(m.Params, &c)
			if p.onChanged != nil {
				p.onChanged(c.Conn)
			}
		}
	}
}

func (p *proc) wait(cmd *exec.Cmd) {
	err := cmd.Wait()
	p.mu.Lock()
	p.alive = false
	for id, ch := range p.pending {
		ch <- addon.Message{Error: &addon.RPCError{Code: -32001, Message: "add-on stopped"}}
		delete(p.pending, id)
	}
	stopping := p.stopping
	ran := time.Since(p.started)
	if err != nil {
		p.lastErr = err.Error()
	}
	p.mu.Unlock()
	if stopping {
		p.log.Info("add-on stopped", "addon", p.id)
		return
	}
	p.log.Warn("add-on exited — restarting it", "addon", p.id, "err", err, "ran", ran.Round(time.Second).String())
	// Back off when it keeps crashing; reset after a healthy minute.
	p.mu.Lock()
	if ran > time.Minute {
		p.restarts = 0
	}
	p.restarts++
	delay := time.Duration(min(p.restarts*p.restarts, 30)) * time.Second
	p.mu.Unlock()
	time.Sleep(delay)
	if err := p.boot(); err != nil {
		p.log.Error("add-on could not restart", "addon", p.id, "err", err)
	}
}

// boot starts the process, initializes it and reopens its connections.
func (p *proc) boot() error {
	if err := p.start(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var res addon.InitializeResult
	if err := p.call(ctx, "initialize", addon.InitializeParams{APIVersion: addon.APIVersion, Host: "ptz"}, &res); err != nil {
		p.kill()
		return fmt.Errorf("add-on %s did not initialize: %w", p.id, err)
	}
	p.mu.Lock()
	opens := make([]addon.OpenParams, 0, len(p.opens))
	for _, o := range p.opens {
		opens = append(opens, o)
	}
	p.mu.Unlock()
	for _, o := range opens {
		if err := p.call(ctx, "open", o, nil); err != nil {
			p.log.Warn("add-on: reopening a connection failed", "addon", p.id, "conn", o.Conn, "err", err)
		}
	}
	return nil
}

func (p *proc) call(ctx context.Context, method string, params, out any) error {
	p.mu.Lock()
	if !p.alive {
		p.mu.Unlock()
		return errNotRunning
	}
	id := p.next.Add(1)
	ch := make(chan addon.Message, 1)
	p.pending[id] = ch
	stdin := p.stdin
	p.mu.Unlock()

	b, _ := json.Marshal(params)
	msg, _ := json.Marshal(addon.Message{JSONRPC: "2.0", ID: &id, Method: method, Params: b})
	p.mu.Lock()
	_, err := stdin.Write(append(msg, '\n'))
	p.mu.Unlock()
	if err != nil {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return fmt.Errorf("add-on %s: %w", p.id, err)
	}
	select {
	case <-ctx.Done():
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return fmt.Errorf("add-on %s did not answer %s: %w", p.id, method, ctx.Err())
	case m := <-ch:
		if m.Error != nil {
			switch m.Error.Code {
			case addon.CodeNotConfigured:
				return fmt.Errorf("%s: %w", m.Error.Message, addon.ErrNotConfigured)
			case addon.CodeUnknownAction:
				return fmt.Errorf("%s: %w", m.Error.Message, addon.ErrUnknownAction)
			}
			return errors.New(m.Error.Message)
		}
		if out != nil && len(m.Result) > 0 {
			return json.Unmarshal(m.Result, out)
		}
		return nil
	}
}

func (p *proc) kill() {
	p.mu.Lock()
	cmd := p.cmd
	p.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		cmd.Process.Kill()
	}
}

// stop shuts the process down for good (update, turned off, exit).
func (p *proc) stop() {
	p.mu.Lock()
	p.stopping = true
	alive := p.alive
	p.mu.Unlock()
	if !alive {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	p.call(ctx, "shutdown", struct{}{}, nil)
	cancel()
	time.Sleep(100 * time.Millisecond)
	p.kill()
}

func (p *proc) status() (running bool, pid int, since time.Time, lastErr string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.alive && p.cmd != nil && p.cmd.Process != nil {
		pid = p.cmd.Process.Pid
	}
	return p.alive, pid, p.started, p.lastErr
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func joinArgs(a []string) string { return strings.Join(a, " ") }
