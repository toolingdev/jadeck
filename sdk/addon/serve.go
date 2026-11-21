package addon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// OpenFunc opens one connection of a type from its saved settings. The
// returned value implements one or more capability interfaces.
type OpenFunc func(config json.RawMessage) (any, error)

// Spec is everything an add-on program provides.
type Spec struct {
	Manifest Manifest
	Open     map[string]OpenFunc               // connection type id → opener
	Options  map[string]func() []Option        // live option sources (Field.OptionsFrom)
	OnInit   func(log func(level, msg string)) // optional
}

// Serve runs the add-on over stdin/stdout until the controller closes
// the pipe or says shutdown. "--manifest" prints addon.json and exits.
func Serve(spec Spec) {
	spec.Manifest = spec.Manifest.LowestAPIVersion()
	if len(os.Args) > 1 && os.Args[1] == "--manifest" {
		b, _ := json.MarshalIndent(spec.Manifest, "", "  ")
		os.Stdout.Write(append(b, '\n'))
		return
	}
	if err := spec.Manifest.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := ServeIO(context.Background(), spec, os.Stdin, os.Stdout); err != nil && !errors.Is(err, io.EOF) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// ServeIO is Serve over any pipe (tests, embedding).
func ServeIO(ctx context.Context, spec Spec, in io.Reader, out io.Writer) error {
	spec.Manifest = spec.Manifest.LowestAPIVersion()
	s := &server{spec: spec, conns: map[string]any{}, out: out}
	if spec.OnInit != nil {
		spec.OnInit(s.log)
	}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	var wg sync.WaitGroup
	defer wg.Wait()
	defer s.closeAll()
	for sc.Scan() {
		var m Message
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			s.log("warn", "bad message from controller: "+err.Error())
			continue
		}
		if m.Method == "shutdown" {
			s.reply(m.ID, struct{}{}, nil)
			return nil
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := s.handle(ctx, m)
			s.reply(m.ID, res, err)
		}()
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return io.EOF
}

type server struct {
	spec  Spec
	mu    sync.Mutex
	conns map[string]any
	wmu   sync.Mutex
	out   io.Writer
}

func (s *server) write(m Message) {
	m.JSONRPC = "2.0"
	b, err := json.Marshal(m)
	if err != nil {
		return
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	s.out.Write(append(b, '\n'))
}

func (s *server) reply(id *int64, res any, err error) {
	if id == nil {
		return
	}
	if err != nil {
		code := -32000
		switch {
		case errors.Is(err, ErrNotConfigured):
			code = CodeNotConfigured
		case errors.Is(err, ErrUnknownAction):
			code = CodeUnknownAction
		case errors.Is(err, errNoConn):
			code = CodeNoConnection
		case errors.Is(err, ErrNoResources):
			code = CodeNoResources
		case errors.Is(err, ErrNotFound):
			code = CodeNotFound
		}
		s.write(Message{ID: id, Error: &RPCError{Code: code, Message: err.Error()}})
		return
	}
	b, _ := json.Marshal(res)
	s.write(Message{ID: id, Result: b})
}

func (s *server) log(level, msg string) {
	b, _ := json.Marshal(LogParams{Level: level, Msg: msg})
	s.write(Message{Method: "log", Params: b})
}

// Changed tells the controller a connection's state changed (for add-ons
// that learn about changes by themselves, e.g. over a websocket).
func (s *server) changed(conn string) {
	b, _ := json.Marshal(map[string]string{"conn": conn})
	s.write(Message{Method: "changed", Params: b})
}

func (s *server) closeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, c := range s.conns {
		if cl, ok := c.(Closer); ok {
			cl.Close()
		}
		delete(s.conns, id)
	}
}

var errNoConn = errors.New("no such connection (it was closed or never opened)")

func (s *server) conn(id string) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.conns[id]
	if !ok {
		return nil, errNoConn
	}
	return c, nil
}

// Notifier is given to connections that implement SetNotify, so they
// can say when their state changes.
type Notifier func()

type notifyable interface{ SetNotify(Notifier) }

