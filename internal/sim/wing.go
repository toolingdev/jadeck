package sim

// Wing is a simulated Behringer WING set up like a small venue's
// console, close enough to the real protocol (WING Remote Protocols 3.1)
// that the behringer-wing add-on runs its real code paths against it.
// Ported from that add-on's test console (fakewing_test.go):
//
//   - UDP discovery on 2222: "WING?" → "WING,<ip>,<name>,<model>,<serial>,<fw>"
//   - OSC on UDP 2223: GET replies carry three values (text, normalized,
//     raw), errors come back on "/*", sets are silent; "<node> ,b dd"
//     answers node definitions (how the add-on learns parameter hashes);
//     the OSC lock and read-only parameters drop sets without a word
//   - native protocol on TCP 2222: every change is pushed to every client
//     as D7 <hash> <value> on channel 1; DA DD (root definition) is the
//     keep-alive; idle clients are dropped after 10 s; at most 24 clients
//   - meters: a channel-3 request (D3 port, D4 id, DC families… DE) makes
//     the console send UDP datagrams every 50 ms until the request is not
//     renewed for 5 s. Levels move like a band, a choir and a speaker.
//
// Hashes are made up (crc32 of the path), so a client has to learn them.

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"log/slog"
	"math"
	"math/rand"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type wparam struct {
	typ   byte // 'i' int, 'f' float, 'l' log float, 's' string, 'e' enum
	i     int
	f     float64
	s     string
	min   float64
	max   float64
	items []string
	ro    bool
	hash  uint32
	fader bool
}

type wclient struct {
	conn net.Conn
	out  chan wframe
	// meter subscription (Wing.mu)
	port    int
	id      uint32
	strips  []wStrip
	renewed time.Time
}

type wframe struct {
	ch int
	b  []byte
}

// Wing is the simulated console.
type Wing struct {
	mu     sync.Mutex
	params map[string]*wparam
	byHash map[uint32]string
	nodes  map[string][]string
	sig    map[wStrip]wsource

	osc, disc, mpc net.PacketConn
	tcp            net.Listener
	clients        []*wclient
	host           string
	start          time.Time
	done           chan struct{}
	sets           int

	Name string
	Log  *slog.Logger
}

// What feeds a strip, for the meters.
type wsource struct {
	kind  string // speech, sing, keys, guitar, bass, kick, snare, cymbals, choir, video, room, tracks
	level float64
	phase float64
}

// The venue setup: channel n is wChannels[n-1].
var wChannels = []struct {
	name   string
	col    int
	stereo bool
	src    string
	level  float64 // source level, dBFS
	gain   float64 // preamp
	fdr    float64
	mute   bool
	tags   string
}{
	{"Speaker", 13, false, "speech", -18, 34, 0, false, "#D3,#M4"},
	{"Lectern", 13, false, "speech", -21, 38, -2, false, "#D3,#M4"},
	{"Lead vox", 9, false, "sing", -14, 32, 1.5, false, "#D1,#M2"},
	{"BGV 1", 10, false, "sing", -17, 30, -4, false, "#D1,#M2"},
	{"BGV 2", 10, false, "sing", -17, 30, -5, true, "#D1,#M2"},
	{"Keys", 5, true, "keys", -16, 18, -3, false, "#D2,#M1"},
	{"Guitar", 15, false, "guitar", -17, 22, -6, false, "#D2,#M1"},
	{"Bass", 8, false, "bass", -15, 16, -4, false, "#D2,#M1"},
	{"Kick", 2, false, "kick", -12, 28, -5, false, "#D2,#M1"},
	{"Snare", 2, false, "snare", -14, 26, -6, false, "#D2,#M1"},
	{"OH", 2, true, "cymbals", -22, 24, -8, false, "#D2,#M1"},
	{"Choir", 12, true, "choir", -20, 36, -6, true, "#D4"},
	{"Video", 14, true, "video", -18, 0, -10, false, "#D5,#M3"},
	{"Handheld", 13, false, "speech", -20, 32, -144, true, "#D3,#M4"},
	{"Tracks", 4, true, "tracks", -16, 0, -10, true, "#D5,#M3"},
	{"Room", 17, true, "room", -38, 40, -15, false, ""},
}

var wNames = map[wStrip]struct {
	name   string
	col    int
	stereo bool
	fdr    float64
}{
	{"aux", 1}: {"Walk-in PC", 4, true, -8},
	{"aux", 2}: {"Stream return", 14, true, -144},
	{"bus", 1}: {"Wedge 1", 7, false, -4}, {"bus", 2}: {"Wedge 2", 7, false, -4},
	{"bus", 3}: {"IEM Lead", 11, true, -3}, {"bus", 4}: {"IEM Keys", 11, true, -3},
	{"bus", 5}: {"IEM Band", 12, true, -2}, {"bus", 6}: {"Stream", 3, true, 0},
	{"bus", 7}: {"Hall FX", 16, true, -6}, {"bus", 8}: {"Delay FX", 16, true, -10},
	{"main", 1}: {"Main", 18, true, 0}, {"main", 2}: {"Sub", 18, false, -4}, {"main", 3}: {"Fill", 18, false, -8},
	{"mtx", 1}: {"Lobby", 17, false, -12}, {"mtx", 2}: {"Nursery", 17, false, -15}, {"mtx", 3}: {"Record", 3, true, -2},
	{"dca", 1}: {"Vocals", 9, false, 0}, {"dca", 2}: {"Band", 5, false, -2}, {"dca", 3}: {"Speech", 13, false, 0},
	{"dca", 4}: {"Choir", 12, false, -3}, {"dca", 5}: {"Playback", 14, false, 0},
	{"mgrp", 1}: {"Band", 0, false, 0}, {"mgrp", 2}: {"Vocals", 0, false, 0}, {"mgrp", 3}: {"Playback", 0, false, 0},
	{"mgrp", 4}: {"Speech", 0, false, 0},
}

// Scenes, and which mute groups (1 Band, 2 Vocals, 3 Playback, 4 Speech)
// each one mutes.
var wScenes = []string{"Walk-in~Music from the PC", "Worship~Band + vocals", "Announcements~Speech only",
	"Sermon~Speaker only", "Response~Keys + vocals", "Walk-out"}
var wSceneMutes = [][4]bool{
	{true, true, false, false}, {false, false, true, false}, {true, true, true, false},
	{true, true, true, false}, {false, false, true, false}, {true, true, false, false},
}

var wSongs = []struct {
	title, artist string
	secs          float64
}{{"Prelude", "Piano", 214}, {"Walk-in mix", "Worship team", 1260}, {"Offering", "Strings", 187}}

