package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"ptz/internal/stage"
)

// Stage messages: channels link Message sender / Message display widgets
// on any screens. Live updates go out as Server-Sent Events.
//
//	GET    /api/stage                         channels + state (all)
//	POST   /api/stage/channels                {name, color}
//	PUT    /api/stage/channels/{id}           {name, color}
//	DELETE /api/stage/channels/{id}
//	POST   /api/stage/channels/{id}/messages  {text, from}
//	DELETE /api/stage/channels/{id}/messages  clear
//	POST   /api/stage/channels/{id}/seen      {id, screen}
//	POST   /api/stage/channels/{id}/timer     {op, label, seconds}
//	GET    /api/stage/events?screen=&channels=a,b   event stream
func (s *Server) stageRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/stage", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, s.stageSnapshot(nil))
	})
	mux.HandleFunc("POST /api/stage/channels", func(w http.ResponseWriter, r *http.Request) {
		var b struct{ Name, Color string }
		if !decode(w, r, &b) {
			return
		}
		c, err := s.Stage.CreateChannel(b.Name, b.Color)
		if stageErr(w, err) {
			return
		}
		writeJSON(w, 201, c)
	})
	mux.HandleFunc("PUT /api/stage/channels/{id}", func(w http.ResponseWriter, r *http.Request) {
		var b struct{ Name, Color string }
		if !decode(w, r, &b) {
			return
		}
		c, err := s.Stage.UpdateChannel(r.PathValue("id"), b.Name, b.Color)
		if stageErr(w, err) {
			return
		}
		writeJSON(w, 200, c)
	})
	mux.HandleFunc("DELETE /api/stage/channels/{id}", func(w http.ResponseWriter, r *http.Request) {
		if stageErr(w, s.Stage.DeleteChannel(r.PathValue("id"))) {
			return
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /api/stage/channels/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		var b struct{ Text, From string }
		if !decode(w, r, &b) {
			return
		}
		m, err := s.Stage.Send(r.PathValue("id"), b.Text, b.From)
		if stageErr(w, err) {
			return
		}
		writeJSON(w, 201, m)
	})
	mux.HandleFunc("DELETE /api/stage/channels/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		if stageErr(w, s.Stage.Clear(r.PathValue("id"))) {
			return
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /api/stage/channels/{id}/seen", func(w http.ResponseWriter, r *http.Request) {
		var b struct{ ID, Screen string }
		if !decode(w, r, &b) {
			return
		}
		if stageErr(w, s.Stage.Seen(r.PathValue("id"), b.ID, b.Screen)) {
			return
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /api/stage/channels/{id}/timer", func(w http.ResponseWriter, r *http.Request) {
		var op stage.TimerOp
		if !decode(w, r, &op) {
			return
		}
		t, err := s.Stage.Timer(r.PathValue("id"), op)
		if stageErr(w, err) {
			return
		}
		writeJSON(w, 200, map[string]any{"timer": t})
	})
	mux.HandleFunc("GET /api/stage/events", s.stageEvents)
}

func stageErr(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, stage.ErrNotFound):
		fail(w, 404, "that channel doesn't exist (deleted?)")
	case errors.Is(err, stage.ErrInvalid):
		fail(w, 422, err.Error())
	default:
		fail(w, 500, err.Error())
	}
	return true
}

type stageSnap struct {
	Now      time.Time            `json:"now"` // server clock, so every screen's countdown agrees
	MaxText  int                  `json:"maxText"`
	Channels []stage.ChannelState `json:"channels"`
}

func (s *Server) stageSnapshot(sub *stage.Sub) stageSnap {
	return stageSnap{Now: time.Now().UTC(), MaxText: stage.MaxText, Channels: s.Stage.State(sub)}
}

func (s *Server) stageEvents(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	q := r.URL.Query()
	var chans []string
	if c := q.Get("channels"); c != "" {
		chans = strings.Split(c, ",")
	}
	sub, leave := s.Stage.Subscribe(q.Get("screen"), chans)
	defer leave()

	send := func() bool {
		b, _ := json.Marshal(s.stageSnapshot(sub))
		if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	if !send() {
		return
	}
	ping := time.NewTicker(20 * time.Second) // keeps proxies and sleepy Wi-Fi honest
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-sub.C:
			if !send() {
				return
			}
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil || rc.Flush() != nil {
				return
			}
		}
	}
}
