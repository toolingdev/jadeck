package sim

// Switcher answers like vMix 29: the Web Controller API on HTTP 8088
// (GET /api → state XML, /api?Function=… runs a function) and the TCP
// API on 8099 (VERSION greeting, CRLF lines, XML / XMLTEXT / TALLY /
// FUNCTION / SUBSCRIBE TALLY|ACTS / PING), consistent with the vMix
// add-on's test vMix (fake_test.go). It also draws the program picture
// for the program feed: transitions crossfade, the T-bar blends, fade to
// black darkens, and title / logo overlays are drawn on top.

import (
	"bufio"
	"fmt"
	"html"
	"image"
	"image/color"
	"image/draw"
	"log/slog"
	"math"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/font"
)

// Input is one switcher source: a camera, the slides, a clip…
type Input struct {
	Title string
	Type  string // Capture | PowerPoint | Video | GT | VideoList | Image | Audio | Colour
	Frame func() *image.RGBA
}

// vinput is an input and everything vMix's XML says about it.
type vinput struct {
	Input
	key                string
	state              string // Running | Paused | Completed
	audio              bool
	muted, solo        bool
	volume, balance    float64
	buses              string  // "M,A"
	level              float64 // loudness of the source, 0..1 amplitude
	fields             [][2]string
	list               []string
	selected           int
	duration, position float64 // ms
	loop               bool
	playFrom           time.Time // when Running: position counts from here
}

type vbus struct {
	name         string
	volume       float64
	muted, solo  bool
	sendToMaster bool
}

type vsub struct {
	tally, acts bool
	wmu         sync.Mutex
}

// Switcher is the simulated vMix.
type Switcher struct {
	mu          sync.Mutex
	inputs      []*vinput
	preview     int // 1-based
	active      int
	fadeFrom    int
	fadeStart   time.Time
	fadeDur     time.Duration
	overlays    [17]int
	ftb         bool
	ftbAt       time.Time
	transitions [4]struct {
		effect string
		ms     int
	}
	fader                                 int // T-bar 0..255
	tbarFrom                              int // where the T-bar's last transition ended
	streaming, recording, external, multi bool
	streamAt, recAt, externalAt, multiAt  time.Time
	fullscreen, playList                  bool
	masterVol, headphones                 float64
	masterMuted                           bool
	buses                                 map[string]*vbus
	subs                                  map[net.Conn]*vsub
	start                                 time.Time
	fonts                                 fonts
	textMu                                sync.Mutex
	frames                                map[string]*image.RGBA // drawn slates and overlays
	Log                                   *slog.Logger
}

const vmixVersion = "29.0.0.50"

func NewSwitcher(inputs []Input) *Switcher {
	s := &Switcher{active: 1, preview: min(2, len(inputs)), streaming: true, masterVol: 100, headphones: 74,
		buses: map[string]*vbus{}, subs: map[net.Conn]*vsub{}, start: time.Now(), frames: map[string]*image.RGBA{},
		Log: slog.Default()}
	s.streamAt = s.start
	for i, t := range []struct {
		e  string
		ms int
	}{{"Fade", 500}, {"Zoom", 1000}, {"Merge", 1000}, {"Stinger1", 1500}} {
		s.transitions[i].effect, s.transitions[i].ms = t.e, t.ms
	}
	for _, b := range []struct {
		id, name string
		vol      float64
	}{{"A", "Stream", 80}, {"B", "Record", 80}, {"C", "Lobby", 60}, {"D", "Hearing loop", 70}, {"E", "", 100}, {"F", "", 100}, {"G", "", 100}} {
		s.buses[b.id] = &vbus{name: b.name, volume: b.vol}
	}
	for _, in := range inputs {
		s.add(in)
	}
	return s
}

func (s *Switcher) add(in Input) *vinput {
	n := len(s.inputs) + 1
	v := &vinput{Input: in, key: fmt.Sprintf("5f1a0000-0000-4000-8000-%012d", n), state: "Running", volume: 100, buses: "M"}
	switch in.Type {
	case "PowerPoint":
		v.audio, v.muted, v.level = true, true, 0.15
	}
	s.inputs = append(s.inputs, v)
	return v
}