// NewWing builds the console's parameter tree.
func NewWing() *Wing {
	w := &Wing{params: map[string]*wparam{}, byHash: map[uint32]string{}, nodes: map[string][]string{},
		sig: map[wStrip]wsource{}, Name: "Sim WING", Log: slog.Default(), start: time.Now(), host: "127.0.0.1", done: make(chan struct{})}
	w.build()
	return w
}

func (w *Wing) add(path string, p wparam) {
	p.hash = crc32.ChecksumIEEE([]byte(path))
	w.params[path] = &p
	w.byHash[p.hash] = path
	i := strings.LastIndex(path, "/")
	node, name := path[:i], path[i+1:]
	if node == "" {
		node = "/"
	}
	w.nodes[node] = append(w.nodes[node], name)
}

func (w *Wing) build() {
	fdr := wparam{typ: 'f', f: -144, min: -144, max: 10, fader: true}
	for _, s := range wAllStrips() {
		p := s.Path()
		name, col, stereo, level := "", 17, false, -144.0
		mute := false
		tags := ""
		if s.Kind == "ch" && s.N <= len(wChannels) {
			c := wChannels[s.N-1]
			name, col, stereo, level, mute, tags = c.name, c.col, c.stereo, c.fdr, c.mute, c.tags
			w.sig[s] = wsource{kind: c.src, level: c.level, phase: rand.Float64() * 10}
		} else if n, ok := wNames[s]; ok {
			name, col, stereo, level = n.name, n.col, n.stereo, n.fdr
			if s == (wStrip{"aux", 1}) {
				w.sig[s] = wsource{kind: "tracks", level: -15, phase: 3}
				tags = "#M3"
			}
		} else if s.Kind != "ch" {
			col = 1 + s.N%18
		}
		if col == 0 {
			col = 1
		}
		w.add(p+"/name", wparam{typ: 's', s: name})
		w.add(p+"/mute", wparam{typ: 'i', min: 0, max: 1, i: b2i(mute)})
		if s.Kind == "mgrp" {
			continue
		}
		f := fdr
		f.f = level
		w.add(p+"/col", wparam{typ: 'i', i: col, min: 1, max: 18})
		w.add(p+"/fdr", f)
		w.add(p+"/$solo", wparam{typ: 'i', min: 0, max: 1})
		if s.Kind == "dca" {
			continue
		}
		w.add(p+"/$name", wparam{typ: 's', s: name, ro: true})
		w.add(p+"/$col", wparam{typ: 'i', i: col, min: 1, max: 18, ro: true})
		w.add(p+"/$mute", wparam{typ: 'i', min: 0, max: 2, ro: true})
		f.ro = true
		w.add(p+"/$fdr", f)
		w.add(p+"/tags", wparam{typ: 's', s: tags})
		w.addProcessing(s, stereo)
	}
	fxModels := []string{"NONE", "EXT", "HALL", "PLATE", "ST-DL", "CHORUS"}
	for n := 1; n <= 16; n++ {
		node := fmt.Sprintf("/fx/%d", n)
		w.add(node+"/mdl", wparam{typ: 'e', items: fxModels})
		w.add(node+"/fxmix", wparam{typ: 'f', f: 100, min: 0, max: 100})
		w.add(node+"/$esrc", wparam{typ: 'i', min: 0, max: 400, ro: true})
	}
	w.setModel(1, "HALL")
	w.setModel(2, "PLATE")
	w.setModel(3, "ST-DL")
	w.add("/$ctl/osc/ronly", wparam{typ: 'i', min: 0, max: 1})
	w.add("/$syscfg/tcplock", wparam{typ: 'i', min: 0, max: 1})
	w.add("/$syscfg/$firmware", wparam{typ: 's', s: "3.1.2", ro: true})
	w.add("/$ctl/$stat/cnslock", wparam{typ: 's', s: "", ro: true})
	w.add("/$stat/solo", wparam{typ: 'i', min: 0, max: 1, ro: true})
	w.add("/$stat/usbstate", wparam{typ: 'e', items: []string{"-", "ERR", "IDLE", "BUSY"}, i: 2, ro: true})
	w.add("/$stat/usbvolname", wparam{typ: 's', s: "SERVICES", ro: true})
	w.add("/$ctl/lib/$scenes", wparam{typ: 'e', items: wScenes, ro: true})
	w.add("/$ctl/lib/$actidx", wparam{typ: 'i', i: 2, min: 0, max: 16384, ro: true})
	w.add("/$ctl/lib/$active", wparam{typ: 's', s: "I:SHOWS/SUNDAY/scene_2.snap", ro: true})
	w.add("/$ctl/lib/$actshow", wparam{typ: 's', s: "I:SHOWS/SUNDAY.show", ro: true})
	w.add("/$ctl/lib/$activeid", wparam{typ: 'i', i: 2, min: 0, max: 128, ro: true})
	w.add("/$ctl/lib/$actionidx", wparam{typ: 'i', min: 0, max: 16384})
	w.add("/$ctl/lib/$action", wparam{typ: 'e', items: []string{"IDLE", "GO", "PREV", "NEXT", "GOPREV", "GONEXT", "GOTAG"}})
	w.add("/rec/$action", wparam{typ: 'e', items: []string{"STOP", "REC", "PAUSE", "NEWFILE"}})
	w.add("/rec/$actstate", wparam{typ: 'e', items: []string{"STOP", "REC", "PAUSE", "ERROR"}, ro: true})
	w.add("/rec/$time", wparam{typ: 'f', min: 0, max: 35999, ro: true})
	w.add("/rec/$actfile", wparam{typ: 's', ro: true})
	w.add("/play/$action", wparam{typ: 'e', items: []string{"IDLE", "STOP", "PLAY", "PAUSE", "NEXT", "PREV", "PLAYFILE"}})
	w.add("/play/$actstate", wparam{typ: 'e', items: []string{"STOP", "PLAY", "PAUSE", "ERROR"}, ro: true})
	w.add("/play/$song", wparam{typ: 's', s: wSongs[0].title, ro: true})
	w.add("/play/$artist", wparam{typ: 's', s: wSongs[0].artist, ro: true})
	w.add("/play/$actfile", wparam{typ: 's', s: "U:MUSIC/01 Prelude.wav", ro: true})
	w.add("/play/$pos", wparam{typ: 'f', min: 0, max: 35999})
	w.add("/play/$total", wparam{typ: 'f', f: wSongs[0].secs, min: 0, max: 35999, ro: true})
	for _, b := range []string{"A", "B"} {
		w.add("/cfg/talk/"+b+"/$on", wparam{typ: 'i', min: 0, max: 1})
		w.add("/cfg/talk/"+b+"/mode", wparam{typ: 'e', items: []string{"AUTO", "PUSH", "LATCH"}, i: 2})
	}
	// nodes as children of their parents (so dd on a parent lists them)
	keys := make([]string, 0, len(w.nodes))
	for k := range w.nodes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, n := range keys {
		for p := n; p != "/" && p != ""; {
			i := strings.LastIndex(p, "/")
			parent, name := p[:i], p[i+1:]
			if parent == "" {
				parent = "/"
			}
			if !wContains(w.nodes[parent], name) {
				w.nodes[parent] = append(w.nodes[parent], name)
			}
			p = parent
		}
	}
	w.applyScene(2, false)
	w.recomputeEffective()
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func wContains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// addProcessing adds the channel-strip parameters of the default models
// (p.47-65): input, filters, gate, EQ, compressor, sends, mains, pan,
// inserts, mono/stereo.
func (w *Wing) addProcessing(s wStrip, stereo bool) {
	p := s.Path()
	lin := func(min, max, v float64) wparam { return wparam{typ: 'f', f: v, min: min, max: max} }
	logf := func(min, max, v float64) wparam { return wparam{typ: 'l', f: v, min: min, max: max} }
	sw := func(on bool) wparam { return wparam{typ: 'i', min: 0, max: 1, i: b2i(on)} }
	named := false
	var src wsource
	if s.Kind == "ch" && s.N <= len(wChannels) {
		named, src = true, w.sig[s]
	}
	voice := named && (src.kind == "speech" || src.kind == "sing" || src.kind == "choir")
	drum := named && (src.kind == "kick" || src.kind == "snare")
	switch s.Kind {
	case "ch", "aux":
		mode := 0
		if stereo {
			mode = 1
		}
		w.add(p+"/in/set/$mode", wparam{typ: 'e', items: []string{"M", "ST", "M/S"}, i: mode, ro: true})
		w.add(p+"/in/set/inv", sw(false))
		w.add(p+"/in/set/trim", lin(-18, 18, 0))
		w.add(p+"/in/set/bal", lin(-9, 9, 0))
		if s.Kind == "ch" {
			g := 20.0
			if named {
				g = wChannels[s.N-1].gain
			}
			w.add(p+"/in/set/$g", lin(-2.5, 45, g))
		}
		w.add(p+"/in/set/$vph", sw(named && (src.kind == "choir" || src.kind == "cymbals" || src.kind == "room")))
		w.add(p+"/in/conn/grp", wparam{typ: 'e', items: []string{"OFF", "LCL", "AUX", "A", "B", "C"}, i: 3})
		w.add(p+"/in/conn/in", wparam{typ: 'i', i: s.N, min: 1, max: 64})
		lc := 100.0
		if src.kind == "bass" || src.kind == "kick" {
			lc = 40
		} else if voice {
			lc = 80
		}
		w.add(p+"/flt/lc", sw(named && src.kind != "kick" && src.kind != "bass"))
		w.add(p+"/flt/lcf", logf(20, 2000, lc))
		w.add(p+"/flt/lcs", wparam{typ: 'e', items: []string{"6", "12", "18", "24"}, i: 1})
		w.add(p+"/flt/hc", sw(false))
		w.add(p+"/flt/hcf", logf(50, 20000, 18000))
	default:
		mono := 0
		if n, ok := wNames[s]; ok && !n.stereo {
			mono = 1
		}
		w.add(p+"/busmono", wparam{typ: 'i', min: 0, max: 1, i: mono})
	}
	if s.Kind == "ch" {
		thr := -40.0
		if drum {
			thr = -32
		}
		for k, v := range map[string]wparam{"on": sw(drum), "thr": lin(-80, 0, thr), "range": lin(3, 60, 40), "att": lin(0, 120, 10),
			"hld": lin(0, 200, 10), "rel": logf(4, 4000, 199)} {
			w.add(p+"/gate/"+k, v)
		}
		w.add(p+"/gate/mdl", wparam{typ: 'e', items: []string{"GATE", "DUCK", "E88"}})
	}
	w.add(p+"/eq/on", sw(named || s.Kind == "main"))
	w.add(p+"/eq/mdl", wparam{typ: 'e', items: []string{"STD", "SOUL", "E88"}})
	eq := map[string][3]float64{"l": {0, 120, 1}, "1": {0, 350, 1.5}, "2": {0, 1200, 1.5}, "3": {0, 2800, 1.5}, "4": {0, 5000, 1.5}, "h": {0, 10000, 1}}
	if voice {
		eq["l"], eq["1"], eq["3"], eq["h"] = [3]float64{2, 120, 1}, [3]float64{-3.5, 350, 2}, [3]float64{1.5, 2800, 1.4}, [3]float64{2.5, 10000, 1}
	} else if drum {
		eq["l"], eq["1"], eq["h"] = [3]float64{3, 60, 1}, [3]float64{-6, 400, 2}, [3]float64{2, 8000, 1}
	} else if named {
		eq["1"] = [3]float64{-2, 300, 1.5}
	}
	for _, b := range []string{"l", "1", "2", "3", "4", "h"} {
		w.add(p+"/eq/"+b+"g", lin(-15, 15, eq[b][0]))
		w.add(p+"/eq/"+b+"f", logf(20, 20000, eq[b][1]))
		w.add(p+"/eq/"+b+"q", logf(0.44, 10, eq[b][2]))
	}
	w.add(p+"/eq/leq", wparam{typ: 'e', items: []string{"PEQ", "SHV"}, i: 1})
	w.add(p+"/eq/heq", wparam{typ: 'e', items: []string{"PEQ", "SHV"}, i: 1})
	w.add(p+"/dyn/on", sw(voice || src.kind == "bass" || s.Kind == "main"))
	w.add(p+"/dyn/mdl", wparam{typ: 'e', items: []string{"COMP", "EXP", "LA"}})
	w.add(p+"/dyn/thr", lin(-60, 0, map[bool]float64{true: -22, false: -10}[voice]))
	w.add(p+"/dyn/ratio", wparam{typ: 'e', items: []string{"1.1", "1.5", "2.0", "3.0", "4.0", "10", "100"}, i: 3})
	w.add(p+"/dyn/att", lin(0, 120, 10))
	w.add(p+"/dyn/rel", logf(4, 4000, 100))
	w.add(p+"/dyn/gain", lin(-6, 12, map[bool]float64{true: 3, false: 0}[voice]))
	if s.Kind == "ch" || s.Kind == "aux" || s.Kind == "bus" {
		for m := 1; m <= 4; m++ {
			n := fmt.Sprintf("%s/main/%d/", p, m)
			w.add(n+"on", wparam{typ: 'i', min: 0, max: 1, i: b2i(m == 1)})
			w.add(n+"lvl", wparam{typ: 'f', f: 0, min: -144, max: 10, fader: true})
			w.add(n+"pre", sw(false))
		}
		for b := 1; b <= 16; b++ {
			if s.Kind == "bus" && b == s.N {
				continue
			}
			n := fmt.Sprintf("%s/send/%d/", p, b)
			on, lvl := false, -144.0
			if named && b <= 6 {
				on, lvl = true, -10+float64((s.N*7+b*3)%9)
			}
			if named && voice && (b == 7 || b == 8) {
				on, lvl = true, -12
			}
			w.add(n+"on", sw(on))
			w.add(n+"lvl", wparam{typ: 'f', f: lvl, min: -144, max: 10, fader: true})
			w.add(n+"mode", wparam{typ: 'e', items: []string{"PRE", "POST", "GRP"}, i: map[bool]int{true: 0, false: 1}[b <= 5]})
		}
	}
	if s.Kind != "mtx" {
		for x := 1; x <= 8; x++ {
			n := fmt.Sprintf("%s/send/MX%d/", p, x)
			w.add(n+"on", sw(false))
			w.add(n+"lvl", wparam{typ: 'f', f: -144, min: -144, max: 10, fader: true})
			w.add(n+"mode", wparam{typ: 'e', items: []string{"PRE", "POST", "GRP"}, i: 1})
		}
	}
	pan := 0.0
	if named {
		pan = []float64{0, 0, 0, -20, 20, 0, -30, 0, 0, 0, 0, 0, 0, 0, 0, 0}[s.N-1]
	}
	w.add(p+"/pan", lin(-100, 100, pan))
	w.add(p+"/wid", lin(-150, 150, 100))
	for _, ins := range []string{"preins", "postins"} {
		w.add(p+"/"+ins+"/on", sw(false))
		w.add(p+"/"+ins+"/ins", wparam{typ: 'e', items: []string{"NONE", "FX1", "FX2", "FX3", "FX4"}})
	}
}

// setModel loads an FX model: its parameters replace the old ones (the
// console's tree changes shape, p.26).
func (w *Wing) setModel(slot int, mdl string) {
	node := fmt.Sprintf("/fx/%d", slot)
	keep := map[string]bool{"mdl": true, "fxmix": true, "$esrc": true}
	var kids []string
	for _, k := range w.nodes[node] {
		if keep[k] {
			kids = append(kids, k)
		} else if p := w.params[node+"/"+k]; p != nil {
			delete(w.byHash, p.hash)
			delete(w.params, node+"/"+k)
		}
	}
	w.nodes[node] = kids
	p := w.params[node+"/mdl"]
	for i, it := range p.items {
		if it == mdl {
			p.i = i
		}
	}
	switch mdl {
	case "HALL", "PLATE":
		w.add(node+"/pdel", wparam{typ: 'i', i: 20, min: 0, max: 200})
		w.add(node+"/size", wparam{typ: 'i', i: 60, min: 0, max: 100})
		w.add(node+"/dcy", wparam{typ: 'l', f: map[bool]float64{true: 2.2, false: 1.4}[mdl == "HALL"], min: 0.2, max: 5})
		w.add(node+"/lc", wparam{typ: 'l', f: 120, min: 20, max: 400})
	case "ST-DL":
		w.add(node+"/time", wparam{typ: 'f', f: 380, min: 1, max: 3000})
		w.add(node+"/feed", wparam{typ: 'f', f: 25, min: 0, max: 100})
	case "CHORUS":
		w.add(node+"/speed", wparam{typ: 'l', f: 0.8, min: 0.05, max: 5})
		w.add(node+"/depth", wparam{typ: 'f', f: 40, min: 0, max: 100})
	case "EXT":
		w.add(node+"/egrp", wparam{typ: 'e', items: []string{"OFF", "LCL"}})
		w.add(node+"/ein", wparam{typ: 'i', i: 1, min: 1, max: 64})
	}
}

// ----------------------------------------------------------- serving

// Serve listens for OSC on oscPort (UDP), the native protocol on
// nativePort (TCP) and discovery on nativePort (UDP), all on host. A port
// that is taken falls back to a free one (see Ports).
func (w *Wing) Serve(host string, oscPort, nativePort int) error {
	w.host = host
	listenUDP := func(port int) (net.PacketConn, error) {
		pc, err := net.ListenPacket("udp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err != nil && port != 0 {
			w.Log.Warn("WING: port taken, using a free one", "udp", port, "err", err)
			pc, err = net.ListenPacket("udp", net.JoinHostPort(host, "0"))
		}
		return pc, err
	}
	var err error
	if w.osc, err = listenUDP(oscPort); err != nil {
		return fmt.Errorf("WING OSC: %w", err)
	}
	if w.tcp, err = net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(nativePort))); err != nil && nativePort != 0 {
		w.Log.Warn("WING: port taken, using a free one", "tcp", nativePort, "err", err)
		w.tcp, err = net.Listen("tcp", net.JoinHostPort(host, "0"))
	}
	if err != nil {
		return fmt.Errorf("WING native: %w", err)
	}
	if w.disc, err = listenUDP(w.tcp.Addr().(*net.TCPAddr).Port); err != nil {
		return fmt.Errorf("WING discovery: %w", err)
	}
	if w.mpc, err = net.ListenPacket("udp", net.JoinHostPort(host, "0")); err != nil {
		return fmt.Errorf("WING meters: %w", err)
	}
	go w.serveOSC()
	go w.serveTCP()
	go w.serveDiscovery()
	go w.run()
	return nil
}

