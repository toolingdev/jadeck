package sim

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

// A service plan: items, each with slides. Placeholder text only.
type item struct {
	ID, Title string
	BG        color.RGBA
	Slides    [][]string // first line is the heading
	Kind      string     // Proclaim's item kind
	Notes     string     // HTML, as Proclaim sends it
	Parts     []string   // songs: each slide's part label (V1, C1, ...), as Proclaim names them
}

var plan = []item{
	{guid(1), "Welcome", color.RGBA{22, 48, 86, 255}, [][]string{{"Welcome", "We're glad you're here", "Service starts at 10:00"}}, "Content", "", nil},
	{guid(2), "Opening Song", color.RGBA{60, 30, 70, 255}, [][]string{
		{"Opening Song", "Verse 1"}, {"Line one of the first verse", "line two of the first verse"},
		{"Chorus", "Sing the chorus line here", "and the second chorus line"}, {"Line one of the second verse", "line two of the second verse"},
		{"Chorus", "Sing the chorus line here", "and the second chorus line"}}, "SongLyrics",
		"<p>Band starts; <b>two</b> bars intro.</p>", []string{"", "V1", "C1", "V2", "C1"}},
	{guid(3), "Announcements", color.RGBA{18, 70, 60, 255}, [][]string{
		{"Community Dinner", "Friday · 6:00 PM", "Everyone welcome"}, {"Volunteers Wanted", "Sound, cameras and slides", "Sign up at the booth"},
		{"Next Week", "Guest speaker", "Family day after the service"}}, "Announcement2", "", nil},
	{guid(4), "Message", color.RGBA{70, 44, 20, 255}, [][]string{
		{"Today's Message", "Part 3 of the series"}, {"Point 1", "Start with the first idea"}, {"Point 2", "Build on it with the second"},
		{"Point 3", "Bring it home"}, {"Reading", "Passage for today, line one", "and line two"}, {"Take Away", "One thing to remember this week"}},
		"Content", "<p>Pause after the reading. Camera 2 wide on the pulpit.</p>", nil},
	{guid(5), "Closing Song", color.RGBA{60, 30, 70, 255}, [][]string{{"Closing Song", "Verse 1"}, {"Closing line one", "closing line two"}},
		"SongLyrics", "", []string{"", "V1"}},
	{guid(6), "Thank You", color.RGBA{22, 48, 86, 255}, [][]string{{"Thank You", "See you next week"}}, "Content", "", nil},
}

// The service's own (custom) quick screens.
var customQuick = []struct{ ID, Title string }{
	{"5e0f0000-0000-4000-8000-000000000201", "Prayer Request"},
	{"5e0f0000-0000-4000-8000-000000000202", "Offering"},
}

// Proclaim's built-in quick screens the app commands show.
var builtinQuick = map[string]string{
	"showamenquickscreen": "Amen", "showhallelujahquickscreen": "Hallelujah", "showpraisethelordquickscreen": "PraiseTheLord",
	"showfloatingheartsquickscreen": "FloatingHeart", "showfloatingamensquickscreen": "FloatingAmen",
	"showheisrisenquickscreen": "HeIsRisen", "showheisrisenwhitequickscreen": "HeIsRisenWhite",
}

// Words shown for the built-in quick screens.
var quickWords = map[string]string{
	"Amen": "Amen", "Hallelujah": "Hallelujah", "PraiseTheLord": "Praise the Lord", "FloatingHeart": "♥  ♥  ♥",
	"FloatingAmen": "Amen  Amen", "HeIsRisen": "He is risen", "HeIsRisenWhite": "He is risen",
}

// Song parts the ShowSongLyrics…ByIndex commands go to, as Proclaim
// labels them.
var songPartLabel = map[string]string{
	"showsonglyricsversebyindex": "V%d", "showsonglyricschorusbyindex": "C%d", "showsonglyricsprechorusbyindex": "PC%d",
	"showsonglyricsbridgebyindex": "Bridge %d", "showsonglyricsinterludebyindex": "Interlude %d",
	"showsonglyricsendingbyindex": "Ending %d", "showsonglyricstagbyindex": "Tag %d",
}

// Where each part of the plan starts, as Proclaim reports it: Welcome is
// the warm-up, Thank You the post-service.
const (
	warmupStart      = 0
	serviceStart     = 1
	postServiceStart = 5
	presentationID   = "5e0f0000-0000-4000-8000-000000000001"
	controlPassword  = "Proclaim" // fixed in Proclaim
)

func guid(n int) string { return fmt.Sprintf("5e0f0000-0000-4000-8000-%012d", n+100) }