// AddServiceInputs adds what a typical live-production vMix has besides cameras and
// slides: a countdown, titles, clips, walk-in music, a logo, the stage
// mix and black. Call before serving.
func (s *Switcher) AddServiceInputs() {
	s.fonts = loadFonts()
	add := func(title, typ string, set func(v *vinput)) {
		v := s.add(Input{Title: title, Type: typ})
		v.Frame = func() *image.RGBA { return s.inputFrame(v) }
		if set != nil {
			set(v)
		}
	}
	add("Countdown", "Video", func(v *vinput) {
		v.state, v.duration, v.audio, v.volume, v.level = "Paused", 300000, true, 80, 0.2
	})
	add("Lower third", "GT", func(v *vinput) {
		v.fields = [][2]string{{"Name.Text", "Guest speaker"}, {"Line2.Text", "Visiting today"}}
	})
	add("Offering video", "Video", func(v *vinput) {
		v.state, v.duration, v.audio, v.volume, v.level = "Paused", 185000, true, 80, 0.3
	})
	add("Walk-in music", "VideoList", func(v *vinput) {
		v.state, v.duration, v.audio, v.volume, v.level, v.buses = "Paused", 241000, true, 70, 0.25, "M,A"
		v.list = []string{`D:\Music\Walk-in\01 Morning light.mp3`, `D:\Music\Walk-in\02 Still waters.mp3`,
			`D:\Music\Walk-in\03 Open fields.mp3`, `D:\Music\Walk-in\04 Homeward.mp3`}
		v.selected = 1
	})
	add("Scripture", "GT", func(v *vinput) {
		v.fields = [][2]string{{"Reference.Text", "Psalm 23:1"}, {"Verse.Text", "The Lord is my shepherd; I shall not want."}}
	})
	add("Logo bug", "Image", nil)
	add("Stage mix", "Audio", func(v *vinput) {
		v.audio, v.volume, v.level, v.buses = true, 100, 0.32, "M,A,B"
	})
	add("Black", "Colour", nil)
	s.recording, s.recAt = true, time.Now().Add(-38*time.Second)
}

// Tally: 1 live, 2 preview, 0 neither, for input n.
func (s *Switcher) Tally(n int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch n {
	case s.active:
		return 1
	case s.preview:
		return 2
	}
	return 0
}

// find resolves an input like vMix: number, key or title (s.mu held).
func (s *Switcher) find(v string) int {
	if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= len(s.inputs) {
		return n
	}
	for i, in := range s.inputs {
		if strings.EqualFold(in.Title, v) || strings.EqualFold(in.key, v) {
			return i + 1
		}
	}
	return 0
}

// advance moves media positions on (s.mu held).
func (s *Switcher) advance(now time.Time) {
	for _, in := range s.inputs {
		if in.state != "Running" || in.duration <= 0 || in.playFrom.IsZero() {
			continue
		}
		pos := in.position + float64(now.Sub(in.playFrom).Milliseconds())
		in.playFrom = now
		if pos >= in.duration {
			switch {
			case in.loop:
				pos = math.Mod(pos, in.duration)
			case len(in.list) > 0:
				in.selected = in.selected%len(in.list) + 1
				pos = 0
			default:
				pos, in.state = in.duration, "Completed"
			}
		}
		in.position = pos
	}
}

func (in *vinput) play(now time.Time) {
	if in.state == "Completed" {
		in.position = 0
	}
	in.state, in.playFrom = "Running", now
}

func (in *vinput) pause() { in.state = "Paused" }

var errNoFunction = fmt.Errorf("No suitable Function could be found.")

