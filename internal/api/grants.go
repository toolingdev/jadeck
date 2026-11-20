package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"

	"ptz/internal/auth"
	"ptz/internal/devices"

	"github.com/toolingdev/jadeck/sdk/addon"
)

// Controller add-ons (e.g. the AI controller) reach the controller's API
// and MCP endpoint with a token minted when their connection opens. It
// lives only in memory, carries only the permissions picked for that
// connection, and is replaced when the connection reopens.

var neverGrant = map[string]bool{"addons.admin": true, "update.apply": true}

type grants struct {
	mu      sync.Mutex
	byToken map[string]*auth.Key
	byConn  map[string]string
}

// GrantAddon is addons.Host.Grant.
func (s *Server) GrantAddon(c devices.Connection, scopes []string) addon.ControllerAccess {
	known := map[string]bool{}
	for _, sc := range auth.Scopes {
		known[sc.ID] = true
	}
	var can []string
	for _, sc := range scopes {
		if known[sc] && !neverGrant[sc] {
			can = append(can, sc)
		}
	}
	b := make([]byte, 24)
	rand.Read(b)
	token := "ptza_" + hex.EncodeToString(b)
	s.grants.mu.Lock()
	if s.grants.byToken == nil {
		s.grants.byToken, s.grants.byConn = map[string]*auth.Key{}, map[string]string{}
	}
	if old, ok := s.grants.byConn[c.ID]; ok {
		delete(s.grants.byToken, old)
	}
	s.grants.byToken[token] = &auth.Key{Name: c.Name + " (add-on)", Scopes: can, Enabled: true}
	s.grants.byConn[c.ID] = token
	s.grants.mu.Unlock()
	port := 8080
	if s.Port != nil {
		port = s.Port()
	}
	s.Log.Info("controller access granted to an add-on connection", "connection", c.Name, "can", fmt.Sprint(can))
	return addon.ControllerAccess{URL: fmt.Sprintf("http://127.0.0.1:%d", port), Token: token, Can: can}
}

func (s *Server) grantFor(token string) *auth.Key {
	s.grants.mu.Lock()
	defer s.grants.mu.Unlock()
	return s.grants.byToken[token]
}

func (s *Server) revokeGrant(conn string) {
	s.grants.mu.Lock()
	defer s.grants.mu.Unlock()
	if t, ok := s.grants.byConn[conn]; ok {
		delete(s.grants.byToken, t)
		delete(s.grants.byConn, conn)
	}
}

// pruneGrants drops tokens of connections that no longer exist.
func (s *Server) pruneGrants(f *devices.File) {
	keep := map[string]bool{}
	for _, c := range f.Connections {
		keep[c.ID] = !c.Disabled
	}
	s.grants.mu.Lock()
	var gone []string
	for conn := range s.grants.byConn {
		if !keep[conn] {
			gone = append(gone, conn)
		}
	}
	s.grants.mu.Unlock()
	for _, c := range gone {
		s.revokeGrant(c)
	}
}
