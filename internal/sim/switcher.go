package sim

import (
	"encoding/xml"
	"fmt"
	"html"
	"image"
	"image/color"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Input is one switcher source: a camera or the slides.
type Input struct {
	Title string
	Type  string // Capture | PowerPoint
	Frame func() *image.RGBA
}

// Switcher answers like vMix's Web Controller API and makes the
// program picture (with fades) for the program feed.
type Switcher struct {
	mu        sync.Mutex
	inputs    []Input
	preview   int // 1-based
	active    int
	fadeFrom  int
	fadeStart time.Time
	fadeDur   time.Duration
	streaming bool
	recording bool
	Log       *slog.Logger
}

func NewSwitcher(inputs []Input) *Switcher {
	return &Switcher{inputs: inputs, active: 1, preview: min(2, len(inputs)), streaming: true, Log: slog.Default()}
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

func (s *Switcher) find(v string) int {
	if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= len(s.inputs) {
		return n
	}
	for i, in := range s.inputs {
		if strings.EqualFold(in.Title, v) {
			return i + 1
		}
	}
	return 0
}

func (s *Switcher) function(q map[string][]string) error {
	get := func(k string) string {
		if v := q[k]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch fn := get("Function"); fn {
	case "PreviewInput":
		n := s.find(get("Input"))
		if n == 0 {
			return fmt.Errorf("no input %q", get("Input"))
		}
		s.preview = n
	case "ActiveInput", "CutDirect":
		n := s.find(get("Input"))
		if n == 0 {
			return fmt.Errorf("no input %q", get("Input"))
		}
		s.fadeDur = 0
		s.preview, s.active = s.active, n
	case "Cut":
		s.fadeDur = 0
		s.preview, s.active = s.active, s.preview
	case "Fade", "Transition1", "Merge", "Stinger1":
		d, _ := strconv.Atoi(get("Duration"))
		if d <= 0 {
			d = 1000
		}
		s.fadeFrom, s.fadeStart, s.fadeDur = s.active, time.Now(), time.Duration(d)*time.Millisecond
		s.preview, s.active = s.active, s.preview
	case "StartStreaming":
		s.streaming = true
	case "StopStreaming":
		s.streaming = false
	case "StartStopStreaming":
		s.streaming = !s.streaming
	case "StartRecording":
		s.recording = true
	case "StopRecording":
		s.recording = false
	case "StartStopRecording":
		s.recording = !s.recording
	default:
		return fmt.Errorf("function %q not simulated", fn)
	}
	return nil
}

func (s *Switcher) stateXML() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	tf := map[bool]string{true: "True", false: "False"}
	var b strings.Builder
	b.WriteString(`<vmix><version>27.0.0.49</version><edition>4K</edition><inputs>`)
	for i, in := range s.inputs {
		fmt.Fprintf(&b, `<input key="sim-%d" number="%d" type="%s" title="%s" state="Running">%s</input>`,
			i+1, i+1, in.Type, html.EscapeString(in.Title), html.EscapeString(in.Title))
	}
	fmt.Fprintf(&b, `</inputs><preview>%d</preview><active>%d</active><fadeToBlack>False</fadeToBlack><recording>%s</recording><streaming>%s</streaming></vmix>`,
		s.preview, s.active, tf[s.recording], tf[s.streaming])
	return []byte(b.String())
}

// Serve listens on addr (vMix uses 8088).
func (s *Switcher) Serve(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("switcher (vMix) on %s: %w", addr, err)
	}
	go http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api") {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		if q.Get("Function") != "" {
			if err := s.function(q); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			s.Log.Info("switcher", "function", q.Get("Function"), "input", q.Get("Input"))
			w.Write([]byte("Function completed successfully."))
			return
		}
		w.Header().Set("Content-Type", "text/xml")
		w.Write(s.stateXML())
	}))
	return nil
}

var _ = xml.Name{}

// Program draws the program picture into dst (crossfading during a fade).
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
	in := s.inputs
	s.mu.Unlock()
	a := in[active-1].Frame()
	if k >= 1 || from < 1 {
		copy(dst.Pix, a.Pix)
		return
	}
	b := in[from-1].Frame()
	for i := range dst.Pix {
		dst.Pix[i] = uint8(float64(b.Pix[i])*(1-k) + float64(a.Pix[i])*k)
	}
}

var (
	tallyRed   = color.RGBA{230, 30, 30, 255}
	tallyGreen = color.RGBA{30, 200, 60, 255}
)
