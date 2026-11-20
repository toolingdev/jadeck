// Package settings holds everything set up from the Settings screen:
// cameras, video, network, access and updates. The app owns the file
// (settings.json in the data folder); nobody is expected to edit it by
// hand. Camera passwords live only here, on this PC, and are never
// sent back to a browser.
package settings

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Camera is one camera the controller drives.
type Camera struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"` // "ptzoptics"
	Host      string `json:"host"`
	ViscaPort int    `json:"viscaPort"`
	HTTPPort  int    `json:"httpPort"`
	RTSPPort  int    `json:"rtspPort"`
	StreamHD  string `json:"streamHd"` // RTSP path, "/1" on PTZOptics
	StreamSD  string `json:"streamSd"` // "/2"
	User      string `json:"user"`
	Pass      string `json:"pass,omitempty"`
	Video     bool   `json:"video"`       // stream it through the gateway
	Presets   string `json:"presetsFile"` // preset names DB (file name in the data dir)
}

// Video is the built-in stream gateway (MediaMTX).
type Video struct {
	Managed  bool   `json:"managed"`  // the app runs MediaMTX itself
	External string `json:"external"` // own gateway: base host, e.g. "10.1.10.20" (when !Managed)
	// Feeds are pictures dashboards can show besides the cameras (which
	// add their own): sent here by vMix / OBS / an encoder (SRT push), or
	// pulled from an address (RTSP / SRT).
	Feeds []Feed `json:"feeds"`

	// Before feeds: two fixed SRT inputs. Read once, turned into feeds.
	Program    bool `json:"program,omitempty"`
	Confidence bool `json:"confidence,omitempty"`
}

// Feed is one video feed. ID is its gateway path (and the SRT stream id
// "publish:<id>" senders use), so it never changes after creation.
type Feed struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Source string `json:"source"`        // push | pull
	URL    string `json:"url,omitempty"` // pull: rtsp://… or srt://…
}

// MigrateFeeds turns the old Program / Confidence switches into feeds
// with the same stream ids, so a configured vMix keeps working.
func (v *Video) MigrateFeeds() {
	if v.Feeds != nil {
		v.Program, v.Confidence = false, false
		return
	}
	v.Feeds = []Feed{}
	if v.Program {
		v.Feeds = append(v.Feeds, Feed{ID: "program", Name: "Program", Source: "push"})
	}
	if v.Confidence {
		v.Feeds = append(v.Feeds, Feed{ID: "confidence", Name: "Slides", Source: "push"})
	}
	v.Program, v.Confidence = false, false
}

// Feed finds a feed by id.
func (v Video) Feed(id string) (Feed, bool) {
	for _, f := range v.Feeds {
		if f.ID == id {
			return f, true
		}
	}
	return Feed{}, false
}

// System is network, access and updates.
type System struct {
	Port         int      `json:"port"`
	Name         string   `json:"name"` // answers as <name>.local
	TrustLAN     bool     `json:"trustLan"`
	TrustedCIDRs []string `json:"trustedCidrs"`
	UpdateRepo   string   `json:"updateRepo"`
	UpdateToken  string   `json:"updateToken,omitempty"`
	Channel      string   `json:"channel"` // stable | beta
	AutoUpdate   bool     `json:"autoUpdate"`
}

type Settings struct {
	Cameras   []Camera `json:"cameras"`
	MainID    string   `json:"main"`
	Video     Video    `json:"video"`
	System    System   `json:"system"`
	SetupDone bool     `json:"setupDone"`
}

// Main returns the main camera, or nil when none is set up.
func (s *Settings) Main() *Camera {
	for i := range s.Cameras {
		if s.Cameras[i].ID == s.MainID {
			return &s.Cameras[i]
		}
	}
	if len(s.Cameras) > 0 {
		return &s.Cameras[0]
	}
	return nil
}

func (s *Settings) Camera(id string) *Camera {
	for i := range s.Cameras {
		if s.Cameras[i].ID == id {
			return &s.Cameras[i]
		}
	}
	return nil
}

func (s Settings) clone() Settings {
	c := s
	c.Cameras = append([]Camera(nil), s.Cameras...)
	c.System.TrustedCIDRs = append([]string(nil), s.System.TrustedCIDRs...)
	return c
}

// Defaults fills unset camera fields.
func (c *Camera) Defaults() {
	if c.Type == "" {
		c.Type = "ptzoptics"
	}
	if c.ViscaPort == 0 {
		c.ViscaPort = 5678
	}
	if c.HTTPPort == 0 {
		c.HTTPPort = 80
	}
	if c.RTSPPort == 0 {
		c.RTSPPort = 554
	}
	if c.StreamHD == "" {
		c.StreamHD = "/1"
	}
	if c.StreamSD == "" {
		c.StreamSD = "/2"
	}
}