// Ports are the OSC (UDP) and native (TCP; discovery on UDP) ports in use.
func (w *Wing) Ports() (osc, native int) {
	return w.osc.LocalAddr().(*net.UDPAddr).Port, w.tcp.Addr().(*net.TCPAddr).Port
}

// Close stops serving.
func (w *Wing) Close() {
	select {
	case <-w.done:
	default:
		close(w.done)
	}
	for _, c := range []interface{ Close() error }{w.osc, w.disc, w.mpc, w.tcp} {
		if c != nil {
			c.Close()
		}
	}
	w.mu.Lock()
	for _, c := range w.clients {
		c.conn.Close()
	}
	w.mu.Unlock()
}

func (w *Wing) identity() string {
	return "WING," + w.host + "," + w.Name + ",ngc-full,S0000001,3.1.2"
}

func (w *Wing) serveDiscovery() {
	buf := make([]byte, 64)
	for {
		n, from, err := w.disc.ReadFrom(buf)
		if err != nil {
			return
		}
		if string(buf[:n]) == "WING?" {
			w.disc.WriteTo([]byte(w.identity()), from)
		}
	}
}

// ---------------------------------------------------------------- OSC

func (w *Wing) serveOSC() {
	buf := make([]byte, 65536)
	for {
		n, from, err := w.osc.ReadFrom(buf)
		if err != nil {
			return
		}
		addr, vals, err := wOSCDecode(buf[:n])
		if err != nil {
			continue
		}
		for _, r := range w.handleOSC(addr, vals) {
			w.osc.WriteTo(r, from)
		}
	}
}