// function runs a vMix function (s.mu not held).
func (s *Switcher) function(q map[string][]string) error {
	get := func(k string) string {
		if v := q[k]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.advance(now)
	name := get("Function")
	fn := strings.ToLower(name)
	if !vmixFunctions[fn] && !vmixEffects[fn] {
		return errNoFunction
	}
	value := get("Value")
	n := 0
	if v := get("Input"); v != "" {
		if n = s.find(v); n == 0 {
			if v == "0" {
				n = s.preview
			} else if v == "-1" {
				n = s.active
			} else {
				return fmt.Errorf("Input %s not found", v)
			}
		}
	}
	in := func() *vinput {
		if n >= 1 && n <= len(s.inputs) {
			return s.inputs[n-1]
		}
		return nil
	}
	num := func() float64 {
		v, _, _ := strings.Cut(value, ",")
		f, _ := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f
	}
	before := s.state()
	transition := func(effect string, ms int) {
		target := s.preview
		if n != 0 {
			target = n
		}
		if target == s.active {
			return
		}
		if d, err := strconv.Atoi(get("Duration")); err == nil && d > 0 {
			ms = d
		}
		if effect == "cut" {
			ms = 0
		}
		s.fadeFrom, s.fadeStart, s.fadeDur = s.active, now, time.Duration(ms)*time.Millisecond
		s.preview, s.active = s.active, target
		s.fader, s.tbarFrom = 0, 0
	}
	switch {
	case fn == "previewinput":
		if n == 0 {
			return fmt.Errorf("no input %q", get("Input"))
		}
		s.preview = n
	case fn == "previewinputnext":
		s.preview = s.preview%len(s.inputs) + 1
	case fn == "previewinputprevious":
		s.preview = (s.preview+len(s.inputs)-2)%len(s.inputs) + 1
	case fn == "activeinput", fn == "cutdirect":
		if n == 0 {
			return fmt.Errorf("no input %q", get("Input"))
		}
		transition("cut", 0)
	case fn == "cut":
		transition("cut", 0)
	case vmixEffects[fn]:
		transition(fn, 1000)
	case strings.HasPrefix(fn, "transition") && len(fn) == len("transition1"):
		t := s.transitions[int(fn[len(fn)-1]-'1')%4]
		transition(strings.ToLower(t.effect), t.ms)
	case strings.HasPrefix(fn, "stinger"):
		transition("stinger", 1500)
	case fn == "quickplay":
		transition("fade", 1000)
	case fn == "fadetoblack":
		s.ftb, s.ftbAt = !s.ftb, now
	case fn == "setfader":
		s.setFader(int(num()), now)
	case fn == "startstreaming", fn == "stopstreaming", fn == "startstopstreaming":
		s.output(&s.streaming, &s.streamAt, fn, now)
	case fn == "startrecording", fn == "stoprecording", fn == "startstoprecording":
		s.output(&s.recording, &s.recAt, fn, now)
	case fn == "startexternal", fn == "stopexternal", fn == "startstopexternal":
		s.output(&s.external, &s.externalAt, fn, now)
	case fn == "startmulticorder", fn == "stopmulticorder", fn == "startstopmulticorder":
		s.output(&s.multi, &s.multiAt, fn, now)
	case fn == "fullscreenon", fn == "fullscreenoff", fn == "fullscreen":
		s.fullscreen = fn == "fullscreenon" || (fn == "fullscreen" && !s.fullscreen)
	case fn == "startplaylist", fn == "stopplaylist":
		s.playList = fn == "startplaylist"
	case fn == "setmastervolume", fn == "setmastervolumefade":
		s.masterVol = clamp(num(), 0, 100)
	case fn == "setheadphonesvolume":
		s.headphones = clamp(num(), 0, 100)
	case fn == "masteraudio", fn == "masteraudioon", fn == "masteraudiooff":
		s.masterMuted = fn == "masteraudiooff" || (fn == "masteraudio" && !s.masterMuted)
	case strings.HasPrefix(fn, "setbus") && strings.Contains(fn, "volume"):
		if b := s.buses[strings.ToUpper(fn[6:7])]; b != nil {
			b.volume = clamp(num(), 0, 100)
		}
	case fn == "busxaudio", fn == "busxaudioon", fn == "busxaudiooff":
		if b := s.buses[strings.ToUpper(value)]; b != nil {
			b.muted = fn == "busxaudiooff" || (fn == "busxaudio" && !b.muted)
		}
	case fn == "busxsolo", fn == "busxsoloon", fn == "busxsolooff":
		if b := s.buses[strings.ToUpper(value)]; b != nil {
			b.solo = fn == "busxsoloon" || (fn == "busxsolo" && !b.solo)
		}
	case fn == "busxsendtomaster", fn == "busxsendtomasteron", fn == "busxsendtomasteroff":
		if b := s.buses[strings.ToUpper(value)]; b != nil {
			b.sendToMaster = fn == "busxsendtomasteron" || (fn == "busxsendtomaster" && !b.sendToMaster)
		}
	case fn == "overlayinputalloff":
		for i := range s.overlays {
			s.overlays[i] = 0
		}
	case strings.HasPrefix(fn, "overlayinput"):
		rest := fn[len("overlayinput"):]
		ch := 0
		for len(rest) > 0 && rest[0] >= '0' && rest[0] <= '9' {
			ch = ch*10 + int(rest[0]-'0')
			rest = rest[1:]
		}
		if ch < 1 || ch > 16 {
			return errNoFunction
		}
		switch rest {
		case "in":
			s.overlays[ch] = n
		case "out", "off":
			s.overlays[ch] = 0
		case "", "zoom", "preview":
			if s.overlays[ch] != 0 && (n == 0 || s.overlays[ch] == n) {
				s.overlays[ch] = 0
			} else if n != 0 {
				s.overlays[ch] = n
			}
		}
	}
	// input-level functions
	if v := in(); v != nil {
		switch fn {
		case "setvolume", "setvolumefade":
			v.volume = clamp(num(), 0, 100)
		case "setbalance":
			v.balance = clamp(num(), -1, 1)
		case "audio", "audioon", "audiooff":
			v.muted = fn == "audiooff" || (fn == "audio" && !v.muted)
		case "solo", "soloon", "solooff":
			v.solo = fn == "soloon" || (fn == "solo" && !v.solo)
		case "audiobus", "audiobuson", "audiobusoff":
			bus := strings.ToUpper(value)
			has := strings.Contains(","+v.buses+",", ","+bus+",")
			on := fn == "audiobuson" || (fn == "audiobus" && !has)
			var keep []string
			for _, b := range strings.Split(v.buses, ",") {
				if b != "" && b != bus {
					keep = append(keep, b)
				}
			}
			if on {
				keep = append(keep, bus)
			}
			v.buses = strings.Join(keep, ",")
		case "settext":
			for i, f := range v.fields {
				if f[0] == get("SelectedName") || strconv.Itoa(i) == get("SelectedIndex") {
					v.fields[i][1] = value
				}
			}
		case "play":
			v.play(now)
		case "pause":
			v.pause()
		case "playpause":
			if v.state == "Running" {
				v.pause()
			} else {
				v.play(now)
			}
		case "restart":
			v.position = 0
			if v.state == "Running" {
				v.playFrom = now
			} else if v.state == "Completed" {
				v.state = "Paused"
			}
		case "setposition":
			v.position = clamp(num(), 0, v.duration)
			if v.state == "Running" {
				v.playFrom = now
			}
		case "loop", "loopon", "loopoff":
			v.loop = fn == "loopon" || (fn == "loop" && !v.loop)
		case "nextitem", "previousitem", "selectindex":
			if len(v.list) > 0 {
				switch fn {
				case "nextitem":
					v.selected = v.selected%len(v.list) + 1
				case "previousitem":
					v.selected = (v.selected+len(v.list)-2)%len(v.list) + 1
				default:
					if i := int(num()); i >= 1 && i <= len(v.list) {
						v.selected = i
					}
				}
				v.position = 0
				if v.state == "Running" {
					v.playFrom = now
				}
			}
		}
	}
	s.Log.Info("switcher", "function", name, "input", get("Input"), "value", value)
	s.notify(before)
	return nil
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

// output starts or stops an output (s.mu held).
func (s *Switcher) output(on *bool, at *time.Time, fn string, now time.Time) {
	want := strings.HasPrefix(fn, "start") && !strings.HasPrefix(fn, "startstop")
	if strings.HasPrefix(fn, "startstop") {
		want = !*on
	}
	if want && !*on {
		*at = now
	}
	*on = want
}

// setFader moves the T-bar: the program blends towards preview and the
// transition completes at the far end (s.mu held).
func (s *Switcher) setFader(v int, now time.Time) {
	v = max(0, min(255, v))
	s.fader = v
	if abs(v-s.tbarFrom) >= 255 {
		s.fadeFrom, s.fadeDur = 0, 0
		s.preview, s.active = s.active, s.preview
		s.tbarFrom = v
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// ------------------------------------------------------------ pushes

// vstate is what TCP subscribers hear about.
type vstate struct {
	active, preview                  int
	overlays                         [17]int
	rec, stream, ext, multi, ftb, mm bool
	playing, muted                   []bool
}

func (s *Switcher) state() vstate {
	st := vstate{active: s.active, preview: s.preview, overlays: s.overlays, rec: s.recording, stream: s.streaming,
		ext: s.external, multi: s.multi, ftb: s.ftb, mm: s.masterMuted}
	for _, in := range s.inputs {
		st.playing = append(st.playing, in.state == "Running" && in.duration > 0)
		st.muted = append(st.muted, in.muted)
	}
	return st
}

func bit(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// notify pushes TALLY / ACTS to subscribers for what changed (s.mu held).
func (s *Switcher) notify(before vstate) {
	after := s.state()
	var acts []string
	if before.active != after.active {
		acts = append(acts, fmt.Sprintf("Input %d 0", before.active), fmt.Sprintf("Input %d 1", after.active))
	}
	if before.preview != after.preview {
		acts = append(acts, fmt.Sprintf("InputPreview %d 0", before.preview), fmt.Sprintf("InputPreview %d 1", after.preview))
	}
	for i := 1; i <= 16; i++ {
		if before.overlays[i] != after.overlays[i] {
			if before.overlays[i] != 0 {
				acts = append(acts, fmt.Sprintf("Overlay%d %d 0", i, before.overlays[i]))
			}
			if after.overlays[i] != 0 {
				acts = append(acts, fmt.Sprintf("Overlay%d %d 1", i, after.overlays[i]))
			}
		}
	}
	for _, c := range []struct {
		name string
		a, b bool
	}{{"Recording", before.rec, after.rec}, {"Streaming", before.stream, after.stream}, {"External", before.ext, after.ext},
		{"MultiCorder", before.multi, after.multi}, {"FadeToBlack", before.ftb, after.ftb}, {"MasterAudio", !before.mm, !after.mm}} {
		if c.a != c.b {
			acts = append(acts, c.name+" "+bit(c.b))
		}
	}
	for i := range after.playing {
		if i < len(before.playing) && before.playing[i] != after.playing[i] {
			acts = append(acts, fmt.Sprintf("InputPlaying %d %s", i+1, bit(after.playing[i])))
		}
		if i < len(before.muted) && before.muted[i] != after.muted[i] {
			acts = append(acts, fmt.Sprintf("InputAudio %d %s", i+1, bit(!after.muted[i])))
		}
	}
	tallyChanged := before.active != after.active || before.preview != after.preview
	tally := s.tallyLocked()
	for c, sub := range s.subs {
		var lines []string
		if sub.tally && tallyChanged {
			lines = append(lines, "TALLY OK "+tally)
		}
		if sub.acts {
			for _, a := range acts {
				lines = append(lines, "ACTS OK "+a)
			}
		}
		if len(lines) > 0 {
			go func(c net.Conn, sub *vsub, lines []string) {
				sub.wmu.Lock()
				defer sub.wmu.Unlock()
				c.SetWriteDeadline(time.Now().Add(2 * time.Second))
				for _, l := range lines {
					c.Write([]byte(l + "\r\n"))
				}
			}(c, sub, lines)
		}
	}
}

func (s *Switcher) tallyLocked() string {
	var b strings.Builder
	for i := range s.inputs {
		switch i + 1 {
		case s.active:
			b.WriteByte('1')
		case s.preview:
			b.WriteByte('2')
		default:
			b.WriteByte('0')
		}
	}
	return b.String()
}

// --------------------------------------------------------------- XML

func tf(b bool) string {
	if b {
		return "True"
	}
	return "False"
}

// meter is a moving amplitude (0..1) for a source of loudness level.
func (s *Switcher) meter(level float64, seed int) (float64, float64) {
	if level <= 0 {
		return 0, 0
	}
	t := time.Since(s.start).Seconds()
	ph := float64(seed) * 1.7
	l := level * (0.65 + 0.25*math.Sin(t*2.3+ph) + 0.1*rand.Float64())
	r := level * (0.65 + 0.25*math.Sin(t*2.1+ph+0.6) + 0.1*rand.Float64())
	return clamp(l, 0, 1), clamp(r, 0, 1)
}

func f3(f float64) string { return strconv.FormatFloat(f, 'f', 3, 64) }

func (s *Switcher) stateXML() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return []byte(s.xmlLocked())
}

func (s *Switcher) xmlLocked() string {
	now := time.Now()
	s.advance(now)
	var b strings.Builder
	b.WriteString("<vmix>\r\n<version>" + vmixVersion + "</version>\r\n<edition>4K</edition>\r\n<preset>C:\\Users\\Booth\\Documents\\vMixStorage\\Sunday.vmix</preset>\r\n<inputs>\r\n")
	busSum := map[string][2]float64{}
	for i, in := range s.inputs {
		fmt.Fprintf(&b, `<input key="%s" number="%d" type="%s" title="%s" shortTitle="%s" state="%s" position="%d" duration="%d" loop="%s"`,
			in.key, i+1, in.Type, html.EscapeString(in.Title), html.EscapeString(in.Title), in.state, int(in.position), int(in.duration), tf(in.loop))
		if in.audio {
			lvl := in.level
			if in.duration > 0 && in.state != "Running" {
				lvl = 0 // a stopped clip is silent
			}
			l, r := s.meter(lvl*in.volume/100, i)
			if in.muted {
				l, r = 0, 0
			}
			for _, bus := range strings.Split(in.buses, ",") {
				x := busSum[bus]
				busSum[bus] = [2]float64{x[0] + l*l, x[1] + r*r}
			}
			fmt.Fprintf(&b, ` muted="%s" volume="%s" balance="%s" solo="%s" soloPFL="False" audiobusses="%s" meterF1="%s" meterF2="%s" gainDb="0"`,
				tf(in.muted), f3(in.volume), f3(in.balance), tf(in.solo), in.buses, f3(l), f3(r))
		}
		if len(in.list) > 0 {
			fmt.Fprintf(&b, ` selectedIndex="%d"`, in.selected)
		}
		b.WriteString(">" + html.EscapeString(in.Title))
		for j, fl := range in.fields {
			fmt.Fprintf(&b, `<text index="%d" name="%s">%s</text>`, j, fl[0], html.EscapeString(fl[1]))
		}
		if len(in.list) > 0 {
			b.WriteString("<list>")
			for j, it := range in.list {
				sel := ""
				if j+1 == in.selected {
					sel = ` selected="true"`
				}
				fmt.Fprintf(&b, "<item%s>%s</item>", sel, html.EscapeString(it))
			}
			b.WriteString("</list>")
		}
		b.WriteString("</input>\r\n")
	}
	b.WriteString("</inputs>\r\n<overlays>")
	for n := 1; n <= 8; n++ {
		if s.overlays[n] != 0 {
			fmt.Fprintf(&b, `<overlay number="%d">%d</overlay>`, n, s.overlays[n])
		} else {
			fmt.Fprintf(&b, `<overlay number="%d" />`, n)
		}
	}
	fmt.Fprintf(&b, "</overlays>\r\n<preview>%d</preview>\r\n<active>%d</active>\r\n<fadeToBlack>%s</fadeToBlack>\r\n", s.preview, s.active, tf(s.ftb))
	b.WriteString("<transitions>")
	for i, t := range s.transitions {
		fmt.Fprintf(&b, `<transition number="%d" effect="%s" duration="%d" />`, i+1, t.effect, t.ms)
	}
	b.WriteString("</transitions>\r\n")
	if s.recording {
		fmt.Fprintf(&b, `<recording duration="%d" filename1="D:\Recordings\Sunday %s.mp4">True</recording>`, int(now.Sub(s.recAt).Seconds()), s.recAt.Format("2006-01-02 1504"))
	} else {
		b.WriteString("<recording>False</recording>")
	}
	fmt.Fprintf(&b, "\r\n<external>%s</external>\r\n", tf(s.external))
	if s.streaming {
		b.WriteString(`<streaming channel1="True" channel2="False" channel3="False">True</streaming>`)
	} else {
		b.WriteString("<streaming>False</streaming>")
	}
	fmt.Fprintf(&b, "\r\n<playList>%s</playList>\r\n<multiCorder>%s</multiCorder>\r\n<fullscreen>%s</fullscreen>\r\n", tf(s.playList), tf(s.multi), tf(s.fullscreen))
	rms := func(x [2]float64) (float64, float64) {
		return clamp(math.Sqrt(x[0]), 0, 1), clamp(math.Sqrt(x[1]), 0, 1)
	}
	ml, mr := rms(busSum["M"])
	if s.masterMuted {
		ml, mr = 0, 0
	}
	ml, mr = ml*s.masterVol/100, mr*s.masterVol/100
	fmt.Fprintf(&b, "<audio>\r\n<master volume=\"%s\" muted=\"%s\" meterF1=\"%s\" meterF2=\"%s\" headphonesVolume=\"%s\" />\r\n",
		f3(s.masterVol), tf(s.masterMuted), f3(ml), f3(mr), f3(s.headphones))
	for _, id := range []string{"A", "B", "C", "D", "E", "F", "G"} {
		bs := s.buses[id]
		l, r := rms(busSum[id])
		if bs.muted {
			l, r = 0, 0
		}
		l, r = l*bs.volume/100, r*bs.volume/100
		fmt.Fprintf(&b, "<bus%s name=\"%s\" volume=\"%s\" muted=\"%s\" meterF1=\"%s\" meterF2=\"%s\" solo=\"%s\" sendToMaster=\"%s\" />\r\n",
			id, html.EscapeString(bs.name), f3(bs.volume), tf(bs.muted), f3(l), f3(r), tf(bs.solo), tf(bs.sendToMaster))
	}
	b.WriteString("</audio>\r\n<dynamic><input1></input1><input2></input2><input3></input3><input4></input4><value1></value1><value2></value2><value3></value3><value4></value4></dynamic>\r\n</vmix>")
	return b.String()
}

// ------------------------------------------------------------ serving

// Serve listens for the Web Controller API on addr (vMix uses 8088).
func (s *Switcher) Serve(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("switcher (vMix) on %s: %w", addr, err)
	}
	go http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if strings.TrimRight(strings.ToLower(r.URL.Path), "/") != "/api" {
			http.NotFound(w, r)
			return
		}
		r.ParseForm()
		if r.Form.Get("Function") != "" {
			if err := s.function(r.Form); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("Function completed successfully."))
			return
		}
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		w.Write(s.stateXML())
	}))
	return nil
}