// Validate checks a camera as typed into the Add camera form.
func (c *Camera) Validate() error {
	c.Name = strings.TrimSpace(c.Name)
	c.Host = strings.TrimSpace(c.Host)
	c.User = strings.TrimSpace(c.User)
	switch {
	case c.Name == "" || len(c.Name) > 40:
		return errors.New("give the camera a name (up to 40 characters)")
	case c.Host == "":
		return errors.New("type the camera's IP address")
	case strings.ContainsAny(c.Host, "/ :?#@"):
		if _, err := netip.ParseAddr(c.Host); err != nil {
			return errors.New("the IP address should look like 10.1.10.110 (no http://, no port)")
		}
	}
	for _, p := range []struct {
		n string
		v int
	}{{"control (VISCA)", c.ViscaPort}, {"web", c.HTTPPort}, {"RTSP", c.RTSPPort}} {
		if p.v < 1 || p.v > 65535 {
			return fmt.Errorf("%s port must be 1-65535", p.n)
		}
	}
	for _, p := range []*string{&c.StreamHD, &c.StreamSD} {
		*p = strings.TrimSpace(*p)
		if !strings.HasPrefix(*p, "/") {
			*p = "/" + *p
		}
	}
	return nil
}

// HTTPBase is the camera's CGI/snapshot base URL.
func (c *Camera) HTTPBase() string {
	if c.HTTPPort == 80 {
		return "http://" + c.Host
	}
	return "http://" + net.JoinHostPort(c.Host, fmt.Sprint(c.HTTPPort))
}

// RTSP returns the camera's stream URL (credentials included when set).
func (c *Camera) RTSP(path string) string {
	auth := ""
	if c.User != "" {
		auth = c.User + ":" + c.Pass + "@"
	}
	return fmt.Sprintf("rtsp://%s%s%s", auth, net.JoinHostPort(c.Host, fmt.Sprint(c.RTSPPort)), path)
}

func NewID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// ValidateSystem checks the network/access/update form.
func (s *System) Validate() error {
	if s.Port < 1 || s.Port > 65535 {
		return errors.New("port must be 1-65535")
	}
	s.Name = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s.Name), ".local")))
	for _, r := range s.Name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return errors.New("the network name may use letters, numbers and dashes only")
		}
	}
	var out []string
	for _, c := range s.TrustedCIDRs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if !strings.Contains(c, "/") {
			if a, err := netip.ParseAddr(c); err == nil {
				c = netip.PrefixFrom(a, a.BitLen()).String()
			}
		}
		if _, err := netip.ParsePrefix(c); err != nil {
			return fmt.Errorf("%q is not a network (use 10.2.0.0/16 or a single IP)", c)
		}
		out = append(out, c)
	}
	s.TrustedCIDRs = out
	s.UpdateRepo = strings.TrimSpace(s.UpdateRepo)
	if s.UpdateRepo != "" && strings.Count(s.UpdateRepo, "/") != 1 {
		return errors.New("release source should look like owner/name")
	}
	if s.Channel != "beta" {
		s.Channel = "stable"
	}
	return nil
}

// Listener runs after a saved change.
type Listener func(old, next Settings)

// Store is the settings file plus change listeners.
type Store struct {
	path string
	mu   sync.Mutex
	s    Settings
	subs []Listener
}

// Path is the settings file (its folder is the data folder).
func (s *Store) Path() string { return s.path }

// Open loads path; when the file does not exist, seed is used and saved.
func Open(path string, seed func() Settings) (*Store, error) {
	st := &Store{path: path}
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(b, &st.s); err != nil {
			return nil, fmt.Errorf("settings file %s unreadable: %w", path, err)
		}
	case errors.Is(err, os.ErrNotExist):
		st.s = seed()
		if err := st.save(); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}
	for i := range st.s.Cameras {
		st.s.Cameras[i].Defaults()
	}
	st.s.Video.MigrateFeeds()
	if st.s.System.Port == 0 {
		st.s.System.Port = 8080
	}
	if st.s.System.Channel == "" {
		st.s.System.Channel = "stable"
	}
	return st, nil
}

func (st *Store) save() error {
	b, err := json.MarshalIndent(st.s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(st.path), 0o755); err != nil {
		return err
	}
	tmp := st.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil { // holds camera passwords
		return err
	}
	return os.Rename(tmp, st.path)
}

// Get returns a copy.
func (st *Store) Get() Settings {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.s.clone()
}

// Update edits a copy; when fn returns nil it is saved and listeners run.
func (st *Store) Update(fn func(*Settings) error) (Settings, error) {
	st.mu.Lock()
	old := st.s.clone()
	next := st.s.clone()
	if err := fn(&next); err != nil {
		st.mu.Unlock()
		return old, err
	}
	st.s = next
	if err := st.save(); err != nil {
		st.s = old
		st.mu.Unlock()
		return old, err
	}
	subs := append([]Listener(nil), st.subs...)
	st.mu.Unlock()
	for _, f := range subs {
		f(old, next.clone())
	}
	return next.clone(), nil
}

// OnChange registers a listener called after every saved update.
func (st *Store) OnChange(f Listener) {
	st.mu.Lock()
	st.subs = append(st.subs, f)
	st.mu.Unlock()
}