func wOSCErr(msg string) [][]byte { return [][]byte{wOSCEncode("/*", msg)} }

func (w *Wing) handleOSC(addr string, vals []any) [][]byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	if addr == "/?" {
		return [][]byte{wOSCEncode("/?", w.identity())}
	}
	if len(vals) == 1 {
		if blob, ok := vals[0].([]byte); ok {
			if len(blob) == 1 && blob[0] == tokDefReq {
				out := w.defsOf(addr)
				if out == nil {
					return wOSCErr("NODE NOT FOUND")
				}
				return [][]byte{wOSCEncode(addr, out)}
			}
			return wOSCErr("NODE NOT FOUND")
		}
	}
	p := w.params[addr]
	if p == nil {
		return wOSCErr("NODE NOT FOUND")
	}
	if len(vals) == 0 {
		return [][]byte{wOSCEncode(addr, w.oscValue(p)...)}
	}
	w.sets++
	if w.params["/$ctl/osc/ronly"].i == 1 || p.ro {
		return nil // dropped without a word, as the console does
	}
	if err := w.assign(addr, p, vals[0]); err != "" {
		return wOSCErr(err)
	}
	w.Log.Info("WING", "set", addr, "to", w.text(p))
	return nil
}

func (w *Wing) text(p *wparam) string {
	switch p.typ {
	case 'i':
		return strconv.Itoa(p.i)
	case 'f', 'l':
		if p.fader && p.f <= -144 {
			return "-oo"
		}
		return strconv.FormatFloat(p.f, 'f', 1, 64)
	case 'e':
		if p.i >= 0 && p.i < len(p.items) {
			return p.items[p.i]
		}
		return ""
	}
	return p.s
}