// Slides is the slide show: the current item and slide, rendered.
type Slides struct {
	mu       sync.Mutex
	item     int
	slide    int
	qs       string // quick screen kind: None, Blank, Logo, NoText, OnScreenBible
	qsData   string
	media    string
	onAir    bool
	sessions int
	session  string
	step     int64         // on-air step, bumped on every change
	wake     chan struct{} // closed on every change (long polls)
	start    time.Time     // when the service started (Proclaim's startTime)
	f        fonts
	img      *image.RGBA
	pics     map[string][]byte // rendered slide pictures (PNG) by item/slide/kind/width
	OnChange func(img *image.RGBA)
}

func NewSlides() *Slides {
	s := &Slides{f: loadFonts(), item: 0, qs: "None", wake: make(chan struct{}), pics: map[string][]byte{},
		start: time.Now().Add(-3 * time.Minute).Truncate(time.Minute)}
	s.goOnAir()
	s.render()
	return s
}

func (s *Slides) goOnAir() {
	s.onAir = true
	s.sessions++
	s.session = fmt.Sprintf("%032x", 0x5e0f0000+s.sessions)
}

// Image is the current slide (1280×720).
func (s *Slides) Image() *image.RGBA {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.img
}

func (s *Slides) blank() bool { return s.qs == "Blank" || s.qs == "Logo" }

func (s *Slides) render() {
	img := image.NewRGBA(image.Rect(0, 0, 1280, 720))
	it := plan[s.item]
	if s.blank() {
		draw.Draw(img, img.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)
	} else {
		bg := it.BG
		if s.qs == "OnScreenBible" {
			bg = color.RGBA{30, 30, 36, 255}
		}
		for y := 0; y < 720; y++ {
			k := 1 - float64(y)/720*0.5
			c := color.RGBA{uint8(float64(bg.R) * k), uint8(float64(bg.G) * k), uint8(float64(bg.B) * k), 255}
			draw.Draw(img, image.Rect(0, y, 1280, y+1), image.NewUniform(c), image.Point{}, draw.Src)
		}
		switch s.qs {
		case "NoText":
		case "OnScreenBible":
			centered(img, s.f.big, 330, s.qsData, color.White)
			centered(img, s.f.title, 420, "(on-screen Bible)", color.RGBA{200, 200, 210, 255})
		case "Custom":
			title := "Custom quick screen"
			for _, q := range customQuick {
				if strings.EqualFold(q.ID, s.qsData) {
					title = q.Title
				}
			}
			centered(img, s.f.big, 380, title, color.White)
		default:
			if w, ok := quickWords[s.qs]; ok {
				centered(img, s.f.big, 380, w, color.White)
				break
			}
			s.drawText(img, s.item, s.slide)
		}
	}
	s.img = img
	s.step++
	close(s.wake)
	s.wake = make(chan struct{})
	if s.OnChange != nil {
		go s.OnChange(img)
	}
}

// drawText draws slide n of item i's text.
func (s *Slides) drawText(img *image.RGBA, i, n int) {
	it := plan[i]
	lines := it.Slides[n]
	y := 360 - len(lines)*40
	for k, l := range lines {
		f := s.f.title
		if k == 0 {
			f = s.f.big
		}
		centered(img, f, y+k*84, l, color.White)
	}
	centered(img, s.f.small, 690, fmt.Sprintf("%s · %d/%d", it.Title, n+1, len(it.Slides)), color.RGBA{200, 200, 210, 255})
}

