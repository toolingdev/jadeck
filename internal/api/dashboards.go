package api

import (
	"errors"
	"net/http"
	"strings"

	"ptz/internal/dashboards"
)

func (s *Server) dashboardRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/dashboards", func(w http.ResponseWriter, r *http.Request) {
		l, err := s.Dashboards.List()
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, l)
	})
	mux.HandleFunc("GET /api/dashboards/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		d, err := s.Dashboards.Get(id)
		if err != nil {
			dashErr(w, err)
			return
		}
		writeJSON(w, 200, d)
	})
	mux.HandleFunc("POST /api/dashboards", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			dashboards.Dashboard
			From *int64 `json:"from"` // copy an existing one (reflowed if Kind differs)
		}
		if !decode(w, r, &b) {
			return
		}
		var c *dashboards.Dashboard
		var err error
		if b.From != nil {
			c, err = s.Dashboards.Duplicate(*b.From, b.Name, b.Kind)
		} else {
			d := b.Dashboard
			c, err = s.Dashboards.Create(&d)
		}
		if err != nil {
			dashErr(w, err)
			return
		}
		writeJSON(w, 201, c)
	})
	mux.HandleFunc("GET /api/dashboards/defaults", func(w http.ResponseWriter, r *http.Request) {
		d, err := s.Dashboards.Defaults()
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, d)
	})
	mux.HandleFunc("PUT /api/dashboards/defaults", func(w http.ResponseWriter, r *http.Request) {
		var b map[string]int64
		if !decode(w, r, &b) {
			return
		}
		for k, id := range b {
			if err := s.Dashboards.SetDefault(k, id); err != nil {
				dashErr(w, err)
				return
			}
		}
		d, _ := s.Dashboards.Defaults()
		writeJSON(w, 200, d)
	})
	mux.HandleFunc("PUT /api/dashboards/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		var d dashboards.Dashboard
		r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
		if !decode(w, r, &d) {
			return
		}
		u, err := s.Dashboards.Update(id, &d)
		if err != nil {
			dashErr(w, err)
			return
		}
		writeJSON(w, 200, u)
	})
	mux.HandleFunc("DELETE /api/dashboards/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		if err := s.Dashboards.Delete(id); err != nil {
			dashErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
}

func dashErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, dashboards.ErrNotFound):
		fail(w, 404, "dashboard not found")
	case errors.Is(err, dashboards.ErrInvalid):
		fail(w, 400, strings.TrimPrefix(err.Error(), dashboards.ErrInvalid.Error()+": "))
	default:
		fail(w, 500, err.Error())
	}
}