func (w *Wing) oscValue(p *wparam) []any {
	switch p.typ {
	case 'i':
		norm := 0.0
		if p.max > p.min {
			norm = (float64(p.i) - p.min) / (p.max - p.min)
		}
		return []any{strconv.Itoa(p.i), float32(norm), int32(p.i)}
	case 'f', 'l':
		norm := 0.0
		if p.fader {
			norm = wDBToPos(p.f)
		} else if p.typ == 'l' && p.min > 0 && p.max > p.min {
			norm = math.Log(p.f/p.min) / math.Log(p.max/p.min)
		} else if p.max > p.min {
			norm = (p.f - p.min) / (p.max - p.min)
		}
		return []any{w.text(p), float32(norm), float32(p.f)}
	case 'e':
		return []any{w.text(p)}
	}
	return []any{p.s}
}

// assign sets a parameter the way the console converts types (p.22).
// w.mu held.
func (w *Wing) assign(path string, p *wparam, v any) string {
	num := func() (float64, bool) {
		switch x := v.(type) {
		case int32:
			return float64(x), true
		case float32:
			return float64(x), true
		case string:
			g, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
			return g, err == nil
		}
		return 0, false
	}
	switch p.typ {
	case 'i':
		n, ok := num()
		if !ok {
			return "VALUE ERROR"
		}
		if n == -1 && p.max == 1 {
			n = float64(1 - p.i) // toggle
		}
		if n < p.min || n > p.max {
			return "VALUE ERROR"
		}
		p.i = int(n)
	case 'f', 'l':
		n, ok := num()
		if !ok {
			return "VALUE ERROR"
		}
		if n < p.min-1e-6 || n > p.max+1e-6 {
			return "VALUE ERROR"
		}
		p.f = n
	case 'e':
		if s, ok := v.(string); ok {
			idx := -1
			for i, it := range p.items {
				if strings.EqualFold(it, s) {
					idx = i
				}
			}
			if idx < 0 {
				return "VALUE ERROR"
			}
			p.i = idx
		} else if n, ok := num(); ok && int(n) >= 0 && int(n) < len(p.items) {
			p.i = int(n)
		} else {
			return "VALUE ERROR"
		}
	default:
		s, _ := v.(string)
		p.s = s
	}
	w.changed(path)
	w.effects(path)
	return ""
}

// set changes a value as the console itself would (read-only ones too);
// w.mu held.
func (w *Wing) set(path string, fn func(*wparam)) {
	if x := w.params[path]; x != nil {
		fn(x)
		w.changed(path)
	}
}

// effects: what the console does after a write (effective values,
// transports, show control). w.mu held.
func (w *Wing) effects(path string) {
	switch {
	case strings.HasSuffix(path, "/mute") || strings.HasSuffix(path, "/fdr") || strings.HasSuffix(path, "/tags"):
		w.recomputeEffective()
	case strings.HasSuffix(path, "/name") || strings.HasSuffix(path, "/col"):
		i := strings.LastIndex(path, "/")
		eff := path[:i+1] + "$" + path[i+1:]
		if src, dst := w.params[path], w.params[eff]; dst != nil {
			w.set(eff, func(x *wparam) { x.s, x.i = src.s, src.i })
		}
	case strings.HasPrefix(path, "/fx/") && strings.HasSuffix(path, "/mdl"):
		var n int
		fmt.Sscanf(path, "/fx/%d/mdl", &n)
		p := w.params[path]
		w.setModel(n, p.items[p.i])
	case strings.HasSuffix(path, "/$solo"):
		any1 := 0
		for _, s := range wAllStrips() {
			if x := w.params[s.Path()+"/$solo"]; x != nil && x.i == 1 {
				any1 = 1
			}
		}
		w.set("/$stat/solo", func(p *wparam) { p.i = any1 })
	case path == "/rec/$action":
		act := w.params[path].items[w.params[path].i]
		switch act {
		case "REC":
			if w.params["/rec/$actstate"].i == 0 {
				w.set("/rec/$time", func(p *wparam) { p.f = 0 })
				w.set("/rec/$actfile", func(p *wparam) { p.s = "U:REC/" + time.Now().Format("2006-01-02_1504") + ".wav" })
			}
			w.set("/rec/$actstate", func(p *wparam) { p.i = 1 })
		case "STOP":
			w.set("/rec/$actstate", func(p *wparam) { p.i = 0 })
		case "PAUSE":
			w.set("/rec/$actstate", func(p *wparam) { p.i = 2 })
		case "NEWFILE":
			w.set("/rec/$time", func(p *wparam) { p.f = 0 })
			w.set("/rec/$actfile", func(p *wparam) { p.s = "U:REC/" + time.Now().Format("2006-01-02_150405") + ".wav" })
		}
	case path == "/play/$action":
		act := w.params[path].items[w.params[path].i]
		switch act {
		case "PLAY":
			w.set("/play/$actstate", func(p *wparam) { p.i = 1 })
		case "STOP":
			w.set("/play/$actstate", func(p *wparam) { p.i = 0 })
			w.set("/play/$pos", func(p *wparam) { p.f = 0 })
		case "PAUSE":
			w.set("/play/$actstate", func(p *wparam) { p.i = 2 })
		case "NEXT", "PREV":
			w.song(map[string]int{"NEXT": 1, "PREV": -1}[act])
		}
	case path == "/$ctl/lib/$action":
		act := w.params[path].items[w.params[path].i]
		idx := w.params["/$ctl/lib/$actionidx"].i
		cur := w.params["/$ctl/lib/$actidx"].i
		next := cur
		switch act {
		case "GO", "GOTAG":
			next = idx
		case "GONEXT", "NEXT":
			next = cur + 1
		case "GOPREV", "PREV":
			next = cur - 1
		}
		if next >= 1 && next <= len(wScenes) && act != "IDLE" {
			w.applyScene(next, true)
		}
	}
}