// picture is slide n of item i as Proclaim's image route draws it, width
// px wide, PNG: the slide itself, or (confidence) the stage view with the
// next slide's first line. Callers hold s.mu.
func (s *Slides) picture(i, n, width int, confidence bool) []byte {
	key := fmt.Sprintf("%d/%d/%v/%d", i, n, confidence, width)
	if b, ok := s.pics[key]; ok {
		return b
	}
	img := image.NewRGBA(image.Rect(0, 0, 1280, 720))
	if confidence {
		draw.Draw(img, img.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)
		lines := plan[i].Slides[n]
		for k, l := range lines {
			left(img, s.f.big, 60, 130+k*90, l, color.White)
		}
		next := "End of the service"
		if n+1 < len(plan[i].Slides) {
			next = plan[i].Slides[n+1][0]
		} else if i+1 < len(plan) {
			next = plan[i+1].Title + " — " + plan[i+1].Slides[0][0]
		}
		left(img, s.f.title, 60, 590, "NEXT  "+next, color.RGBA{250, 200, 70, 255})
		left(img, s.f.label, 60, 670, fmt.Sprintf("%s · slide %d of %d", plan[i].Title, n+1, len(plan[i].Slides)), color.RGBA{170, 170, 180, 255})
	} else {
		bg := plan[i].BG
		for y := 0; y < 720; y++ {
			k := 1 - float64(y)/720*0.5
			c := color.RGBA{uint8(float64(bg.R) * k), uint8(float64(bg.G) * k), uint8(float64(bg.B) * k), 255}
			draw.Draw(img, image.Rect(0, y, 1280, y+1), image.NewUniform(c), image.Point{}, draw.Src)
		}
		s.drawText(img, i, n)
	}
	out := image.Image(img)
	if width != 1280 {
		dst := image.NewRGBA(image.Rect(0, 0, width, width*720/1280))
		xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, img.Bounds(), xdraw.Src, nil)
		out = dst
	}
	var b bytes.Buffer
	(&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&b, out)
	if len(s.pics) > 256 {
		s.pics = map[string][]byte{}
	}
	s.pics[key] = b.Bytes()
	return b.Bytes()
}

func left(img *image.RGBA, f font.Face, x, y int, txt string, c color.Color) {
	d := &font.Drawer{Dst: img, Src: image.NewUniform(c), Face: f, Dot: fixed.P(x, y)}
	d.DrawString(txt)
}

func centered(img *image.RGBA, f font.Face, y int, txt string, c color.Color) {
	d := &font.Drawer{Dst: img, Src: image.NewUniform(c), Face: f}
	w := d.MeasureString(txt).Ceil()
	d.Dot = fixed.P((img.Bounds().Dx()-w)/2, y)
	d.DrawString(txt)
}

// goTo shows item i, slide n (callers hold s.mu).
func (s *Slides) goTo(i, n int) {
	s.item, s.slide, s.qs, s.qsData, s.media = i, n, "None", "", ""
}

// do runs a Proclaim app command (names are case-insensitive, index is
// 1-based where a command takes one).
func (s *Slides) do(cmd string, index int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	it := plan[s.item]
	switch strings.ToLower(cmd) {
	case "nextslide":
		if s.slide+1 < len(it.Slides) {
			s.goTo(s.item, s.slide+1)
		} else if s.item+1 < len(plan) {
			s.goTo(s.item+1, 0)
		} else {
			s.goTo(s.item, s.slide)
		}
	case "previousslide":
		if s.slide > 0 {
			s.goTo(s.item, s.slide-1)
		} else if s.item > 0 {
			s.goTo(s.item-1, len(plan[s.item-1].Slides)-1)
		} else {
			s.goTo(s.item, s.slide)
		}
	case "nextserviceitem":
		s.goTo(min(s.item+1, len(plan)-1), 0)
	case "previousserviceitem":
		s.goTo(max(s.item-1, 0), 0)
	case "gotoserviceitem":
		// Counts from the service start and skips items without slides,
		// like Proclaim; it can't reach the warm-up.
		k := 0
		found := false
		for i := serviceStart; i < len(plan) && !found; i++ {
			if len(plan[i].Slides) > 0 {
				if k++; k == index {
					s.goTo(i, 0)
					found = true
				}
			}
		}
		if !found {
			return false
		}
	case "gotoslide":
		if index < 1 || index > len(it.Slides) {
			return false
		}
		s.goTo(s.item, index-1)
	case "startpreservice", "startwarmup":
		s.goTo(warmupStart, 0)
	case "startservice":
		s.goTo(serviceStart, 0)
	case "startpostservice":
		s.goTo(postServiceStart, 0)
	case "goonair":
		if !s.onAir {
			s.goOnAir()
		}
	case "gooffair":
		s.onAir = false
	case "showblankquickscreen":
		s.qs = "Blank"
	case "showlogoquickscreen":
		s.qs = "Logo"
	case "shownotextquickscreen":
		s.qs = "NoText"
	case "showlastslide", "showtext":
		s.qs, s.qsData = "None", ""
	case "showcustomquickscreen":
		if index < 1 || index > len(customQuick) {
			return true // Proclaim ignores an index it doesn't have
		}
		s.qs, s.qsData = "Custom", customQuick[index-1].ID
	case "videorestart", "videorewind", "videofastforward", "nextaudioitem", "previouspreviousaudioitem":
	case "videoplay":
		s.media = "Playing"
	case "videopause":
		s.media = "Paused"
	default:
		if k, ok := builtinQuick[strings.ToLower(cmd)]; ok {
			s.qs = k
			break
		}
		if f, ok := songPartLabel[strings.ToLower(cmd)]; ok {
			// Proclaim goes to the first slide with that label, if the
			// song on screen has one; otherwise nothing happens.
			want := fmt.Sprintf(f, max(index, 1))
			for n, p := range it.Parts {
				if p == want {
					if n != s.slide {
						s.goTo(s.item, n)
						s.render()
					}
					break
				}
			}
			return true
		}
		return false
	}
	s.render()
	return true
}

