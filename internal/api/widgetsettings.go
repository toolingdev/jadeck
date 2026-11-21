package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/toolingdev/jadeck/sdk/addon"
	"ptz/internal/dashboards"
	"ptz/internal/devices"
)

// Per-widget settings an add-on defines (design/WIDGETS-V2.md v2.1).

// options answers a live option list. With conn, the connection (of that
// add-on) is asked first: its Optioner, which for an add-on process sends
// options {name, conn}; an empty answer falls back to the add-on-wide
// source (Spec.Options).
func (s *Server) options(r *http.Request, addonID, name, conn string) []addon.Option {
	if s.Addons == nil {
		return nil
	}
	if conn != "" {
		if l := s.Devs().Get(conn); l != nil && l.Addon == addonID && l.Impl != nil {
			if o, ok := l.Impl.(addon.Optioner); ok {
				ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
				got, err := o.AddonOptions(ctx, name)
				cancel()
				if err == nil && got != nil {
					return got
				}
			}
		}
	}
	return s.Addons.Options(addonID, name)
}

// widgetSettings finds a dashboard widget showing this connection's
// add-on and returns the settings its add-on declared (WidgetDef.Fields,
// defaults filled in), nothing else. dash narrows the search to one
// dashboard (widget ids are only unique within one; a duplicated
// dashboard repeats them). ok is false when there's no such widget or it
// declares no fields.
func (s *Server) widgetSettings(l *devices.Live, widgetID, dash string) (map[string]any, bool) {
	if s.Dashboards == nil || s.Addons == nil {
		return nil, false
	}
	m, ok := s.Addons.Manifest(l.Addon)
	if !ok {
		return nil, false
	}
	var list []dashboards.Dashboard
	if id, err := strconv.ParseInt(dash, 10, 64); err == nil && dash != "" {
		d, err := s.Dashboards.Get(id)
		if err != nil {
			return nil, false
		}
		list = []dashboards.Dashboard{*d}
	} else {
		all, err := s.Dashboards.List()
		if err != nil {
			return nil, false
		}
		list = all
	}
	prefix := "addon:" + l.Addon + ":"
	var found *dashboards.Widget
	for _, d := range list {
		for i := range d.Widgets {
			wd := &d.Widgets[i]
			if wd.ID != widgetID || !strings.HasPrefix(wd.Type, prefix) {
				continue
			}
			// The widget's connection must be this one (or unset: the
			// add-on's first connection). A named one wins over unset.
			conn, _ := wd.Settings["connection"].(string)
			if conn != "" && conn != l.ID {
				continue
			}
			if found == nil || conn == l.ID {
				found = wd
			}
		}
	}
	if found == nil {
		return nil, false
	}
	typ := strings.TrimPrefix(found.Type, prefix)
	for _, def := range m.Widgets {
		if def.Kind == addon.KindCore || def.Type != typ || len(def.Fields) == 0 {
			continue
		}
		out := map[string]any{}
		for _, f := range def.Fields {
			if v, ok := found.Settings[f.Key]; ok && v != nil {
				out[f.Key] = v
			} else if f.Default != nil {
				out[f.Key] = f.Default
			}
		}
		return out, true
	}
	return nil, false
}