// applyScene recalls scene n (1-based): mute groups as the scene has
// them. w.mu held.
func (w *Wing) applyScene(n int, push bool) {
	set := func(path string, fn func(*wparam)) {
		if push {
			w.set(path, fn)
		} else if x := w.params[path]; x != nil {
			fn(x)
		}
	}
	set("/$ctl/lib/$actidx", func(p *wparam) { p.i = n })
	set("/$ctl/lib/$activeid", func(p *wparam) { p.i = n })
	set("/$ctl/lib/$active", func(p *wparam) { p.s = fmt.Sprintf("I:SHOWS/SUNDAY/scene_%d.snap", n) })
	for g, m := range wSceneMutes[n-1] {
		set(fmt.Sprintf("/mgrp/%d/mute", g+1), func(p *wparam) { p.i = b2i(m) })
	}
	w.recomputeEffective()
	if push {
		w.Log.Info("WING", "scene", n, "name", wScenes[n-1])
	}
}

// song moves the USB player by d songs. w.mu held.
func (w *Wing) song(d int) {
	cur := 0
	for i, s := range wSongs {
		if s.title == w.params["/play/$song"].s {
			cur = i
		}
	}
	cur = (cur + d + len(wSongs)) % len(wSongs)
	s := wSongs[cur]
	w.set("/play/$song", func(p *wparam) { p.s = s.title })
	w.set("/play/$artist", func(p *wparam) { p.s = s.artist })
	w.set("/play/$total", func(p *wparam) { p.f = s.secs })
	w.set("/play/$actfile", func(p *wparam) { p.s = fmt.Sprintf("U:MUSIC/%02d %s.wav", cur+1, s.title) })
	w.set("/play/$pos", func(p *wparam) { p.f = 0 })
}

// stripTags splits a tags value ("#D1,#M2").
func stripTags(s string) map[string]bool {
	out := map[string]bool{}
	for _, t := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		out[t] = true
	}
	return out
}

// recomputeEffective: $mute = 1 own mute, 2 muted by a mute group or
// DCA; $fdr = fader + DCA. w.mu held.
func (w *Wing) recomputeEffective() {
	for _, s := range wAllStrips() {
		if !wKinds[s.Kind].chLike {
			continue
		}
		p := s.Path()
		tags := stripTags(w.params[p+"/tags"].s)
		em := 0
		if w.params[p+"/mute"].i == 1 {
			em = 1
		} else {
			for g := 1; g <= 8; g++ {
				if tags[fmt.Sprintf("#M%d", g)] && w.params[fmt.Sprintf("/mgrp/%d/mute", g)].i == 1 {
					em = 2
				}
			}
			for d := 1; d <= 16; d++ {
				if tags[fmt.Sprintf("#D%d", d)] && w.params[fmt.Sprintf("/dca/%d/mute", d)].i == 1 {
					em = 2
				}
			}
		}
		if x := w.params[p+"/$mute"]; x.i != em {
			x.i = em
			w.changed(p + "/$mute")
		}
		ef := w.params[p+"/fdr"].f
		for d := 1; d <= 16; d++ {
			if tags[fmt.Sprintf("#D%d", d)] {
				ef = math.Max(-144, ef+math.Max(-144, w.params[fmt.Sprintf("/dca/%d/fdr", d)].f))
			}
		}
		if x := w.params[p+"/$fdr"]; x.f != ef {
			x.f = ef
			w.changed(p + "/$fdr")
		}
	}
}

// defsOf answers "<path> ,b dd": a node's children, or a parameter's own
// definition, then DE. w.mu held.
func (w *Wing) defsOf(path string) []byte {
	var out []byte
	def := func(full string, parent uint32, idx int) {
		if p := w.params[full]; p != nil {
			out = append(out, encodeDef(w.paramDef(full, p, parent, idx))...)
			return
		}
		name := full[strings.LastIndex(full, "/")+1:]
		out = append(out, encodeDef(nodeDef{Parent: parent, Hash: crc32.ChecksumIEEE([]byte(full)), Index: idx, Name: name, Long: strings.ToUpper(name)})...)
	}
	if p := w.params[path]; p != nil {
		def(path, crc32.ChecksumIEEE([]byte(path[:strings.LastIndex(path, "/")])), 0)
		return append(out, tokEnd)
	}
	kids, ok := w.nodes[path]
	if !ok {
		return nil
	}
	ph := crc32.ChecksumIEEE([]byte(path))
	for i, k := range kids {
		full := path + "/" + k
		if path == "/" {
			full = "/" + k
		}
		def(full, ph, i+1)
	}
	return append(out, tokEnd)
}

func (w *Wing) paramDef(path string, p *wparam, parent uint32, idx int) nodeDef {
	name := path[strings.LastIndex(path, "/")+1:]
	d := nodeDef{Parent: parent, Hash: p.hash, Index: idx, Name: name, Long: strings.ToUpper(name)}
	switch p.typ {
	case 'i':
		d.Flags = typeInt << 4
		d.Min, d.Max = p.min, p.max
	case 'f', 'l':
		if p.fader {
			d.Flags = typeFader<<4 | 1
		} else if p.typ == 'l' {
			d.Flags = typeLogF << 4
			d.Min, d.Max, d.Steps = p.min, p.max, 100
		} else {
			d.Flags = typeLinF << 4
			d.Min, d.Max, d.Steps = p.min, p.max, 1000
		}
	case 'e':
		d.Flags = typeEnum << 4
		d.Items = p.items
		d.ItemLong = p.items
	default:
		d.Flags = typeString << 4
		d.MaxLen = 64
	}
	if p.ro {
		d.Flags |= 1 << 9
	}
	return d
}

// ------------------------------------------------------------- native

func (w *Wing) serveTCP() {
	for {
		c, err := w.tcp.Accept()
		if err != nil {
			return
		}
		w.mu.Lock()
		if len(w.clients) >= 24 {
			w.mu.Unlock()
			c.Close()
			continue
		}
		cl := &wclient{conn: c, out: make(chan wframe, 4096)}
		w.clients = append(w.clients, cl)
		w.mu.Unlock()
		w.Log.Info("WING: native client connected", "from", c.RemoteAddr())
		go cl.writer()
		go w.serveClient(cl)
	}
}

