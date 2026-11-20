// Package sim is the gear simulator behind `ptzsim`: PTZOptics cameras
// with live video of a drawn venue (PTZ moves and zooms the picture),
// a vMix-style switcher, a Proclaim-style slide server and a
// WING-style mixer. They speak the real protocols, so jadeck and its
// add-ons connect to them exactly as to real gear.
package sim

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"math/rand"
	"sync"
	"time"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// The venue is a wide panorama; a camera sees part of it.
const (
	PanoW, PanoH = 3200, 1400
	pxPerDeg     = 22.86 // 140° across, ~61° high
	wideFOV      = 62.0  // degrees across at full wide
	maxZoom      = 20.0  // optical zoom at 16384
	viscaPerDeg  = 14.4  // VISCA pan/tilt units per degree
)

// Where things stand on the panorama (pixels).
var (
	screenRect = image.Rect(1180, 170, 2020, 642) // projection screen, 16:9
	lecternX   = 1760
	stageY     = 930
)

// Scene draws the venue once and keeps the projection screen showing
// the current slide.
type Scene struct {
	mu     sync.RWMutex
	base   *image.RGBA // the room, screen blank
	comp   *image.RGBA // base + current slide
	start  time.Time
	fonts  fonts
	textMu sync.Mutex // font faces cache glyphs: one drawer at a time
}

type fonts struct{ small, label, big, title font.Face }

func loadFonts() fonts {
	reg, _ := opentype.Parse(goregular.TTF)
	bold, _ := opentype.Parse(gobold.TTF)
	face := func(f *opentype.Font, size float64) font.Face {
		fc, _ := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
		return fc
	}
	return fonts{small: face(reg, 20), label: face(bold, 22), big: face(bold, 56), title: face(bold, 40)}
}

func NewScene() *Scene {
	s := &Scene{start: time.Now(), fonts: loadFonts()}
	s.base = drawVenue()
	s.comp = image.NewRGBA(s.base.Bounds())
	draw.Draw(s.comp, s.comp.Bounds(), s.base, image.Point{}, draw.Src)
	return s
}

// SetSlide puts a rendered slide on the projection screen.
func (s *Scene) SetSlide(slide image.Image) {
	next := image.NewRGBA(s.base.Bounds())
	draw.Draw(next, next.Bounds(), s.base, image.Point{}, draw.Src)
	if slide != nil {
		xdraw.ApproxBiLinear.Scale(next, screenRect, slide, slide.Bounds(), draw.Src, nil)
	}
	s.mu.Lock()
	s.comp = next
	s.mu.Unlock()
}

// View is what a camera sees: pan/tilt in VISCA units, zoom 0..16384.
type View struct{ Pan, Tilt, Zoom int }

// crop is the panorama rectangle a view covers (may run off the edges).
func (v View) crop(w, h int) (cx, cy, cw, ch float64) {
	z := 1 + float64(v.Zoom)/16384*(maxZoom-1)
	cw = wideFOV / z * pxPerDeg
	ch = cw * float64(h) / float64(w)
	cx = PanoW/2 + float64(v.Pan)/viscaPerDeg*pxPerDeg
	cy = PanoH/2 - float64(v.Tilt)/viscaPerDeg*pxPerDeg
	return
}

// Render draws a camera's picture into dst.
func (s *Scene) Render(dst *image.RGBA, v View, name string, tally color.Color) {
	w, h := dst.Bounds().Dx(), dst.Bounds().Dy()
	cx, cy, cw, ch := v.crop(w, h)
	x0, y0 := cx-cw/2, cy-ch/2
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.RGBA{6, 7, 10, 255}), image.Point{}, draw.Src)

	// the part of the crop that's on the panorama
	src := image.Rect(int(math.Max(0, x0)), int(math.Max(0, y0)), int(math.Min(PanoW, x0+cw)), int(math.Min(PanoH, y0+ch)))
	if !src.Empty() {
		sx, sy := float64(w)/cw, float64(h)/ch
		d := image.Rect(int((float64(src.Min.X)-x0)*sx), int((float64(src.Min.Y)-y0)*sy),
			int((float64(src.Max.X)-x0)*sx), int((float64(src.Max.Y)-y0)*sy))
		s.mu.RLock()
		xdraw.ApproxBiLinear.Scale(dst, d, s.comp, src, draw.Src, nil)
		s.mu.RUnlock()
		// the speaker at the lectern sways a little: live, not a still
		t := time.Since(s.start).Seconds()
		px := float64(lecternX) + 14*math.Sin(t*0.9)
		// head and shoulders above the lectern top; the lectern hides the rest
		s.person(dst, (px-x0)*sx, (float64(stageY)-300-y0)*sy, sx, (float64(stageY)-182-y0)*sy, color.RGBA{58, 72, 110, 255})
	}

	// on-screen label and clock, like a camera's OSD
	s.textMu.Lock()
	nw := (&font.Drawer{Face: s.fonts.label}).MeasureString(name).Ceil()
	label(dst, s.fonts.label, (w-nw)/2, 34, name, color.White) // top centre, clear of jadeck's badges
	label(dst, s.fonts.small, w-130, h-18, time.Now().Format("15:04:05"), color.RGBA{220, 220, 220, 255})
	s.textMu.Unlock()
	if tally != nil {
		border(dst, 8, tally)
	}
}