func (s *Slides) status() map[string]any {
	var qsData, media any
	if s.qsData != "" {
		qsData = s.qsData
	}
	if s.media != "" {
		media = s.media
	}
	return map[string]any{"presentationId": presentationID, "presentationLocalRevision": 1, "status": map[string]any{
		"revision": s.step, "itemId": plan[s.item].ID, "slideIndex": s.slide,
		"quickScreen": nil, "quickScreenData": qsData, "quickScreenKind": s.qs, "mediaState": media}}
}

// ServeProclaim answers like Proclaim's Local Remote server (port 52195).
func (s *Slides) ServeProclaim(addr string, log *slog.Logger) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("slides (Proclaim) on %s: %w", addr, err)
	}
	go http.Serve(ln, s.proclaimHandler(log))
	return nil
}

// proclaimHandler serves Proclaim 4.22's Local Remote API (server
// 1.4.0.0): the routes and JSON shapes Proclaim's own remote apps use.
func (s *Slides) proclaimHandler(log *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	j := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(v)
	}
	fail := func(w http.ResponseWriter, code int, errCode string) {
		j(w, code, map[string]string{"errorCode": errCode})
	}
	// onAir checks the OnAirSessionId header (callers hold s.mu).
	onAir := func(w http.ResponseWriter, r *http.Request) bool {
		if !s.onAir || r.Header.Get("OnAirSessionId") != s.session {
			fail(w, 404, "PresentationNotOnAir")
			return false
		}
		return true
	}
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Write([]byte("Hello from Proclaim Local Remote version 1.4.0.0"))
	})
	mux.HandleFunc("POST /config", func(w http.ResponseWriter, r *http.Request) {
		j(w, 200, map[string]any{"version": "1.4.0.0", "isClientSupported": true})
	})
	mux.HandleFunc("GET /onair/session", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		on, sess := s.onAir, s.session
		s.mu.Unlock()
		if !on {
			w.WriteHeader(204)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Write([]byte(sess)) // a bare id, not a JSON string
	})
	mux.HandleFunc("POST /auth/control", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if !onAir(w, r) {
			return
		}
		var b struct{ UserName, RemoteDeviceName, Password string }
		json.NewDecoder(r.Body).Decode(&b)
		if b.Password != controlPassword {
			fail(w, 403, "AccessDenied")
			return
		}
		j(w, 200, map[string]string{"authenticationToken": "sim-control-token", "connectionId": "sim-conn-" + strconv.Itoa(s.sessions)})
	})
	mux.HandleFunc("GET /auth/disconnectcontrol", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		onAir(w, r)
	})
	mux.HandleFunc("GET /onair/statusChanged", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		deadline := time.NewTimer(60 * time.Second)
		defer deadline.Stop()
		for {
			s.mu.Lock()
			if !onAir(w, r) {
				s.mu.Unlock()
				return
			}
			// Long poll: hold while both values match the current ones.
			hold := q.Has("localRevision") && q.Has("step") && q.Get("localRevision") == "1" && q.Get("step") == strconv.FormatInt(s.step, 10)
			if !hold {
				st := s.status()
				s.mu.Unlock()
				j(w, 200, st)
				return
			}
			wake := s.wake
			s.mu.Unlock()
			select {
			case <-wake:
			case <-r.Context().Done():
				return
			case <-deadline.C:
				q.Del("step") // answer with the unchanged status
			}
		}
	})
	mux.HandleFunc("POST /onair/statusChanged", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if !onAir(w, r) {
			return
		}
		if r.Header.Get("ProclaimAuthToken") != "sim-control-token" {
			fail(w, 403, "AccessDenied")
			return
		}
		var b struct {
			Status struct {
				Revision        int64
				ItemID          string
				SlideIndex      int
				QuickScreenData *string
				QuickScreenKind string
				MediaState      *string
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			fail(w, 500, "Unknown")
			return
		}
		st := b.Status
		if st.Revision < s.step {
			return // an old step: ignored
		}
		idx := -1
		for i, it := range plan {
			if it.ID == st.ItemID {
				idx = i
			}
		}
		if idx < 0 || st.SlideIndex < 0 || st.SlideIndex >= len(plan[idx].Slides) {
			fail(w, 404, "ItemNotFound")
			return
		}
		s.goTo(idx, st.SlideIndex)
		if k := st.QuickScreenKind; k != "" {
			s.qs = k
		}
		if st.QuickScreenData != nil {
			s.qsData = *st.QuickScreenData
		}
		if st.MediaState != nil {
			s.media = *st.MediaState
		}
		log.Info("slides", "status", fmt.Sprintf("item %d slide %d %s", idx+1, st.SlideIndex+1, s.qs))
		s.render()
	})
	mux.HandleFunc("GET /presentations/onair", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if !onAir(w, r) {
			return
		}
		items := []map[string]any{}
		for _, it := range plan {
			sl := []map[string]any{}
			for i := range it.Slides {
				sl = append(sl, map[string]any{"localRevision": 1, "index": i})
			}
			var notes any
			if it.Notes != "" {
				notes = it.Notes
			}
			items = append(items, map[string]any{"id": it.ID, "title": it.Title, "notes": notes, "kind": it.Kind, "slides": sl})
		}
		var quick any // Proclaim leaves these out for other clients
		if r.UserAgent() == "ProclaimLocalRemote/1.4.0.0" {
			qs := []any{}
			for k, q := range customQuick {
				qs = append(qs, map[string]any{"id": q.ID, "serviceItemId": guid(50 + k), "kind": "Custom", "title": q.Title})
			}
			for _, k := range []string{"Blank", "Logo", "NoText", "Amen", "Hallelujah"} {
				qs = append(qs, map[string]any{"id": nil, "serviceItemId": nil, "kind": k, "title": nil})
			}
			quick = qs
		}
		j(w, 200, map[string]any{"sessionId": s.session, "groupId": nil, "groupName": nil, "id": presentationID, "localRevision": 1,
			"title": "Weekend Service", "logoServiceItemId": plan[0].ID, "aspectRatio": "16:9",
			"startTime":        s.start.UTC().UnixNano()/100 + 621355968000000000, // .NET ticks
			"warmupStartIndex": warmupStart, "serviceStartIndex": serviceStart, "postServiceStartIndex": postServiceStart,
			"serviceItems": items, "quickScreens": quick})
	})
	// Pictures. Proclaim serves these while on air without the session
	// header; width is required, localRevision too for slides.
	pic := func(w http.ResponseWriter, b []byte) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		w.Write(b)
	}
	width := func(r *http.Request) (int, bool) {
		n, err := strconv.Atoi(r.URL.Query().Get("width"))
		return min(max(n, 16), 1920), err == nil
	}
	mux.HandleFunc("GET /presentations/onair/items/{id}/slides/{idx}/image", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if !s.onAir {
			fail(w, 404, "PresentationNotOnAir")
			return
		}
		wd, ok := width(r)
		n, err := strconv.Atoi(r.PathValue("idx"))
		i := -1
		for k, it := range plan {
			if strings.EqualFold(it.ID, r.PathValue("id")) {
				i = k
			}
		}
		if !ok || !r.URL.Query().Has("localRevision") || err != nil || i < 0 || n < 0 || n >= len(plan[i].Slides) {
			fail(w, 404, "ItemNotFound")
			return
		}
		pic(w, s.picture(i, n, wd, r.URL.Query().Get("confidence") == "true"))
	})
	mux.HandleFunc("GET /presentations/onair/thumbnail", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if !s.onAir {
			fail(w, 404, "PresentationNotOnAir")
			return
		}
		wd, ok := width(r)
		if !ok {
			fail(w, 404, "ItemNotFound")
			return
		}
		pic(w, s.picture(0, 0, wd, false))
	})
	mux.HandleFunc("POST /appCommand/authenticate", func(w http.ResponseWriter, r *http.Request) {
		var b struct{ Password string }
		json.NewDecoder(r.Body).Decode(&b)
		if b.Password == "" {
			w.WriteHeader(400)
			return
		}
		j(w, 200, map[string]string{"proclaimAuthToken": "sim-token"})
	})
	mux.HandleFunc("GET /appCommand/perform", func(w http.ResponseWriter, r *http.Request) {
		cmd := r.URL.Query().Get("appCommandName")
		idx, _ := strconv.Atoi(r.URL.Query().Get("index"))
		if !s.do(cmd, idx) {
			http.Error(w, "unknown command", 404)
			return
		}
		log.Info("slides", "command", cmd)
		w.Write([]byte("success"))
	})
	return mux
}