func (s *server) handle(ctx context.Context, m Message) (any, error) {
	switch m.Method {
	case "initialize":
		var p InitializeParams
		json.Unmarshal(m.Params, &p)
		if p.APIVersion != 0 && p.APIVersion < s.spec.Manifest.APIVersion {
			return nil, fmt.Errorf("this add-on needs a newer controller (API %d, controller has %d)", s.spec.Manifest.APIVersion, p.APIVersion)
		}
		return InitializeResult{Manifest: s.spec.Manifest}, nil
	case "open":
		var p OpenParams
		if err := json.Unmarshal(m.Params, &p); err != nil {
			return nil, err
		}
		open := s.spec.Open[p.Type]
		if open == nil {
			return nil, fmt.Errorf("this add-on has no connection type %q", p.Type)
		}
		cfg := p.Config
		if len(cfg) == 0 {
			cfg = json.RawMessage("{}")
		}
		c, err := open(cfg)
		if err != nil {
			return nil, err
		}
		if n, ok := c.(notifyable); ok {
			id := p.Conn
			n.SetNotify(func() { s.changed(id) })
		}
		s.mu.Lock()
		if old, ok := s.conns[p.Conn]; ok {
			if cl, ok := old.(Closer); ok {
				cl.Close()
			}
		}
		s.conns[p.Conn] = c
		s.mu.Unlock()
		return OpenResult{Capabilities: Caps(c)}, nil
	case "close":
		var p struct {
			Conn string `json:"conn"`
		}
		json.Unmarshal(m.Params, &p)
		s.mu.Lock()
		c := s.conns[p.Conn]
		delete(s.conns, p.Conn)
		s.mu.Unlock()
		if cl, ok := c.(Closer); ok {
			cl.Close()
		}
		return struct{}{}, nil
	case "options":
		var p OptionsParams
		json.Unmarshal(m.Params, &p)
		if p.Conn != "" {
			// A widget field: the connection's own list first (live
			// channel names...), then the add-on-wide source.
			if c, err := s.conn(p.Conn); err == nil {
				if o, ok := c.(Optioner); ok {
					cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
					opts, err := o.AddonOptions(cctx, p.Name)
					cancel()
					if err != nil {
						return nil, err
					}
					if opts != nil {
						return opts, nil
					}
				}
			}
		}
		if f := s.spec.Options[p.Name]; f != nil {
			if o := f(); o != nil {
				return o, nil
			}
		}
		return []Option{}, nil
	case "call":
		var p CallParams
		if err := json.Unmarshal(m.Params, &p); err != nil {
			return nil, err
		}
		c, err := s.conn(p.Conn)
		if err != nil {
			return nil, err
		}
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		return Dispatch(cctx, c, p.Method, p.Params)
	}
	return nil, fmt.Errorf("unknown method %q", m.Method)
}

// Dispatch runs one call method against a connection value. Exposed so
// the controller can run built-in connections in-process the same way.
func Dispatch(ctx context.Context, c any, method string, params json.RawMessage) (any, error) {
	need := func(cap string) error { return fmt.Errorf("this connection has no %s capability", cap) }
	var p struct {
		Input      int             `json:"input"`
		DurationMs int             `json:"durationMs"`
		Action     string          `json:"action"`
		Channel    int             `json:"channel"`
		Muted      bool            `json:"muted"`
		Name       string          `json:"name"`
		Params     json.RawMessage `json:"params"`
		Path       string          `json:"path"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
	}
	ok := struct {
		OK bool `json:"ok"`
	}{true}
	switch method {
	case "switcher.state", "switcher.preview", "switcher.cut", "switcher.fade":
		sw, is := c.(Switcher)
		if !is {
			return nil, need(CapSwitcher)
		}
		switch method {
		case "switcher.state":
			return sw.State(ctx)
		case "switcher.preview":
			return ok, sw.Preview(ctx, p.Input)
		case "switcher.cut":
			return ok, sw.Cut(ctx)
		default:
			return ok, sw.Fade(ctx, p.DurationMs)
		}
	case "slides.info", "slides.actions", "slides.do":
		pr, is := c.(Presentation)
		if !is {
			return nil, need(CapSlides)
		}
		switch method {
		case "slides.info":
			return pr.Info(), nil
		case "slides.actions":
			return pr.Actions(), nil
		default:
			d, err := pr.Do(ctx, p.Action)
			if d == nil {
				d = map[string]any{}
			}
			return d, err
		}
	case "mixer.probe", "mixer.maxChannel", "mixer.getMute", "mixer.setMute":
		mx, is := c.(Mixer)
		if !is {
			return nil, need(CapMixer)
		}
		switch method {
		case "mixer.probe":
			id, err := mx.Probe(ctx)
			return map[string]string{"identity": id}, err
		case "mixer.maxChannel":
			return mx.MaxChannel(), nil
		case "mixer.getMute":
			m, err := mx.GetMute(ctx, p.Channel)
			return map[string]bool{"muted": m}, err
		default:
			return ok, mx.SetMute(ctx, p.Channel, p.Muted)
		}
	case "addon.state":
		var sp StateParams
		if len(params) > 0 {
			json.Unmarshal(params, &sp)
		}
		if ss, is := c.(SettingsStater); is {
			if _, plain := c.(Stater); !plain || sp.Settings != nil {
				return ss.AddonStateFor(ctx, sp.Settings)
			}
		}
		st, is := c.(Stater)
		if !is {
			return map[string]any{}, nil
		}
		return st.AddonState(ctx)
	case "addon.action":
		ac, is := c.(Actioner)
		if !is {
			return nil, fmt.Errorf("%w: %s", ErrUnknownAction, p.Name)
		}
		return ac.AddonAction(ctx, p.Name, p.Params)
	case "addon.resource":
		var res ResourceResult
		if rr, is := c.(ResultResourcer); is {
			r, err := rr.ResourceResult(ctx, p.Path)
			if err != nil {
				return nil, err
			}
			res = r
		} else if rs, is := c.(Resourcer); is {
			ct, data, err := rs.Resource(ctx, p.Path)
			if err != nil {
				return nil, err
			}
			res = ResourceResult{ContentType: ct, Data: data}
		} else {
			return nil, ErrNoResources
		}
		if len(res.Data) > MaxResource {
			return nil, fmt.Errorf("resource %s is %d bytes; the most is %d", p.Path, len(res.Data), MaxResource)
		}
		if res.ContentType == "" {
			res.ContentType = "application/octet-stream"
		}
		if res.MaxAge != nil && *res.MaxAge < 0 {
			res.MaxAge = Age(0)
		}
		return res, nil
	}
	return nil, fmt.Errorf("unknown call method %q", method)
}