// person draws a figure whose head top is at (x, y), scaled, cut off at
// cut (what stands in front of them).
func (s *Scene) person(dst *image.RGBA, x, y, sc, cut float64, shirt color.RGBA) {
	head := 24 * sc
	fillCircle(dst, x, y+head, head, color.RGBA{214, 176, 140, 255})
	fillRect(dst, x-44*sc, y+head*2+4*sc, x+44*sc, math.Min(cut, y+head*2+150*sc), shirt)
}

// ---------------------------------------------------------------- venue

func drawVenue() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, PanoW, PanoH))
	r := rand.New(rand.NewSource(7))
	// back wall: dark gradient, with panels
	for y := 0; y < PanoH; y++ {
		k := float64(y) / PanoH
		c := color.RGBA{uint8(18 + 22*k), uint8(20 + 18*k), uint8(34 + 10*k), 255}
		draw.Draw(img, image.Rect(0, y, PanoW, y+1), image.NewUniform(c), image.Point{}, draw.Src)
	}
	for x := 60; x < PanoW; x += 260 {
		fillRect(img, float64(x), 80, float64(x+200), 860, color.RGBA{30, 33, 50, 255})
	}
	// stage lights along the top
	for x := 200; x < PanoW; x += 300 {
		glow(img, float64(x), 60, 140, color.RGBA{255, 214, 150, 0})
		fillCircle(img, float64(x), 60, 16, color.RGBA{255, 240, 210, 255})
	}
	// projection screen frame
	fillRect(img, float64(screenRect.Min.X-14), float64(screenRect.Min.Y-14), float64(screenRect.Max.X+14), float64(screenRect.Max.Y+14), color.RGBA{12, 12, 14, 255})
	fillRect(img, float64(screenRect.Min.X), float64(screenRect.Min.Y), float64(screenRect.Max.X), float64(screenRect.Max.Y), color.RGBA{40, 44, 60, 255})
	// stage platform
	fillRect(img, 0, float64(stageY), PanoW, float64(stageY+150), color.RGBA{92, 62, 40, 255})
	fillRect(img, 0, float64(stageY), PanoW, float64(stageY+8), color.RGBA{140, 100, 70, 255})
	// band, stage left: drums, keys, guitar
	fillCircle(img, 520, float64(stageY)-70, 70, color.RGBA{150, 30, 36, 255})
	fillCircle(img, 400, float64(stageY)-120, 46, color.RGBA{200, 190, 120, 255})
	fillCircle(img, 640, float64(stageY)-130, 42, color.RGBA{200, 190, 120, 255})
	fillRect(img, 820, float64(stageY)-150, 1060, float64(stageY)-120, color.RGBA{20, 20, 22, 255})
	for k := 0; k < 12; k++ {
		fillRect(img, 826+float64(k)*19, float64(stageY)-146, 840+float64(k)*19, float64(stageY)-126, color.RGBA{235, 235, 235, 255})
	}
	standing(img, 520, float64(stageY)-330, color.RGBA{70, 70, 76, 255})
	standing(img, 940, float64(stageY)-350, color.RGBA{90, 40, 60, 255})
	standing(img, 300, float64(stageY)-345, color.RGBA{40, 80, 90, 255})
	// lectern, centre right
	fillRect(img, float64(lecternX-70), float64(stageY)-170, float64(lecternX+70), float64(stageY), color.RGBA{120, 84, 52, 255})
	fillRect(img, float64(lecternX-84), float64(stageY)-182, float64(lecternX+84), float64(stageY)-166, color.RGBA{150, 108, 70, 255})
	// singers, stage right
	for i, c := range []color.RGBA{{110, 40, 40, 255}, {40, 60, 110, 255}, {60, 90, 50, 255}} {
		x := 2300 + float64(i)*170
		standing(img, x, float64(stageY)-340, c)
		fillRect(img, x-3, float64(stageY)-230, x+3, float64(stageY), color.RGBA{30, 30, 30, 255}) // mic stand
	}
	// audience: rows of heads, darker further back is nearer the camera
	for row := 0; row < 5; row++ {
		y := 1130 + float64(row)*60
		for x := 40.0; x < PanoW; x += 70 + float64(r.Intn(20)) {
			if r.Intn(9) == 0 {
				continue
			}
			sh := uint8(40 + r.Intn(50))
			fillCircle(img, x, y, 26+float64(row)*2, color.RGBA{sh, sh - 10, sh - 18, 255})
			fillRect(img, x-34, y+20, x+34, y+90, color.RGBA{sh / 2, sh / 2, sh/2 + 8, 255})
		}
	}
	return img
}

