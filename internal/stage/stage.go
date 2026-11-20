// Package stage carries short messages between screens: the booth tells
// the pulpit "5 minutes", the pulpit answers "Got it". A channel is just a
// name and a color; every widget set to the same channel is linked, both
// ways. Each channel also holds one countdown timer.
//
// State lives in stage.json in the data folder so a reload, a sleeping
// iPad or a restart shows the same message. Messages are kept for the
// current day only.
package stage

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	MaxText     = 280
	maxMessages = 200 // per channel per day
	maxChannels = 24
	maxName     = 40
)

var (
	ErrNotFound = errors.New("no such channel")
	ErrInvalid  = errors.New("invalid")
)

type Channel struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
	// OneAnswer: the first reply to a cue answers it for everyone (it
	// leaves every screen). Off: each screen answers for itself.
	OneAnswer bool `json:"oneAnswer,omitempty"`
}

type Message struct {
	ID   string    `json:"id"`
	Text string    `json:"text"`
	From string    `json:"from"` // screen name of the sender
	At   time.Time `json:"at"`
	// Seen lists the screens that tapped "seen" on this message.
	Seen []string `json:"seen"`
	// Replies: each screen's answer to this cue (its latest one).
	Replies []Reply `json:"replies"`
	// Done: answered for everyone (a channel with OneAnswer).
	Done bool `json:"done,omitempty"`
}

// Reply is one screen's answer to a cue ("👍 Got it").
type Reply struct {
	Screen string    `json:"screen"`
	Text   string    `json:"text"`
	At     time.Time `json:"at"`
}

// Timer counts down to EndsAt while running; paused, it keeps Left.
type Timer struct {
	Label   string    `json:"label"`
	Running bool      `json:"running"`
	EndsAt  time.Time `json:"endsAt"`
	Left    int       `json:"left"` // seconds, when paused
	Total   int       `json:"total"`
}

type channelData struct {
	Channel
	Messages []Message `json:"messages"`
	Timer    *Timer    `json:"timer,omitempty"`
}

type file struct {
	Channels []*channelData `json:"channels"`
}

// Screen is one connected display or sender, by its device name.
type Screen struct {
	Name   string `json:"name"`
	Online bool   `json:"online"`
}

// ChannelState is what a widget needs to draw a channel.
type ChannelState struct {
	Channel
	Messages []Message `json:"messages"`
	Timer    *Timer    `json:"timer"`
	Screens  []Screen  `json:"screens"`
}

type Store struct {
	path string
	now  func() time.Time

	mu    sync.Mutex
	data  file
	conns map[*Sub]struct{}
}

// Sub is one live listener (a browser's event stream).
type Sub struct {
	Screen   string
	Channels map[string]bool // empty: all channels
	C        chan struct{}   // poked on every change
}

func Open(path string) (*Store, error) {
	s := &Store{path: path, now: time.Now, conns: map[*Sub]struct{}{}}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		s.data.Channels = []*channelData{
			{Channel: Channel{ID: "pulpit", Name: "Pulpit", Color: "#F5A524"}},
			{Channel: Channel{ID: "worship", Name: "Worship", Color: "#60A5FA"}},
		}
		if err := s.saveLocked(); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(b, &s.data); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) saveLocked() error {
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// changed saves and wakes every listener.
func (s *Store) changed() error {
	err := s.saveLocked()
	s.notify()
	return err
}

// notify wakes every listener (presence changes need no save).
func (s *Store) notify() {
	for c := range s.conns {
		select {
		case c.C <- struct{}{}:
		default:
		}
	}
}

func (s *Store) find(id string) *channelData {
	for _, c := range s.data.Channels {
		if c.ID == id {
			return c
		}
	}
	return nil
}

// today drops messages from earlier days.
func (s *Store) today(c *channelData) {
	y, m, d := s.now().Date()
	kept := c.Messages[:0]
	for _, msg := range c.Messages {
		if yy, mm, dd := msg.At.Local().Date(); yy == y && mm == m && dd == d {
			kept = append(kept, msg)
		}
	}
	c.Messages = kept
}

var colorRe = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

func clean(name, color string) (string, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxName {
		return "", "", errorf("a channel name is 1-%d characters", maxName)
	}
	if color == "" {
		color = "#F5A524"
	}
	if !colorRe.MatchString(color) {
		return "", "", errorf("color must look like #F5A524")
	}
	return name, color, nil
}

func errorf(msg string, a ...any) error {
	return &invalid{msg: sprintf(msg, a...)}
}

type invalid struct{ msg string }

func (e *invalid) Error() string   { return e.msg }
func (e *invalid) Is(t error) bool { return t == ErrInvalid }

func slug(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "channel"
	}
	if len(out) > 24 {
		out = out[:24]
	}
	return out
}

