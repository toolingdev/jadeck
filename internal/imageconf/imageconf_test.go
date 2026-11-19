package imageconf

import (
	"strings"
	"testing"
)

const sample = `bright = "11"
saturation = "4"
contrast = "10"
sharpness = "3"
hue = "8"
ldc = "0"
flip = "0"
mirror = "0"
`

func TestRoundTripKeepsUnmodeledKeys(t *testing.T) {
	c := Parse(sample)
	typed := ToTyped(c)
	if typed["brightness"] != 11 || typed["flip"] != false {
		t.Fatalf("typed: %v", typed)
	}
	if _, ok := typed["wbMode"]; ok {
		t.Fatal("invented a key the camera didn't return")
	}
	n, err := Apply(c, map[string]any{"brightness": float64(7), "mirror": true})
	if err != nil {
		t.Fatal(err)
	}
	q := n.PostQuery()
	want := "post_image_conf&bright=7&saturation=4&contrast=10&sharpness=3&hue=8&ldc=0&flip=0&mirror=1"
	if q != want {
		t.Fatalf("got  %s\nwant %s", q, want)
	}
	if v, _ := c.Get("bright"); v != "11" {
		t.Fatal("Apply mutated the original")
	}
}

func TestValidation(t *testing.T) {
	c := Parse(sample)
	for _, bad := range []map[string]any{
		{"brightness": float64(15)},
		{"brightness": 1.5},
		{"flip": "yes"},
		{"shutter": "1/7"},
		{"nope": 1},
	} {
		if _, err := Apply(c, bad); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
	if _, err := Apply(c, map[string]any{"shutter": "1/250"}); err != nil {
		t.Fatal(err)
	}
	if q := Parse("s = \"1/250 a&b=c\"\n").PostQuery(); q != "post_image_conf&s=1%2F250%20a%26b%3Dc" {
		t.Fatalf("escape: %s", q)
	}
	if !strings.Contains(Parse("x = bare\n").PostQuery(), "x=bare") {
		t.Fatal("unquoted value")
	}
}
