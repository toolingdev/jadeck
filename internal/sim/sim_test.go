package sim

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestSwitcherCutFadeAndXML(t *testing.T) {
	f := func() *image.RGBA { return image.NewRGBA(image.Rect(0, 0, W, H)) }
	s := NewSwitcher([]Input{{"Wide", "Capture", f}, {"Lectern", "Capture", f}, {"Slides", "PowerPoint", f}})
	if s.Tally(1) != 1 || s.Tally(2) != 2 {
		t.Fatalf("start: active 1, preview 2")
	}
	q, _ := url.ParseQuery("Function=PreviewInput&Input=Slides")
	if err := s.function(q); err != nil || s.Tally(3) != 2 {
		t.Fatalf("preview by title: %v", err)
	}
	q, _ = url.ParseQuery("Function=Cut")
	s.function(q)
	if s.Tally(3) != 1 || s.Tally(1) != 2 {
		t.Fatalf("cut swaps program and preview")
	}
	x := string(s.stateXML())
	if !strings.Contains(x, `<active>3</active>`) || !strings.Contains(x, `title="Lectern"`) {
		t.Fatalf("xml: %s", x)
	}
	q, _ = url.ParseQuery("Function=Nope")
	if s.function(q) == nil {
		t.Fatal("unknown function should fail")
	}
}

func TestMixerMuteOverOSC(t *testing.T) {
	m := NewMixer()
	addr, args, _ := oscDecode(m.handle(oscEncode("/?")))
	if addr != "/?" || len(args) == 0 {
		t.Fatal("probe should answer")
	}
	_, args, _ = oscDecode(m.handle(oscEncode("/ch/2/mute", int32(1))))
	if args[0].(int32) != 1 {
		t.Fatal("mute set")
	}
	_, args, _ = oscDecode(m.handle(oscEncode("/ch/2/mute")))
	if args[0].(int32) != 1 {
		t.Fatal("mute read back")
	}
	_, args, _ = oscDecode(m.handle(oscEncode("/ch/2/name")))
	if args[0].(string) != "Lectern" {
		t.Fatalf("name: %v", args)
	}
}

func TestSlidesNavigate(t *testing.T) {
	s := NewSlides()
	s.do("NextSlide", 0) // Welcome has one slide: moves to the next item
	if s.item != 1 || s.slide != 0 {
		t.Fatalf("next across items: %d/%d", s.item, s.slide)
	}
	// Proclaim counts GoToServiceItem from the service start (item 2 here).
	s.do("GoToServiceItem", 4)
	if s.item != 4 {
		t.Fatalf("go to service item 4: %d", s.item)
	}
	s.do("ShowBlankQuickScreen", 0)
	if !s.blank() {
		t.Fatal("blank")
	}
	s.do("NextSlide", 0)
	if s.blank() {
		t.Fatal("next slide clears blank")
	}
}