func newID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// ------------------------------------------------------------- channels

func (s *Store) Channels() []Channel {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Channel, 0, len(s.data.Channels))
	for _, c := range s.data.Channels {
		out = append(out, c.Channel)
	}
	return out
}

func (s *Store) CreateChannel(name, color string) (Channel, error) {
	name, color, err := clean(name, color)
	if err != nil {
		return Channel{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.data.Channels) >= maxChannels {
		return Channel{}, errorf("%d channels is the limit", maxChannels)
	}
	for _, c := range s.data.Channels {
		if strings.EqualFold(c.Name, name) {
			return Channel{}, errorf("there is already a channel called %q", c.Name)
		}
	}
	id := slug(name)
	for n := 2; s.find(id) != nil; n++ {
		id = slug(name) + "-" + itoa(n)
	}
	c := &channelData{Channel: Channel{ID: id, Name: name, Color: color}}
	s.data.Channels = append(s.data.Channels, c)
	return c.Channel, s.changed()
}

// SetOneAnswer turns "the first reply answers it for everyone" on or off.
func (s *Store) SetOneAnswer(id string, on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.find(id)
	if c == nil {
		return ErrNotFound
	}
	c.OneAnswer = on
	return s.changed()
}

func (s *Store) UpdateChannel(id, name, color string) (Channel, error) {
	name, color, err := clean(name, color)
	if err != nil {
		return Channel{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.find(id)
	if c == nil {
		return Channel{}, ErrNotFound
	}
	for _, o := range s.data.Channels {
		if o != c && strings.EqualFold(o.Name, name) {
			return Channel{}, errorf("there is already a channel called %q", o.Name)
		}
	}
	c.Name, c.Color = name, color
	return c.Channel, s.changed()
}

func (s *Store) DeleteChannel(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, c := range s.data.Channels {
		if c.ID == id {
			s.data.Channels = append(s.data.Channels[:i], s.data.Channels[i+1:]...)
			return s.changed()
		}
	}
	return ErrNotFound
}

// ------------------------------------------------------------- messages

func (s *Store) Send(id, text, from string) (Message, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Message{}, errorf("the message is empty")
	}
	if utf8.RuneCountInString(text) > MaxText {
		return Message{}, errorf("messages are limited to %d characters", MaxText)
	}
	from = strings.TrimSpace(from)
	if utf8.RuneCountInString(from) > maxName {
		from = string([]rune(from)[:maxName])
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.find(id)
	if c == nil {
		return Message{}, ErrNotFound
	}
	s.today(c)
	m := Message{ID: newID(), Text: text, From: from, At: s.now().UTC(), Seen: []string{}, Replies: []Reply{}}
	c.Messages = append(c.Messages, m)
	if len(c.Messages) > maxMessages {
		c.Messages = c.Messages[len(c.Messages)-maxMessages:]
	}
	return m, s.changed()
}

// Clear empties a channel's messages (the stage screens go blank).
func (s *Store) Clear(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.find(id)
	if c == nil {
		return ErrNotFound
	}
	c.Messages = nil
	return s.changed()
}

// Seen records that screen read message msgID.
func (s *Store) Seen(id, msgID, screen string) error {
	screen = strings.TrimSpace(screen)
	if screen == "" {
		return errorf("which screen saw it?")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.find(id)
	if c == nil {
		return ErrNotFound
	}
	for i := range c.Messages {
		if c.Messages[i].ID != msgID {
			continue
		}
		for _, n := range c.Messages[i].Seen {
			if n == screen {
				return nil
			}
		}
		c.Messages[i].Seen = append(c.Messages[i].Seen, screen)
		return s.changed()
	}
	return errorf("that message is gone")
}

// Reply records screen's answer to cue msgID (it also counts as seen).
// One screen has one answer; a new one replaces it. On a OneAnswer
// channel the first answer makes the cue Done for everyone.
func (s *Store) Reply(id, msgID, screen, text string) (Message, error) {
	screen, text = strings.TrimSpace(screen), strings.TrimSpace(text)
	if screen == "" {
		return Message{}, errorf("which screen is answering?")
	}
	if text == "" {
		return Message{}, errorf("the reply is empty")
	}
	if utf8.RuneCountInString(text) > MaxText {
		return Message{}, errorf("replies are limited to %d characters", MaxText)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.find(id)
	if c == nil {
		return Message{}, ErrNotFound
	}
	for i := range c.Messages {
		m := &c.Messages[i]
		if m.ID != msgID {
			continue
		}
		r := Reply{Screen: screen, Text: text, At: s.now().UTC()}
		replaced := false
		for j := range m.Replies {
			if m.Replies[j].Screen == screen {
				m.Replies[j], replaced = r, true
			}
		}
		if !replaced {
			m.Replies = append(m.Replies, r)
		}
		seen := false
		for _, n := range m.Seen {
			seen = seen || n == screen
		}
		if !seen {
			m.Seen = append(m.Seen, screen)
		}
		if c.OneAnswer {
			m.Done = true
		}
		return *m, s.changed()
	}
	return Message{}, errorf("that cue is gone")
}

// ---------------------------------------------------------------- timer

// TimerOp: start (label, seconds), pause, resume, add (seconds), stop.
type TimerOp struct {
	Op      string `json:"op"`
	Label   string `json:"label"`
	Seconds int    `json:"seconds"`
}

func (s *Store) Timer(id string, op TimerOp) (*Timer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.find(id)
	if c == nil {
		return nil, ErrNotFound
	}
	now := s.now().UTC()
	t := c.Timer
	switch op.Op {
	case "start":
		if op.Seconds < 1 || op.Seconds > 6*3600 {
			return nil, errorf("a timer runs 1 second to 6 hours")
		}
		label := strings.TrimSpace(op.Label)
		if utf8.RuneCountInString(label) > maxName {
			label = string([]rune(label)[:maxName])
		}
		t = &Timer{Label: label, Running: true, EndsAt: now.Add(time.Duration(op.Seconds) * time.Second), Total: op.Seconds}
	case "pause":
		if t == nil || !t.Running {
			return t, nil
		}
		t.Left = int(t.EndsAt.Sub(now).Round(time.Second) / time.Second)
		t.Running = false
	case "resume":
		if t == nil || t.Running {
			return t, nil
		}
		t.EndsAt = now.Add(time.Duration(t.Left) * time.Second)
		t.Running = true
	case "add":
		if t == nil {
			return nil, errorf("no timer is running")
		}
		d := time.Duration(op.Seconds) * time.Second
		if t.Running {
			t.EndsAt = t.EndsAt.Add(d)
		} else {
			t.Left += op.Seconds
		}
		t.Total += op.Seconds
	case "stop":
		t = nil
	default:
		return nil, errorf("timer op must be start, pause, resume, add or stop")
	}
	c.Timer = t
	return t, s.changed()
}

// ---------------------------------------------------------------- live

// Subscribe registers a listener; call the returned func when it leaves.
func (s *Store) Subscribe(screen string, channels []string) (*Sub, func()) {
	sub := &Sub{Screen: strings.TrimSpace(screen), Channels: map[string]bool{}, C: make(chan struct{}, 1)}
	for _, c := range channels {
		if c = strings.TrimSpace(c); c != "" {
			sub.Channels[c] = true
		}
	}
	s.mu.Lock()
	s.conns[sub] = struct{}{}
	s.notify() // presence changed
	s.mu.Unlock()
	return sub, func() {
		s.mu.Lock()
		delete(s.conns, sub)
		s.notify()
		s.mu.Unlock()
	}
}

// State is the channels sub listens to (all when it named none), with
// who is online on each.
func (s *Store) State(sub *Sub) []ChannelState {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []ChannelState{}
	for _, c := range s.data.Channels {
		if sub != nil && len(sub.Channels) > 0 && !sub.Channels[c.ID] {
			continue
		}
		s.today(c)
		st := ChannelState{Channel: c.Channel, Messages: append([]Message{}, c.Messages...), Timer: c.Timer}
		names := map[string]bool{}
		for o := range s.conns {
			if o.Screen != "" && (len(o.Channels) == 0 || o.Channels[c.ID]) {
				names[o.Screen] = true
			}
		}
		for n := range names {
			st.Screens = append(st.Screens, Screen{Name: n, Online: true})
		}
		sort.Slice(st.Screens, func(i, j int) bool { return st.Screens[i].Name < st.Screens[j].Name })
		if st.Screens == nil {
			st.Screens = []Screen{}
		}
		out = append(out, st)
	}
	return out
}
