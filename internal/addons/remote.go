package addons

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/toolingdev/ptz-controller/sdk/addon"
)

// remote is a connection living in an add-on process. It implements
// every capability interface by forwarding the call; the connection's
// Caps (from the add-on) say which ones are real.
type remote struct {
	p    *proc
	conn string

	mu      sync.Mutex
	info    addon.PresentationInfo
	actions []string
	infoAt  time.Time
	maxCh   int
}

func (r *remote) call(ctx context.Context, method string, params, out any) error {
	b, _ := json.Marshal(params)
	return r.p.call(ctx, "call", addon.CallParams{Conn: r.conn, Method: method, Params: b}, out)
}

func quick() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 3*time.Second)
}

// switcher
func (r *remote) State(ctx context.Context) (st addon.SwitcherState, err error) {
	err = r.call(ctx, "switcher.state", nil, &st)
	return
}
func (r *remote) Preview(ctx context.Context, input int) error {
	return r.call(ctx, "switcher.preview", map[string]int{"input": input}, nil)
}
func (r *remote) Cut(ctx context.Context) error { return r.call(ctx, "switcher.cut", nil, nil) }
func (r *remote) Fade(ctx context.Context, ms int) error {
	return r.call(ctx, "switcher.fade", map[string]int{"durationMs": ms}, nil)
}

// slides (Info/Actions have no ctx in the interface: short timeout, cached 2 s)
func (r *remote) refreshInfo() {
	r.mu.Lock()
	fresh := time.Since(r.infoAt) < 2*time.Second
	r.mu.Unlock()
	if fresh {
		return
	}
	ctx, cancel := quick()
	defer cancel()
	var info addon.PresentationInfo
	var acts []string
	if err := r.call(ctx, "slides.info", nil, &info); err != nil {
		info = addon.PresentationInfo{Available: false, Target: err.Error()}
	}
	r.call(ctx, "slides.actions", nil, &acts)
	r.mu.Lock()
	r.info, r.infoAt = info, time.Now()
	if acts != nil {
		r.actions = acts
	}
	r.mu.Unlock()
}
func (r *remote) Info() addon.PresentationInfo {
	r.refreshInfo()
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.info
}
func (r *remote) Actions() []string {
	r.refreshInfo()
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.actions...)
}
func (r *remote) Do(ctx context.Context, action string) (map[string]any, error) {
	var d map[string]any
	err := r.call(ctx, "slides.do", map[string]string{"action": action}, &d)
	r.mu.Lock()
	r.infoAt = time.Time{} // re-read status after acting
	r.mu.Unlock()
	return d, err
}

// mixer
func (r *remote) Probe(ctx context.Context) (string, error) {
	var out struct {
		Identity string `json:"identity"`
	}
	err := r.call(ctx, "mixer.probe", nil, &out)
	return out.Identity, err
}
func (r *remote) MaxChannel() int {
	r.mu.Lock()
	n := r.maxCh
	r.mu.Unlock()
	if n > 0 {
		return n
	}
	ctx, cancel := quick()
	defer cancel()
	r.call(ctx, "mixer.maxChannel", nil, &n)
	if n > 0 {
		r.mu.Lock()
		r.maxCh = n
		r.mu.Unlock()
	}
	return n
}
func (r *remote) GetMute(ctx context.Context, ch int) (bool, error) {
	var out struct {
		Muted bool `json:"muted"`
	}
	err := r.call(ctx, "mixer.getMute", map[string]int{"channel": ch}, &out)
	return out.Muted, err
}
func (r *remote) SetMute(ctx context.Context, ch int, muted bool) error {
	return r.call(ctx, "mixer.setMute", map[string]any{"channel": ch, "muted": muted}, nil)
}

// add-on extras
func (r *remote) AddonState(ctx context.Context) (map[string]any, error) {
	var st map[string]any
	err := r.call(ctx, "addon.state", nil, &st)
	return st, err
}
func (r *remote) AddonAction(ctx context.Context, name string, params json.RawMessage) (any, error) {
	var out any
	err := r.call(ctx, "addon.action", map[string]any{"name": name, "params": params}, &out)
	return out, err
}

// Close tells the add-on to drop the connection.
func (r *remote) Close() error {
	r.p.mu.Lock()
	delete(r.p.opens, r.conn)
	r.p.mu.Unlock()
	ctx, cancel := quick()
	defer cancel()
	return r.p.call(ctx, "close", map[string]string{"conn": r.conn}, nil)
}
