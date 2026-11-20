package sim

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

// A service plan: items, each with slides. Placeholder text only.
type item struct {
	ID, Title string
	BG        color.RGBA
	Slides    [][]string // first line is the heading
}

var plan = []item{
	{"welcome", "Welcome", color.RGBA{22, 48, 86, 255}, [][]string{{"Welcome", "We're glad you're here", "Service starts at 10:00"}}},
	{"song1", "Opening Song", color.RGBA{60, 30, 70, 255}, [][]string{
		{"Opening Song", "Verse 1"}, {"Line one of the first verse", "line two of the first verse"},
		{"Chorus", "Sing the chorus line here", "and the second chorus line"}, {"Line one of the second verse", "line two of the second verse"},
		{"Chorus", "Sing the chorus line here", "and the second chorus line"}}},
	{"news", "Announcements", color.RGBA{18, 70, 60, 255}, [][]string{
		{"Community Dinner", "Friday · 6:00 PM", "Everyone welcome"}, {"Volunteers Wanted", "Sound, cameras and slides", "Sign up at the booth"},
		{"Next Week", "Guest speaker", "Family day after the service"}}},
	{"talk", "Message", color.RGBA{70, 44, 20, 255}, [][]string{
		{"Today's Message", "Part 3 of the series"}, {"Point 1", "Start with the first idea"}, {"Point 2", "Build on it with the second"},
		{"Point 3", "Bring it home"}, {"Reading", "Passage for today, line one", "and line two"}, {"Take Away", "One thing to remember this week"}}},
	{"song2", "Closing Song", color.RGBA{60, 30, 70, 255}, [][]string{{"Closing Song", "Verse 1"}, {"Closing line one", "closing line two"}}},
	{"bye", "Thank You", color.RGBA{22, 48, 86, 255}, [][]string{{"Thank You", "See you next week"}}},
}

// Slides is the slide show: the current item and slide, rendered.
type Slides struct {
	mu       sync.Mutex
	item     int
	slide    int
	blank    bool
	rev      int
	f        fonts
	img      *image.RGBA
	OnChange func(img *image.RGBA)
}

func NewSlides() *Slides {
	s := &Slides{f: loadFonts(), item: 0}
	s.render()
	return s
}

// Image is the current slide (1280×720).
func (s *Slides) Image() *image.RGBA {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.img
}

func (s *Slides) render() {
	img := image.NewRGBA(image.Rect(0, 0, 1280, 720))
	it := plan[s.item]
	if s.blank {
		draw.Draw(img, img.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)
	} else {
		for y := 0; y < 720; y++ {
			k := 1 - float64(y)/720*0.5
			c := color.RGBA{uint8(float64(it.BG.R) * k), uint8(float64(it.BG.G) * k), uint8(float64(it.BG.B) * k), 255}
			draw.Draw(img, image.Rect(0, y, 1280, y+1), image.NewUniform(c), image.Point{}, draw.Src)
		}
		lines := it.Slides[s.slide]
		y := 360 - len(lines)*40
		for i, l := range lines {
			f := s.f.title
			if i == 0 {
				f = s.f.big
			}
			centered(img, f, y+i*84, l, color.White)
		}
		centered(img, s.f.small, 690, fmt.Sprintf("%s · %d/%d", it.Title, s.slide+1, len(it.Slides)), color.RGBA{200, 200, 210, 255})
	}
	s.img = img
	s.rev++
	if s.OnChange != nil {
		go s.OnChange(img)
	}
}

func centered(img *image.RGBA, f font.Face, y int, txt string, c color.Color) {
	d := &font.Drawer{Dst: img, Src: image.NewUniform(c), Face: f}
	w := d.MeasureString(txt).Ceil()
	d.Dot = fixed.P((img.Bounds().Dx()-w)/2, y)
	d.DrawString(txt)
}

func (s *Slides) do(cmd string, index int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	it := plan[s.item]
	switch cmd {
	case "NextSlide":
		if s.slide+1 < len(it.Slides) {
			s.slide++
		} else if s.item+1 < len(plan) {
			s.item, s.slide = s.item+1, 0
		}
	case "PreviousSlide":
		if s.slide > 0 {
			s.slide--
		} else if s.item > 0 {
			s.item--
			s.slide = len(plan[s.item].Slides) - 1
		}
	case "NextServiceItem":
		s.item, s.slide = min(s.item+1, len(plan)-1), 0
	case "PreviousServiceItem":
		s.item, s.slide = max(s.item-1, 0), 0
	case "GoToServiceItem":
		if index < 1 || index > len(plan) {
			return false
		}
		s.item, s.slide = index-1, 0
	case "StartPreService", "StartService", "StartPostService", "GoOnAir", "GoOffAir":
	case "ShowBlankQuickScreen", "ShowLogoQuickScreen":
		s.blank = true
	case "ShowLastSlide":
		s.blank = false
	default:
		return false
	}
	if cmd == "NextSlide" || cmd == "PreviousSlide" || cmd == "GoToServiceItem" || cmd == "NextServiceItem" || cmd == "PreviousServiceItem" {
		s.blank = false
	}
	s.render()
	return true
}

// ServeProclaim answers like Proclaim's network control (port 52195).
func (s *Slides) ServeProclaim(addr string, log *slog.Logger) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("slides (Proclaim) on %s: %w", addr, err)
	}
	mux := http.NewServeMux()
	j := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("POST /appCommand/authenticate", func(w http.ResponseWriter, r *http.Request) {
		j(w, map[string]string{"proclaimAuthToken": "sim-token"})
	})
	mux.HandleFunc("GET /onair/session", func(w http.ResponseWriter, r *http.Request) { j(w, "sim-session") })
	mux.HandleFunc("GET /presentations/onair", func(w http.ResponseWriter, r *http.Request) {
		items := []map[string]any{}
		for _, it := range plan {
			sl := []map[string]any{}
			for i := range it.Slides {
				sl = append(sl, map[string]any{"index": i})
			}
			items = append(items, map[string]any{"id": it.ID, "title": it.Title, "kind": "Content", "slides": sl})
		}
		j(w, map[string]any{"id": "sim", "title": "Weekend Service", "localRevision": 1, "serviceItems": items})
	})
	mux.HandleFunc("GET /onair/statusChanged", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		st := map[string]any{"presentationLocalRevision": 1, "status": map[string]any{"itemId": plan[s.item].ID, "slideIndex": s.slide,
			"quickScreenKind": map[bool]string{true: "Blank", false: "None"}[s.blank]}}
		s.mu.Unlock()
		j(w, st)
	})
	mux.HandleFunc("GET /appCommand/perform", func(w http.ResponseWriter, r *http.Request) {
		cmd := r.URL.Query().Get("appCommandName")
		idx, _ := strconv.Atoi(r.URL.Query().Get("index"))
		if !s.do(cmd, idx) {
			http.Error(w, "unknown command", 400)
			return
		}
		log.Info("slides", "command", cmd)
		w.Write([]byte("success"))
	})
	go http.Serve(ln, mux)
	return nil
}
