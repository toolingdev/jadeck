package api

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"ptz/internal/camera"

	"net/http"
)

func statusMsg(cam string, st camera.Status) []byte {
	b, _ := json.Marshal(struct {
		T   string `json:"t"`
		Cam string `json:"cam"`
		camera.Status
	}{"status", cam, st})
	return b
}

// hub fans status out to every WebSocket client. A slow client's queue
// drops messages instead of ever blocking the camera goroutines.
// Each client hears only the camera it is driving.
type hub struct {
	mu      sync.Mutex
	clients map[chan []byte]string
}

func newHub() *hub { return &hub{clients: map[chan []byte]string{}} }

func (h *hub) add(cam string) chan []byte {
	c := make(chan []byte, 16)
	h.mu.Lock()
	h.clients[c] = cam
	h.mu.Unlock()
	return c
}

func (h *hub) remove(c chan []byte) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
}

func (h *hub) broadcast(cam string, msg []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c, want := range h.clients {
		if want != cam {
			continue
		}
		select {
		case c <- msg:
		default:
		}
	}
}

var ownerSeq atomic.Int64

// inbound WebSocket message. Numbers arrive as JSON numbers; anything
// non-numeric fails to decode and the message is skipped (the Python
// build closed the socket on it).
type wsMsg struct {
	T     string   `json:"t"`
	Pan   float64  `json:"pan"`
	Tilt  float64  `json:"tilt"`
	Zoom  float64  `json:"zoom"`
	Speed *float64 `json:"speed"`
}

func unit(v float64) int {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	}
	return 0
}

func (s *Server) ws(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Same-origin pages only by default; LAN clients use the host
		// they loaded the page from.
	})
	if err != nil {
		return
	}
	defer c.CloseNow()
	u := s.Cams.Get(r.URL.Query().Get("cam"))
	if u == nil {
		c.Close(4004, "no such camera — set one up in Settings → Cameras")
		return
	}
	camID := u.Cfg.ID
	owner := fmt.Sprintf("ws-%d", ownerSeq.Add(1))
	t0 := time.Now()
	s.Log.Debug("control socket opened", "camera", camID, "client", clientIP(r), "id", owner)
	defer func() {
		s.Log.Debug("control socket closed", "camera", camID, "client", clientIP(r), "id", owner, "after", time.Since(t0).Round(time.Second).String())
	}()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	out := s.hub.add(camID)
	defer s.hub.remove(out)
	// Whatever happens, a departing driver stops the camera now rather
	// than waiting for the dead-man switch.
	defer u.Svc.ReleaseOwner(owner)

	go func() {
		defer cancel()
		if err := c.Write(ctx, websocket.MessageText, statusMsg(camID, u.Svc.Snapshot())); err != nil {
			return
		}
		swap := time.NewTicker(2 * time.Second)
		defer swap.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-swap.C:
				if s.Cams.Get(camID) != u { // camera edited or removed: client reconnects
					c.Close(websocket.StatusServiceRestart, "camera settings changed")
					return
				}
			case m := <-out:
				if err := c.Write(ctx, websocket.MessageText, m); err != nil {
					return
				}
			}
		}
	}()

	c.SetReadLimit(4096)
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		var m wsMsg
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		switch m.T {
		case "drive":
			speed := 40
			if m.Speed != nil {
				speed = int(*m.Speed)
			}
			u.Svc.SetDrive(camera.DriveVector{
				Pan: unit(m.Pan), Tilt: unit(m.Tilt), Zoom: unit(m.Zoom), Speed: speed,
			}, owner)
		case "stop":
			u.Svc.SetDrive(camera.DriveVector{}, owner)
		}
	}
}