// ServeTCP listens for the TCP API on addr (vMix uses 8099).
func (s *Switcher) ServeTCP(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("switcher (vMix TCP API) on %s: %w", addr, err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serveTCP(c)
		}
	}()
	return nil
}

func (s *Switcher) serveTCP(c net.Conn) {
	sub := &vsub{}
	defer func() {
		s.mu.Lock()
		delete(s.subs, c)
		s.mu.Unlock()
		c.Close()
	}()
	write := func(str string) {
		sub.wmu.Lock()
		c.SetWriteDeadline(time.Now().Add(5 * time.Second))
		c.Write([]byte(str))
		sub.wmu.Unlock()
	}
	write("VERSION OK " + vmixVersion + "\r\n")
	br := bufio.NewReader(c)
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		cmd, arg, _ := strings.Cut(line, " ")
		switch strings.ToUpper(cmd) {
		case "VERSION":
			write("VERSION OK " + vmixVersion + "\r\n")
		case "PING":
			write("PING OK PONG\r\n")
		case "XML":
			x := string(s.stateXML()) + "\r\n"
			write(fmt.Sprintf("XML %d\r\n%s", len(x), x))
		case "XMLTEXT":
			v, ok := xmlText(string(s.stateXML()), arg)
			if !ok {
				write("XMLTEXT ER Not Found\r\n")
			} else {
				write("XMLTEXT OK " + v + "\r\n")
			}
		case "TALLY":
			s.mu.Lock()
			t := s.tallyLocked()
			s.mu.Unlock()
			write("TALLY OK " + t + "\r\n")
		case "SUBSCRIBE", "UNSUBSCRIBE":
			on := strings.ToUpper(cmd) == "SUBSCRIBE"
			switch strings.ToUpper(arg) {
			case "TALLY":
				s.mu.Lock()
				sub.tally = on
				s.subs[c] = sub
				t := s.tallyLocked()
				s.mu.Unlock()
				write(cmd + " OK TALLY Subscribed\r\n")
				if on {
					write("TALLY OK " + t + "\r\n")
				}
			case "ACTS":
				s.mu.Lock()
				sub.acts = on
				s.subs[c] = sub
				s.mu.Unlock()
				write(cmd + " OK ACTS Subscribed\r\n")
			default:
				write(cmd + " ER Not Supported\r\n")
			}
		case "FUNCTION":
			name, qs, _ := strings.Cut(arg, " ")
			q, _ := url.ParseQuery(qs)
			q.Set("Function", name)
			if strings.EqualFold(name, "ScriptStartDynamic") {
				write("FUNCTION ER Function not supported over API\r\n")
				continue
			}
			if err := s.function(q); err != nil && err != errNoFunction {
				write("FUNCTION ER " + err.Error() + "\r\n")
				continue
			}
			write("FUNCTION OK Completed\r\n") // vMix says OK even for unknown names
		case "QUIT":
			write("QUIT OK Bye\r\n")
			return
		default:
			write(cmd + " ER Unknown Command\r\n")
		}
	}
}

