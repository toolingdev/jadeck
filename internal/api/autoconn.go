package api

import (
	"encoding/json"
	"sync"

	"ptz/internal/devices"
)

// Auto connection types (SDK ConnectionType.Auto): the controller keeps
// exactly one connection of each such type while its add-on is on, so the
// add-on's widgets work without any setup (Live chat's widgets read the
// platform connections). They stay out of Settings → Connections.

var autoMu sync.Mutex

// ensureAutoConnections adds the missing auto connections (and saves and
// reopens the set when it did). Cheap when nothing is missing.
func (s *Server) ensureAutoConnections() {
	if s.Addons == nil {
		return
	}
	autoMu.Lock()
	defer autoMu.Unlock()
	s.devMu.RLock()
	loaded := s.DevFile != nil
	s.devMu.RUnlock()
	if !loaded {
		return // devices set up some other way (tests, legacy): leave it alone
	}
	f := s.currentFile()
	have := map[string]bool{}
	for _, c := range f.Connections {
		have[c.Addon+"\x00"+c.Type] = true
	}
	var add []devices.Connection
	for _, in := range s.Addons.List() {
		if !in.Enabled {
			continue
		}
		for _, t := range in.Manifest.ConnectionTypes {
			if !t.Auto || have[in.Manifest.ID+"\x00"+t.ID] {
				continue
			}
			add = append(add, devices.Connection{Addon: in.Manifest.ID, Type: t.ID, Name: t.Label, Config: json.RawMessage("{}")})
		}
	}
	if len(add) == 0 {
		return
	}
	nf := *f
	nf.Connections = append([]devices.Connection(nil), f.Connections...)
	for _, c := range add {
		c.ID = devices.NewID(&nf, slug(c.Name, c.Addon))
		nf.Connections = append(nf.Connections, c)
		s.Log.Info("connection added by the controller", "connection", c.Name, "addon", c.Addon, "type", c.Type)
	}
	if err := s.saveAndOpen(&nf, "added (auto)"); err != nil {
		s.Log.Warn("auto connection not saved", "err", err.Error())
	}
}

// isAuto reports whether a connection is of an auto type.
func (s *Server) isAuto(c devices.Connection) bool {
	if s.Addons == nil {
		return false
	}
	t, ok := s.Addons.ConnectionType(c.Addon, c.Type)
	return ok && t.Auto
}
