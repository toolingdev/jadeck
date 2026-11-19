// Package imageconf maps the camera's get_image_conf / post_image_conf
// text to typed JSON. Writes are read-modify-write: only changed keys
// are replaced; every other key (modeled or not, e.g. ldc) goes back
// byte-identical.
//
// Live reconciliation (Nov 7, 2026, SOC v6.3.45): get_image_conf holds
// exactly bright, saturation, contrast, sharpness, hue, ldc, flip,
// mirror. The WB / exposure / focus / NR groups below are unverified
// reconstructions from PTZOptics docs; ToTyped omits whatever the
// camera does not return. Ranges are one-line edits in Fields.
package imageconf

import (
	"bufio"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var ShutterSteps = []string{"1/30", "1/60", "1/90", "1/100", "1/125", "1/180",
	"1/250", "1/350", "1/500", "1/725", "1/1000", "1/1500",
	"1/2000", "1/3000", "1/4000", "1/6000", "1/10000"}
var IrisSteps = []string{"F1.8", "F2.0", "F2.4", "F2.8", "F3.4", "F4.0", "F4.8",
	"F5.6", "F6.8", "F8.0", "F9.6", "F11.0", "Close"}

type Kind int

const (
	Int Kind = iota
	Enum
	Bool
)

type Field struct {
	Name, Key string
	Kind      Kind
	Min, Max  int
	Values    []string
}

var Fields = []Field{
	// image basics (confirmed live)
	{Name: "brightness", Key: "bright", Kind: Int, Min: 0, Max: 14},
	{Name: "saturation", Key: "saturation", Kind: Int, Min: 0, Max: 14},
	{Name: "contrast", Key: "contrast", Kind: Int, Min: 0, Max: 14},
	{Name: "sharpness", Key: "sharpness", Kind: Int, Min: 0, Max: 14},
	{Name: "hue", Key: "hue", Kind: Int, Min: 0, Max: 14},
	{Name: "flip", Key: "flip", Kind: Bool},
	{Name: "mirror", Key: "mirror", Kind: Bool},
	// white balance
	{Name: "wbMode", Key: "wbmode", Kind: Enum, Values: []string{"auto", "indoor", "outdoor", "onepush", "manual", "var"}},
	{Name: "redGain", Key: "rgain", Kind: Int, Min: 0, Max: 255},
	{Name: "blueGain", Key: "bgain", Kind: Int, Min: 0, Max: 255},
	{Name: "redTuning", Key: "rgtuning", Kind: Int, Min: -10, Max: 10},
	{Name: "blueTuning", Key: "bgtuning", Kind: Int, Min: -10, Max: 10},
	{Name: "awbSensitivity", Key: "awbsens", Kind: Enum, Values: []string{"low", "normal", "high"}},
	{Name: "colorTemp", Key: "colortemp", Kind: Int, Min: 2800, Max: 6500},
	// exposure
	{Name: "expMode", Key: "aemode", Kind: Enum, Values: []string{"auto", "manual", "shutter", "iris", "bright"}},
	{Name: "shutter", Key: "shutter", Kind: Enum, Values: ShutterSteps},
	{Name: "iris", Key: "iris", Kind: Enum, Values: IrisSteps},
	{Name: "gain", Key: "gain", Kind: Int, Min: 0, Max: 15},
	{Name: "brightLevel", Key: "expbright", Kind: Int, Min: 0, Max: 17},
	{Name: "expCompOn", Key: "expcompmode", Kind: Bool},
	{Name: "expComp", Key: "expcomp", Kind: Int, Min: -7, Max: 7},
	{Name: "backlight", Key: "backlight", Kind: Bool},
	{Name: "gainLimit", Key: "gainlimit", Kind: Int, Min: 0, Max: 15},
	{Name: "antiFlicker", Key: "flicker", Kind: Enum, Values: []string{"off", "50hz", "60hz"}},
	{Name: "drc", Key: "drc", Kind: Int, Min: 0, Max: 8},
	// focus extras
	{Name: "afSensitivity", Key: "afsens", Kind: Enum, Values: []string{"low", "normal", "high"}},
	{Name: "afZone", Key: "afzone", Kind: Enum, Values: []string{"top", "center", "bottom"}},
	// noise reduction
	{Name: "nr2d", Key: "nr2d", Kind: Enum, Values: []string{"off", "auto", "1", "2", "3", "4", "5"}},
	{Name: "nr3d", Key: "nr3d", Kind: Enum, Values: []string{"off", "1", "2", "3", "4", "5", "6", "7", "8"}},
}

var byName = func() map[string]Field {
	m := map[string]Field{}
	for _, f := range Fields {
		m[f.Name] = f
	}
	return m
}()

// Conf is an ordered raw key -> value map, values verbatim.
type Conf struct {
	Keys []string
	Vals map[string]string
}

func (c *Conf) Get(k string) (string, bool) { v, ok := c.Vals[k]; return v, ok }

func (c *Conf) Set(k, v string) {
	if _, ok := c.Vals[k]; !ok {
		c.Keys = append(c.Keys, k)
	}
	c.Vals[k] = v
}

func (c *Conf) Clone() *Conf {
	n := &Conf{Keys: slices.Clone(c.Keys), Vals: make(map[string]string, len(c.Vals))}
	for k, v := range c.Vals {
		n.Vals[k] = v
	}
	return n
}

var lineRe = regexp.MustCompile(`^\s*([A-Za-z0-9_]+)\s*=\s*(?:"([^"]*)"|(\S+))\s*$`)

func Parse(text string) *Conf {
	c := &Conf{Vals: map[string]string{}}
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		m := lineRe.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		v := m[2]
		if m[2] == "" && m[3] != "" {
			v = m[3]
		}
		c.Set(m[1], v)
	}
	return c
}