// ------------------------------------------------------------ picture

// inputFrame draws an input that isn't a camera or the slides.
func (s *Switcher) inputFrame(in *vinput) *image.RGBA {
	s.mu.Lock()
	s.advance(time.Now())
	title, typ := in.Title, in.Type
	sub := ""
	switch {
	case typ == "Video" && strings.Contains(strings.ToLower(title), "countdown"):
		left := (in.duration - in.position) / 1000
		sub = fmt.Sprintf("%d:%02d", int(left)/60, int(left)%60)
	case in.duration > 0:
		sub = fmt.Sprintf("%s  %d:%02d / %d:%02d", in.state, int(in.position/1000)/60, int(in.position/1000)%60,
			int(in.duration/1000)/60, int(in.duration/1000)%60)
		if len(in.list) > 0 && in.selected >= 1 {
			p := in.list[in.selected-1]
			sub = p[strings.LastIndexAny(p, `\/`)+1:] + "   " + sub
		}
	case len(in.fields) > 0:
		sub = in.fields[0][1]
	}
	s.mu.Unlock()
	key := "in:" + title + "|" + sub
	s.textMu.Lock()
	defer s.textMu.Unlock()
	if img, ok := s.frames[key]; ok {
		return img
	}
	if len(s.frames) > 200 {
		s.frames = map[string]*image.RGBA{}
	}
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	bg := map[string]color.RGBA{"Video": {28, 36, 64, 255}, "GT": {20, 60, 40, 255}, "VideoList": {50, 30, 64, 255},
		"Image": {64, 44, 20, 255}, "Audio": {24, 24, 28, 255}, "Colour": {0, 0, 0, 255}}[typ]
	draw.Draw(img, img.Bounds(), image.NewUniform(bg), image.Point{}, draw.Src)
	if typ != "Colour" && s.fonts.big != nil {
		if typ == "Video" && strings.Contains(strings.ToLower(title), "countdown") {
			centered(img, s.fonts.title, 250, "Service starts in", color.RGBA{200, 210, 230, 255})
			centered(img, s.fonts.big, 380, sub, color.White)
		} else {
			centered(img, s.fonts.big, 330, title, color.White)
			centered(img, s.fonts.small, 400, sub, color.RGBA{200, 210, 230, 255})
		}
	}
	s.frames[key] = img
	return img
}