func (cl *wclient) writer() {
	enc := newNRPEncoder()
	for f := range cl.out {
		cl.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		if _, err := cl.conn.Write(enc.frame(f.ch, f.b)); err != nil {
			cl.conn.Close()
		}
	}
}

func (cl *wclient) send(ch int, b []byte) {
	select {
	case cl.out <- wframe{ch, b}:
	default: // a client that doesn't read falls behind and loses events
	}
}

func (w *Wing) drop(cl *wclient) {
	w.mu.Lock()
	for i, x := range w.clients {
		if x == cl {
			w.clients = append(w.clients[:i], w.clients[i+1:]...)
			close(cl.out)
			break
		}
	}
	w.mu.Unlock()
	cl.conn.Close()
	w.Log.Info("WING: native client gone", "from", cl.conn.RemoteAddr())
}

func (w *Wing) serveClient(cl *wclient) {
	defer w.drop(cl)
	dec := newNRPDecoder()
	var ch1, ch3 []byte
	buf := make([]byte, 4096)
	for {
		cl.conn.SetReadDeadline(time.Now().Add(10 * time.Second)) // the console's idle timeout
		n, err := cl.conn.Read(buf)
		if err != nil {
			return
		}
		dec.feed(buf[:n], func(ch int, db byte) {
			switch ch {
			case nrpAudio:
				ch1 = append(ch1, db)
			case nrpMeters:
				ch3 = append(ch3, db)
			}
		})
		// channel 1: DA DD (root definition) is all a client sends here
		for len(ch1) >= 2 {
			if ch1[0] == tokRoot && ch1[1] == tokDefReq {
				w.mu.Lock()
				d := w.defsOf("/")
				w.mu.Unlock()
				cl.send(nrpAudio, d)
				ch1 = ch1[2:]
				continue
			}
			ch1 = ch1[1:]
		}
		ch3 = w.meterTokens(cl, ch3)
	}
}

// meterTokens parses channel-3 requests: D3 port.w, D4 id.l, then DC
// <family index>... DE for a definition, or D4 id alone to renew.
func (w *Wing) meterTokens(cl *wclient, b []byte) []byte {
	fam := map[byte]string{}
	for k, f := range wMeterFamily {
		fam[f.tok] = k
	}
	for len(b) > 0 {
		switch b[0] {
		case tokI16:
			if len(b) < 3 {
				return b
			}
			w.mu.Lock()
			cl.port = int(binary.BigEndian.Uint16(b[1:]))
			w.mu.Unlock()
			b = b[3:]
		case tokI32:
			if len(b) < 5 {
				return b
			}
			id := binary.BigEndian.Uint32(b[1:])
			rest := b[5:]
			if len(rest) > 0 && rest[0] == tokReq {
				end := -1
				for i := 1; i < len(rest); i++ {
					if rest[i] == tokEnd {
						end = i
						break
					}
				}
				if end < 0 {
					return b
				}
				var strips []wStrip
				for i := 1; i+1 < end; i += 2 {
					if k, ok := fam[rest[i]]; ok {
						strips = append(strips, wStrip{k, int(rest[i+1]) + 1})
					}
				}
				w.mu.Lock()
				cl.id, cl.strips, cl.renewed = id, strips, time.Now()
				w.mu.Unlock()
				b = rest[end+1:]
				continue
			}
			w.mu.Lock()
			if id == cl.id {
				cl.renewed = time.Now()
			}
			w.mu.Unlock()
			b = rest
		default:
			b = b[1:]
		}
	}
	return b
}

// changed pushes D7 <hash> <value> to every native client. w.mu held.
func (w *Wing) changed(path string) {
	p := w.params[path]
	var v nval
	switch p.typ {
	case 'i', 'e': // enums travel as their index
		v = nval{kind: 'i', i: int64(p.i)}
	case 'f', 'l':
		v = nval{kind: 'f', f: p.f}
	default:
		v = nval{kind: 's', s: p.s}
	}
	msg := binary.BigEndian.AppendUint32([]byte{tokHash}, p.hash)
	msg = append(msg, encodeValue(v)...)
	for _, cl := range w.clients {
		cl.send(nrpAudio, msg)
	}
}

// ------------------------------------------------------------- meters

// run sends meters every 50 ms and moves the recorder / player clocks.
func (w *Wing) run() {
	t := time.NewTicker(50 * time.Millisecond)
	defer t.Stop()
	last := time.Now()
	for {
		select {
		case <-w.done:
			return
		case <-t.C:
		}
		now := time.Now()
		w.mu.Lock()
		if now.Sub(last) >= time.Second {
			w.tickTransport(now.Sub(last).Seconds())
			last = now
		}
		var lv map[wStrip]wlevel
		for _, cl := range w.clients {
			if cl.port == 0 || len(cl.strips) == 0 || time.Since(cl.renewed) > 5*time.Second {
				continue
			}
			if lv == nil {
				lv = w.levels(now.Sub(w.start).Seconds())
			}
			b := binary.BigEndian.AppendUint32(nil, cl.id)
			for _, s := range cl.strips {
				b = lv[s].append(b, wMeterFamily[s.Kind].words)
			}
			host := cl.conn.RemoteAddr().(*net.TCPAddr).IP
			w.mpc.WriteTo(b, &net.UDPAddr{IP: host, Port: cl.port})
		}
		w.mu.Unlock()
	}
}

// tickTransport moves the recorder's and player's clocks. w.mu held.
func (w *Wing) tickTransport(dt float64) {
	if w.params["/rec/$actstate"].i == 1 {
		w.set("/rec/$time", func(p *wparam) { p.f = math.Round(p.f + dt) })
	}
	if w.params["/play/$actstate"].i == 1 {
		pos, total := w.params["/play/$pos"].f+dt, w.params["/play/$total"].f
		if pos >= total {
			w.song(1)
		} else {
			w.set("/play/$pos", func(p *wparam) { p.f = math.Round(pos) })
		}
	}
}

// wlevel is one strip's meter reading (dB).
type wlevel struct {
	inL, inR, outL, outR float64
	gr                   float64
	gate, dyn            bool
}

const wFloor = -120.0

func (l wlevel) append(b []byte, words int) []byte {
	word := func(db float64) uint16 {
		db = math.Max(wFloor, math.Min(20, db))
		return uint16(int16(db * 256))
	}
	vals := []uint16{word(l.inL), word(l.inR), word(l.outL), word(l.outR), 0, 0, 0,
		uint16(int16(-l.gr * 256 / 20)), uint16(b2i(l.gate)), uint16(b2i(l.dyn)), 0}
	for i := 0; i < words; i++ {
		v := uint16(0)
		if i < len(vals) {
			v = vals[i]
		}
		b = binary.BigEndian.AppendUint16(b, v)
	}
	return b
}

