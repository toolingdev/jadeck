package api

import (
	"sync"
	"time"
)

// health remembers whether each device answered last time it was asked,
// and logs only the changes ("vMix stopped answering", "vMix answering
// again after 42s"), so a dead device during a service is one line, not
// one line every two seconds. The diagnostics snapshot reads it too.
type health struct {
	mu sync.Mutex
	m  map[string]*healthState
}

type healthState struct {
	OK     bool      `json:"ok"`
	Detail string    `json:"detail,omitempty"`
	Since  time.Time `json:"since"`
	Checks int       `json:"checks"`
	Flips  int       `json:"changes"` // how many times it went down or up since start
}

func (s *Server) track(name string, ok bool, detail string) {
	h := &s.health
	h.mu.Lock()
	if h.m == nil {
		h.m = map[string]*healthState{}
	}
	st, seen := h.m[name]
	if !seen {
		st = &healthState{OK: ok, Detail: detail, Since: time.Now()}
		h.m[name] = st
	}
	st.Checks++
	changed := seen && (st.OK != ok || (!ok && st.Detail != detail))
	was, since := st.OK, st.Since
	if changed {
		if st.OK != ok {
			st.Flips++
			st.Since = time.Now()
		}
		st.OK, st.Detail = ok, detail
	}
	h.mu.Unlock()
	switch {
	case !seen && ok && detail != "":
		s.Log.Info(name+" answering", "detail", detail)
	case !seen && ok:
		s.Log.Info(name + " answering")
	case !seen:
		s.Log.Warn(name+" not answering", "reason", detail)
	case changed && ok:
		s.Log.Info(name+" answering again", "after", time.Since(since).Round(time.Second).String())
	case changed && was:
		s.Log.Warn(name+" stopped answering", "reason", detail)
	case changed:
		s.Log.Warn(name+" still not answering, new reason", "reason", detail)
	}
}

func (s *Server) healthSnapshot() map[string]healthState {
	s.health.mu.Lock()
	defer s.health.mu.Unlock()
	out := map[string]healthState{}
	for k, v := range s.health.m {
		out[k] = *v
	}
	return out
}
