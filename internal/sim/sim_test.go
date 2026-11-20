package sim

import (
	"image"
	"net/url"
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
	s.do("GoToServiceItem", 4)
	if s.item != 3 {
		t.Fatalf("go to item 4: %d", s.item)
	}
	s.do("ShowBlankQuickScreen", 0)
	if !s.blank {
		t.Fatal("blank")
	}
	s.do("NextSlide", 0)
	if s.blank {
		t.Fatal("next slide clears blank")
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