// overlay draws what an overlay input puts over the program: a lower
// third for a title, a corner bug for an image (nil: nothing).
func (s *Switcher) overlay(in *vinput) *image.RGBA {
	if s.fonts.big == nil || (in.Type != "GT" && in.Type != "Image") {
		return nil
	}
	parts := []string{in.Type, in.Title}
	for _, f := range in.fields {
		parts = append(parts, f[1])
	}
	key := "ov:" + strings.Join(parts, "|")
	s.textMu.Lock()
	defer s.textMu.Unlock()
	if img, ok := s.frames[key]; ok {
		return img
	}
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	if in.Type == "Image" {
		r := image.Rect(W-230, 30, W-40, 90)
		draw.Draw(img, r, image.NewUniform(color.RGBA{0, 0, 0, 150}), image.Point{}, draw.Src)
		d := &font.Drawer{Face: s.fonts.label}
		w := d.MeasureString("LIVE STREAM").Ceil()
		left(img, s.fonts.label, r.Min.X+(r.Dx()-w)/2, 68, "LIVE STREAM", color.White)
	} else {
		r := image.Rect(80, H-190, 860, H-70)
		draw.Draw(img, r, image.NewUniform(color.RGBA{10, 20, 40, 200}), image.Point{}, draw.Src)
		draw.Draw(img, image.Rect(80, H-190, 92, H-70), image.NewUniform(color.RGBA{230, 160, 40, 255}), image.Point{}, draw.Src)
		if len(in.fields) > 0 {
			left(img, s.fonts.title, 116, H-136, in.fields[0][1], color.White)
		}
		if len(in.fields) > 1 {
			left(img, s.fonts.small, 118, H-96, in.fields[1][1], color.RGBA{210, 220, 235, 255})
		}
	}
	s.frames[key] = img
	return img
}