// PostQuery renders the conf as the post_image_conf query string.
func (c *Conf) PostQuery() string {
	var b strings.Builder
	b.WriteString("post_image_conf")
	for _, k := range c.Keys {
		b.WriteString("&" + k + "=" + strictEscape(c.Vals[k]))
	}
	return b.String()
}

// ToTyped returns the modeled subset as typed JSON values. Keys the
// camera didn't return are absent, never invented.
func ToTyped(c *Conf) map[string]any {
	out := map[string]any{}
	for _, f := range Fields {
		raw, ok := c.Get(f.Key)
		if !ok {
			continue
		}
		switch f.Kind {
		case Int:
			if n, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil {
				out[f.Name] = n
			} else {
				out[f.Name] = raw
			}
		case Bool:
			r := strings.TrimSpace(raw)
			out[f.Name] = !(r == "0" || r == "" || strings.EqualFold(r, "off"))
		default:
			out[f.Name] = raw
		}
	}
	return out
}

// Validate checks one typed value against its field.
func Validate(f Field, v any) error {
	switch f.Kind {
	case Int:
		n, ok := asInt(v)
		if !ok {
			return fmt.Errorf("%s: expected an integer", f.Name)
		}
		if n < f.Min || n > f.Max {
			return fmt.Errorf("%s: %d outside %d..%d", f.Name, n, f.Min, f.Max)
		}
	case Enum:
		s, ok := v.(string)
		if !ok || !slices.Contains(f.Values, s) {
			return fmt.Errorf("%s: %v not one of %v", f.Name, v, f.Values)
		}
	case Bool:
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("%s: expected true/false", f.Name)
		}
	}
	return nil
}

func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case float64: // JSON numbers
		if n == float64(int(n)) {
			return int(n), true
		}
	}
	return 0, false
}

func toRaw(f Field, v any) string {
	switch f.Kind {
	case Bool:
		if v.(bool) {
			return "1"
		}
		return "0"
	case Int:
		n, _ := asInt(v)
		return strconv.Itoa(n)
	}
	return v.(string)
}

// Apply merges typed updates into a copy of conf. Unknown names and
// invalid values fail the whole update; nothing partial.
func Apply(c *Conf, updates map[string]any) (*Conf, error) {
	n := c.Clone()
	for name, v := range updates {
		f, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("unknown image field: %s", name)
		}
		if err := Validate(f, v); err != nil {
			return nil, err
		}
		n.Set(f.Key, toRaw(f, v))
	}
	return n, nil
}

// strictEscape percent-encodes everything except RFC 3986 unreserved
// characters (same as Python's quote(v, safe="")).
func strictEscape(v string) string {
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// Spec is a field as the UI needs it.
type Spec struct {
	Name   string   `json:"name"`
	Key    string   `json:"key"`
	Group  string   `json:"group"`
	Label  string   `json:"label"`
	Kind   string   `json:"kind"`
	Min    *int     `json:"min,omitempty"`
	Max    *int     `json:"max,omitempty"`
	Values []string `json:"values,omitempty"`
}

var groupOf = map[string]string{
	"brightness": "image", "saturation": "image", "contrast": "image", "sharpness": "image", "hue": "image",
	"flip": "image", "mirror": "image",
	"wbMode": "wb", "redGain": "wb", "blueGain": "wb", "redTuning": "wb", "blueTuning": "wb",
	"awbSensitivity": "wb", "colorTemp": "wb",
	"expMode": "exposure", "shutter": "exposure", "iris": "exposure", "gain": "exposure",
	"brightLevel": "exposure", "expCompOn": "exposure", "expComp": "exposure", "backlight": "exposure",
	"gainLimit": "exposure", "antiFlicker": "exposure", "drc": "exposure",
	"afSensitivity": "focus", "afZone": "focus", "nr2d": "nr", "nr3d": "nr",
}

var labelOf = map[string]string{
	"brightness": "Brightness", "saturation": "Saturation", "contrast": "Contrast", "sharpness": "Sharpness",
	"hue": "Hue", "flip": "Flip", "mirror": "Mirror", "wbMode": "Mode", "redGain": "Red gain",
	"blueGain": "Blue gain", "redTuning": "Red tuning", "blueTuning": "Blue tuning",
	"awbSensitivity": "AWB sensitivity", "colorTemp": "Color temperature", "expMode": "Mode",
	"shutter": "Shutter", "iris": "Iris", "gain": "Gain", "brightLevel": "Bright level",
	"expCompOn": "Exposure comp", "expComp": "Comp level", "backlight": "Backlight",
	"gainLimit": "Gain limit", "antiFlicker": "Anti-flicker", "drc": "DRC",
	"afSensitivity": "AF sensitivity", "afZone": "AF zone", "nr2d": "2D noise reduction", "nr3d": "3D noise reduction",
}

// Specs lists every modeled field for the UI.
func Specs() []Spec {
	out := make([]Spec, 0, len(Fields))
	for _, f := range Fields {
		s := Spec{Name: f.Name, Key: f.Key, Group: groupOf[f.Name], Label: labelOf[f.Name], Values: f.Values}
		switch f.Kind {
		case Int:
			s.Kind = "int"
			lo, hi := f.Min, f.Max
			s.Min, s.Max = &lo, &hi
		case Enum:
			s.Kind = "enum"
		case Bool:
			s.Kind = "bool"
		}
		out = append(out, s)
	}
	return out
}