func standing(img *image.RGBA, x, y float64, shirt color.RGBA) {
	fillCircle(img, x, y+24, 24, color.RGBA{204, 166, 130, 255})
	fillRect(img, x-42, y+52, x+42, y+200, shirt)
	fillRect(img, x-32, y+200, x+32, y+330, color.RGBA{30, 30, 36, 255})
}

// ---------------------------------------------------------------- shapes

func fillRect(img *image.RGBA, x0, y0, x1, y1 float64, c color.RGBA) {
	draw.Draw(img, image.Rect(int(x0), int(y0), int(x1), int(y1)).Intersect(img.Bounds()), image.NewUniform(c), image.Point{}, draw.Src)
}

func fillCircle(img *image.RGBA, cx, cy, r float64, c color.RGBA) {
	b := image.Rect(int(cx-r), int(cy-r), int(cx+r)+1, int(cy+r)+1).Intersect(img.Bounds())
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			dx, dy := float64(x)-cx, float64(y)-cy
			if dx*dx+dy*dy <= r*r {
				img.SetRGBA(x, y, c)
			}
		}
	}
}

// glow brightens a soft disc (additive).
func glow(img *image.RGBA, cx, cy, r float64, c color.RGBA) {
	b := image.Rect(int(cx-r), int(cy-r), int(cx+r)+1, int(cy+r)+1).Intersect(img.Bounds())
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			d := math.Hypot(float64(x)-cx, float64(y)-cy) / r
			if d >= 1 {
				continue
			}
			k := (1 - d) * (1 - d) * 0.45
			o := img.RGBAAt(x, y)
			add := func(a, b uint8) uint8 { return uint8(math.Min(255, float64(a)+float64(b)*k)) }
			img.SetRGBA(x, y, color.RGBA{add(o.R, c.R), add(o.G, c.G), add(o.B, c.B), 255})
		}
	}
}

func border(img *image.RGBA, t int, c color.Color) {
	b := img.Bounds()
	u := image.NewUniform(c)
	for _, r := range []image.Rectangle{{b.Min, image.Pt(b.Max.X, b.Min.Y+t)}, {image.Pt(b.Min.X, b.Max.Y-t), b.Max},
		{b.Min, image.Pt(b.Min.X+t, b.Max.Y)}, {image.Pt(b.Max.X-t, b.Min.Y), b.Max}} {
		draw.Draw(img, r, u, image.Point{}, draw.Src)
	}
}

// label draws text with a dark backing so it reads on any picture.
func label(img *image.RGBA, f font.Face, x, y int, s string, c color.Color) {
	d := &font.Drawer{Dst: img, Src: image.NewUniform(color.RGBA{0, 0, 0, 160}), Face: f}
	w := d.MeasureString(s).Ceil()
	m := f.Metrics()
	bg := image.Rect(x-8, y-m.Ascent.Ceil()-6, x+w+8, y+m.Descent.Ceil()+6)
	draw.Draw(img, bg, image.NewUniform(color.RGBA{0, 0, 0, 150}), image.Point{}, draw.Over)
	d.Src = image.NewUniform(c)
	d.Dot = fixed.P(x, y)
	d.DrawString(s)
}