// Program draws the program picture into dst: crossfades during a
// transition, the T-bar's blend, overlays, fade to black.
func (s *Switcher) Program(dst *image.RGBA) {
	s.mu.Lock()
	active, from, k := s.active, s.fadeFrom, 1.0
	if s.fadeDur > 0 {
		k = float64(time.Since(s.fadeStart)) / float64(s.fadeDur)
		if k >= 1 {
			s.fadeDur = 0
			k = 1
		}
	}
	if s.fadeDur == 0 && s.fader != s.tbarFrom { // the T-bar is part-way
		from, active, k = s.active, s.preview, float64(abs(s.fader-s.tbarFrom))/255
	}
	ftb := 0.0
	if s.ftb {
		ftb = math.Min(1, time.Since(s.ftbAt).Seconds())
	} else if !s.ftbAt.IsZero() {
		ftb = math.Max(0, 1-time.Since(s.ftbAt).Seconds())
	}
	var ovs []*vinput
	for ch := 1; ch <= 4; ch++ {
		if n := s.overlays[ch]; n >= 1 && n <= len(s.inputs) {
			ovs = append(ovs, s.inputs[n-1])
		}
	}
	in := s.inputs
	s.mu.Unlock()
	a := in[active-1].Frame()
	if k >= 1 || from < 1 {
		copy(dst.Pix, a.Pix)
	} else {
		b := in[from-1].Frame()
		for i := range dst.Pix {
			dst.Pix[i] = uint8(float64(b.Pix[i])*(1-k) + float64(a.Pix[i])*k)
		}
	}
	for _, o := range ovs {
		s.mu.Lock()
		img := s.overlay(o)
		s.mu.Unlock()
		if img != nil {
			draw.Draw(dst, dst.Bounds(), img, image.Point{}, draw.Over)
		}
	}
	if ftb > 0 {
		f := 1 - ftb
		for i := range dst.Pix {
			if i%4 != 3 {
				dst.Pix[i] = uint8(float64(dst.Pix[i]) * f)
			}
		}
	}
}

var (
	tallyRed   = color.RGBA{230, 30, 30, 255}
	tallyGreen = color.RGBA{30, 200, 60, 255}
)

// vmixEffects are transition effects, callable as functions.
var vmixEffects = map[string]bool{}

func init() {
	for _, e := range []string{"Fade", "Zoom", "Wipe", "Slide", "Fly", "CrossZoom", "FlyRotate", "Cube", "CubeZoom",
		"VerticalWipe", "VerticalSlide", "Merge", "WipeReverse", "SlideReverse", "VerticalWipeReverse", "VerticalSlideReverse",
		"BarnDoor", "RollerDoor", "AlphaFade", "Cut"} {
		vmixEffects[strings.ToLower(e)] = true
	}
}