// source is what a strip's input "hears" at time t (dBFS).
func (s wsource) at(t float64) float64 {
	ph := s.phase
	jit := (rand.Float64() - 0.5) * 2.5
	beat := 60.0 / 76 // 76 bpm
	switch s.kind {
	case "speech":
		if math.Sin(2*math.Pi*0.11*t+ph) > 0.8 { // a pause between phrases
			return -62 + jit*2
		}
		syl := math.Abs(math.Sin(2*math.Pi*3.3*t + ph*1.7))
		return s.level - 14 + 16*syl + 3*math.Sin(2*math.Pi*0.4*t+ph) + jit
	case "sing":
		if math.Sin(2*math.Pi*0.05*t+ph) > 0.92 { // between verses
			return -58 + jit*2
		}
		return s.level - 3 + 4*math.Sin(2*math.Pi*0.31*t+ph) + 3*math.Abs(math.Sin(2*math.Pi*1.9*t+ph)) + jit
	case "kick", "snare":
		period, off := beat, 0.0
		if s.kind == "snare" {
			period, off = 2*beat, beat
		}
		p := math.Mod(t+off, period) / period
		return math.Max(-60, s.level+6-55*p) + jit
	case "bass":
		p := math.Mod(t, beat/2) / (beat / 2)
		return s.level + 2 - 8*p + 2*math.Sin(2*math.Pi*0.2*t+ph) + jit
	case "cymbals":
		p := math.Mod(t, beat/2) / (beat / 2)
		return s.level + 3 - 10*p + jit
	case "room":
		return s.level + 4*math.Sin(2*math.Pi*0.07*t+ph) + jit
	case "":
		return -100
	}
	return s.level + 3*math.Sin(2*math.Pi*0.23*t+ph) + 2*math.Sin(2*math.Pi*1.3*t+ph*2) + jit
}

func powerSum(dbs ...float64) float64 {
	sum := 0.0
	for _, d := range dbs {
		if d > wFloor {
			sum += math.Pow(10, d/10)
		}
	}
	if sum == 0 {
		return wFloor
	}
	return 10 * math.Log10(sum)
}

// levels computes every strip's meters at time t. w.mu held.
func (w *Wing) levels(t float64) map[wStrip]wlevel {
	out := map[wStrip]wlevel{}
	post := func(s wStrip, in float64) float64 {
		p := s.Path()
		if w.params[p+"/$mute"].i != 0 || w.params[p+"/$fdr"].f <= -144 {
			return wFloor
		}
		return in + w.params[p+"/$fdr"].f
	}
	stereoOf := func(s wStrip) bool {
		if x := w.params[s.Path()+"/in/set/$mode"]; x != nil {
			return x.i > 0
		}
		if x := w.params[s.Path()+"/busmono"]; x != nil {
			return x.i == 0
		}
		return false
	}
	strip := func(s wStrip, in float64, sig wsource) wlevel {
		l := wlevel{inL: in, inR: in}
		if stereoOf(s) {
			l.inR = in - 1 + 2*math.Sin(t*1.7+sig.phase)
		}
		p := s.Path()
		if x := w.params[p+"/gate/on"]; x != nil && x.i == 1 {
			l.gate = in > w.params[p+"/gate/thr"].f
		}
		if x := w.params[p+"/dyn/on"]; x != nil && x.i == 1 {
			if over := in - w.params[p+"/dyn/thr"].f; over > 0 {
				l.gr = math.Min(15, over*(1-1/3.0))
				l.dyn = l.gr > 0.5
			}
		}
		l.outL, l.outR = post(s, l.inL-l.gr), post(s, l.inR-l.gr)
		if x := w.params[p+"/gate/on"]; x != nil && x.i == 1 && !l.gate {
			l.outL, l.outR = wFloor, wFloor
		}
		return l
	}
	var mainL, mainR []float64
	for _, k := range []string{"ch", "aux"} {
		for n := 1; n <= wKinds[k].count; n++ {
			s := wStrip{k, n}
			sig, ok := w.sig[s]
			in := -100.0
			if ok {
				in = sig.at(t) + w.params[s.Path()+"/in/set/trim"].f
			}
			l := strip(s, in, sig)
			out[s] = l
			if w.params[s.Path()+"/main/1/on"].i == 1 {
				mainL, mainR = append(mainL, l.outL), append(mainR, l.outR)
			}
		}
	}
	mix := wlevel{inL: powerSum(mainL...), inR: powerSum(mainR...)}
	for n := 1; n <= 16; n++ {
		s := wStrip{"bus", n}
		in := -100.0
		if _, ok := wNames[s]; ok {
			in = mix.inL - 4 - float64(n%3) + (rand.Float64()-0.5)*1.5
		}
		out[s] = strip(s, in, wsource{phase: float64(n)})
	}
	for n := 1; n <= 4; n++ {
		s := wStrip{"main", n}
		in := -100.0
		if _, ok := wNames[s]; ok {
			in = mix.inL - float64(n-1)*3
		}
		l := strip(s, in, wsource{phase: 1})
		if n == 1 {
			l.inR = mix.inR
			l.outR = post(s, l.inR-l.gr)
		}
		out[s] = l
	}
	for n := 1; n <= 8; n++ {
		s := wStrip{"mtx", n}
		in := -100.0
		if _, ok := wNames[s]; ok {
			in = out[wStrip{"main", 1}].outL - 2
		}
		out[s] = strip(s, in, wsource{phase: 2})
	}
	for d := 1; d <= 16; d++ {
		s := wStrip{"dca", d}
		l := wlevel{inL: wFloor, inR: wFloor, outL: wFloor, outR: wFloor}
		tag := fmt.Sprintf("#D%d", d)
		for n := 1; n <= 40; n++ {
			c := wStrip{"ch", n}
			if stripTags(w.params[c.Path()+"/tags"].s)[tag] {
				m := out[c]
				l.inL, l.inR = math.Max(l.inL, m.inL), math.Max(l.inR, m.inR)
				l.outL, l.outR = math.Max(l.outL, m.outL), math.Max(l.outR, m.outR)
			}
		}
		out[s] = l
	}
	return out
}

// ---------------------------------------------------------- inspection

// Value reads a parameter: its number (ints and enum indexes as numbers)
// and its text. ok is false for an unknown path.
func (w *Wing) Value(path string) (num float64, text string, ok bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	p := w.params[path]
	if p == nil {
		return 0, "", false
	}
	switch p.typ {
	case 'i', 'e':
		num = float64(p.i)
	case 'f', 'l':
		num = p.f
	}
	return num, w.text(p), true
}

// Clients is how many native clients are connected.
func (w *Wing) Clients() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.clients)
}