// The fake Proclaim speaks the Local Remote API: session-gated reads,
// the remote-control login and POST /onair/statusChanged.
func TestProclaimRemoteControl(t *testing.T) {
	s := NewSlides()
	srv := httptest.NewServer(s.proclaimHandler(slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer srv.Close()
	call := func(method, path, sess, token string, body any, out any) int {
		t.Helper()
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, srv.URL+path, rd)
		req.Header.Set("User-Agent", "ProclaimLocalRemote/1.4.0.0")
		if sess != "" {
			req.Header.Set("OnAirSessionId", sess)
		}
		if token != "" {
			req.Header.Set("ProclaimAuthToken", token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if out != nil {
			if p, ok := out.(*string); ok {
				*p = string(b)
			} else {
				json.Unmarshal(b, out)
			}
		}
		return resp.StatusCode
	}
	var sess string
	if call("GET", "/onair/session", "", "", nil, &sess) != 200 || len(sess) != 32 {
		t.Fatalf("session %q", sess)
	}
	if code := call("GET", "/presentations/onair", "", "", nil, nil); code != 404 {
		t.Fatalf("no session header: %d", code)
	}
	var pres struct {
		ServiceStartIndex int
		ServiceItems      []struct{ ID string }
		QuickScreens      []any
	}
	if call("GET", "/presentations/onair", sess, "", nil, &pres) != 200 || pres.ServiceStartIndex != 1 || len(pres.ServiceItems) != 6 || pres.QuickScreens == nil {
		t.Fatalf("presentation %+v", pres)
	}
	if code := call("POST", "/auth/control", sess, "", map[string]string{"userName": "t", "remoteDeviceName": "t", "password": "nope"}, nil); code != 403 {
		t.Fatalf("wrong control password: %d", code)
	}
	var auth struct{ AuthenticationToken, ConnectionID string }
	if call("POST", "/auth/control", sess, "", map[string]string{"userName": "t", "remoteDeviceName": "t", "password": "Proclaim"}, &auth) != 200 || auth.AuthenticationToken == "" {
		t.Fatalf("control login %+v", auth)
	}
	var st struct {
		PresentationLocalRevision int64
		Status                    struct {
			Revision int64
			ItemID   string
		}
	}
	call("GET", "/onair/statusChanged", sess, "", nil, &st)
	next := map[string]any{"presentationId": "x", "presentationLocalRevision": st.PresentationLocalRevision,
		"status": map[string]any{"revision": st.Status.Revision, "itemId": pres.ServiceItems[0].ID, "slideIndex": 0, "quickScreenKind": "None"}}
	if code := call("POST", "/onair/statusChanged", sess, "bad", next, nil); code != 403 {
		t.Fatalf("bad token: %d", code)
	}
	code := call("POST", "/onair/statusChanged", sess, auth.AuthenticationToken, next, nil)
	s.mu.Lock()
	at := s.item
	s.mu.Unlock()
	if code != 200 || at != 0 {
		t.Fatalf("go to the warm-up item: %d, at %d", code, at)
	}
	// A long poll returns as soon as the status moves.
	call("GET", "/onair/statusChanged", sess, "", nil, &st)
	go s.do("NextSlide", 0)
	path := "/onair/statusChanged?localRevision=1&step=" + strconv.FormatInt(st.Status.Revision, 10)
	var moved struct{ Status struct{ Revision int64 } }
	if call("GET", path, sess, "", nil, &moved) != 200 || moved.Status.Revision <= st.Status.Revision {
		t.Fatalf("long poll: %+v", moved)
	}
	if code := call("GET", "/appCommand/perform?appCommandName=Dance", "", "", nil, nil); code != 404 {
		t.Fatalf("unknown command: %d", code)
	}
}

// The fake Proclaim draws slide pictures, the stage view and the
// thumbnail, and knows the widget commands (song parts, quick screens).
func TestProclaimPictures(t *testing.T) {
	s := NewSlides()
	srv := httptest.NewServer(s.proclaimHandler(slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer srv.Close()
	get := func(path string) (int, image.Image) {
		t.Helper()
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return resp.StatusCode, nil
		}
		img, err := png.Decode(resp.Body)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		return 200, img
	}
	song := plan[1].ID
	code, img := get("/presentations/onair/items/" + song + "/slides/2/image?localRevision=1&width=320")
	if code != 200 || img.Bounds().Dx() != 320 || img.Bounds().Dy() != 180 {
		t.Fatalf("slide picture: %d %v", code, img)
	}
	if code, img = get("/presentations/onair/items/" + song + "/slides/2/image?localRevision=1&width=640&confidence=true"); code != 200 || img.Bounds().Dx() != 640 {
		t.Fatalf("stage picture: %d", code)
	}
	if r, g, b, _ := img.At(5, 5).RGBA(); r|g|b != 0 {
		t.Fatal("the stage view is drawn on black")
	}
	if code, _ = get("/presentations/onair/thumbnail?width=200"); code != 200 {
		t.Fatalf("thumbnail: %d", code)
	}
	for _, bad := range []string{"/presentations/onair/items/" + song + "/slides/9/image?localRevision=1&width=320",
		"/presentations/onair/items/" + song + "/slides/0/image?width=320", "/presentations/onair/thumbnail"} {
		if code, _ := get(bad); code != 404 {
			t.Errorf("%s: %d", bad, code)
		}
	}

	s.do("GoToServiceItem", 1) // Opening Song
	s.do("ShowSongLyricsChorusByIndex", 1)
	if s.slide != 2 {
		t.Fatalf("chorus 1: slide %d", s.slide)
	}
	s.do("ShowSongLyricsBridgeByIndex", 1) // no bridge: stays
	if s.slide != 2 {
		t.Fatalf("no bridge: slide %d", s.slide)
	}
	s.do("ShowAmenQuickScreen", 0)
	if s.qs != "Amen" {
		t.Fatalf("amen: %s", s.qs)
	}
	s.do("ShowCustomQuickScreen", 2)
	if s.qs != "Custom" || s.qsData != customQuick[1].ID {
		t.Fatalf("custom: %s %s", s.qs, s.qsData)
	}
}

func TestViewCropZoomsIn(t *testing.T) {
	_, _, wide, _ := View{Zoom: 0}.crop(W, H)
	_, _, tight, _ := View{Zoom: 16384}.crop(W, H)
	if tight >= wide/10 {
		t.Fatalf("20x zoom should narrow the view: %v vs %v", tight, wide)
	}
	cx, _, _, _ := View{Pan: 1440}.crop(W, H)
	if cx <= PanoW/2 {
		t.Fatal("pan right moves the view right")
	}
}
