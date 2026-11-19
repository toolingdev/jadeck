package macros

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ptz/internal/devices"
	"ptz/internal/devices/sim"
)

func env() Env {
	favs := map[string]int{"main": 1, "pc": 0}
	return Env{
		Favorite: func(n string) (string, int, bool) {
			ch, ok := favs[strings.ToLower(n)]
			return strings.ToUpper(n), ch, ok
		},
		FavoriteLabels: func() []string { return []string{"MAIN", "PC"} },
		MaxChannel:     48,
		PresetExists:   func(n string) bool { return n == "Pulpit" },
	}
}

func doc(js string) any {
	var v any
	if err := json.Unmarshal([]byte(js), &v); err != nil {
		panic(err)
	}
	return v
}

func TestValidate(t *testing.T) {
	m, err := Validate(doc(`{"name":"  Swap ","steps":[
		{"type":"wing_mute","channelName":"main","muted":false},
		{"type":"delay","ms":250},
		{"type":"switcher","action":"fade"},
		{"type":"slides","action":"next"},
		{"type":"camera_preset","preset":"Pulpit"}]}`), env())
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "Swap" || m.Steps[0].Type != "mixer_mute" || m.Steps[0].Channel != 1 ||
		m.Steps[0].ChannelName != "MAIN" || m.Steps[2].DurationMs != 1000 {
		t.Fatalf("%+v", m)
	}
	if Describe(m.Steps[0]) != "Unmute MAIN · Ch 1" || Describe(m.Steps[1]) != "Wait 250 ms" {
		t.Fatal(Describe(m.Steps[0]), Describe(m.Steps[1]))
	}
	bad := map[string]string{
		"no name":        `{"steps":[{"type":"delay","ms":1}]}`,
		"extra field":    `{"name":"x","steps":[{"type":"delay","ms":1}],"color":"red"}`,
		"no steps":       `{"name":"x","steps":[]}`,
		"unknown type":   `{"name":"x","steps":[{"type":"launch"}]}`,
		"unassigned fav": `{"name":"x","steps":[{"type":"mixer_mute","channelName":"PC","muted":true}]}`,
		"unknown fav":    `{"name":"x","steps":[{"type":"mixer_mute","channelName":"Organ","muted":true}]}`,
		"disagree":       `{"name":"x","steps":[{"type":"mixer_mute","channel":2,"channelName":"MAIN","muted":true}]}`,
		"channel range":  `{"name":"x","steps":[{"type":"mixer_mute","channel":49,"muted":true}]}`,
		"muted missing":  `{"name":"x","steps":[{"type":"mixer_mute","channel":4}]}`,
		"float delay":    `{"name":"x","steps":[{"type":"delay","ms":1.5}]}`,
		"long delay":     `{"name":"x","steps":[{"type":"delay","ms":60001}]}`,
		"bad switcher":   `{"name":"x","steps":[{"type":"switcher","action":"explode"}]}`,
		"preview no in":  `{"name":"x","steps":[{"type":"switcher","action":"preview"}]}`,
		"unknown preset": `{"name":"x","steps":[{"type":"camera_preset","preset":"Balcony"}]}`,
		"step field":     `{"name":"x","steps":[{"type":"delay","ms":1,"x":2}]}`,
	}
	for name, js := range bad {
		if _, err := Validate(doc(js), env()); !errors.Is(err, ErrValidation) {
			t.Errorf("%s: accepted (%v)", name, err)
		}
	}
}

func TestExtractImport(t *testing.T) {
	for js, n := range map[string]int{
		`{"name":"a","steps":[]}`:                       1,
		`[{"name":"a"},{"name":"b"}]`:                   2,
		`{"macros":[{"name":"a"}],"validateOnly":true}`: 1,
	} {
		items, _, err := ExtractImport(doc(js))
		if err != nil || len(items) != n {
			t.Errorf("%s: %d %v", js, len(items), err)
		}
	}
	if _, _, err := ExtractImport(doc(`42`)); err == nil {
		t.Fatal("number accepted")
	}
}

type rig struct {
	store *Store
	eng   *Engine
	mixer *sim.Mixer
	sw    *sim.Switcher
}

func newRig(t *testing.T) *rig {
	st, err := OpenStore(filepath.Join(t.TempDir(), "macros.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	mx := sim.NewMixer(48)
	sw := sim.NewSwitcher()
	set := &devices.Set{Mixer: mx, Switcher: sw}
	eng := NewEngine(st, Actions{
		MuteChannel:     set.MuteChannel,
		SwitcherCut:     sw.Cut,
		SwitcherPreview: sw.Preview,
		SwitcherFade:    sw.Fade,
	})
	return &rig{st, eng, mx, sw}
}

func (r *rig) add(t *testing.T, js string) int64 {
	m, err := Validate(doc(js), env())
	if err != nil {
		t.Fatal(err)
	}
	c, err := r.store.Create(m)
	if err != nil {
		t.Fatal(err)
	}
	return c.ID
}

func TestRunSuccessAndRecord(t *testing.T) {
	r := newRig(t)
	id := r.add(t, `{"name":"Go live","steps":[
		{"type":"mixer_mute","channel":13,"muted":false},
		{"type":"mixer_mute","channelName":"MAIN","muted":true},
		{"type":"switcher","action":"preview","input":3},
		{"type":"switcher","action":"cut"}]}`)
	rep, err := r.eng.Run(context.Background(), id)
	if err != nil || !rep.OK || len(rep.Steps) != 4 {
		t.Fatalf("%+v %v", rep, err)
	}
	if muted, _ := r.mixer.GetMute(context.Background(), 1); !muted {
		t.Fatal("MAIN not muted")
	}
	st, _ := r.sw.State(context.Background())
	if st.Active != 3 {
		t.Fatalf("program should be input 3, got %d", st.Active)
	}
	m, _ := r.store.Get(id)
	if m.LastRun["ok"] != true {
		t.Fatalf("last run not recorded: %v", m.LastRun)
	}
	if _, err := r.store.Create(&Macro{Name: "go LIVE", Steps: m.Steps}); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate name accepted")
	}
}

func TestReadBackMismatchStopsRun(t *testing.T) {
	r := newRig(t)
	r.mixer.StuckChannels[13] = true // console ignores this channel
	id := r.add(t, `{"name":"Swap","steps":[
		{"type":"mixer_mute","channel":13,"muted":true},
		{"type":"mixer_mute","channel":1,"muted":true}]}`)
	rep, _ := r.eng.Run(context.Background(), id)
	if rep.OK || rep.FailedStep == nil || *rep.FailedStep != 1 || len(rep.Steps) != 1 {
		t.Fatalf("%+v", rep)
	}
	if !strings.Contains(*rep.Error, "did not take the change") {
		t.Fatal(*rep.Error)
	}
	if muted, _ := r.mixer.GetMute(context.Background(), 1); muted {
		t.Fatal("step 2 ran after step 1 failed")
	}
}

func TestMissingDeviceFailsStep(t *testing.T) {
	r := newRig(t)
	id := r.add(t, `{"name":"Slides","steps":[{"type":"slides","action":"next"}]}`)
	rep, _ := r.eng.Run(context.Background(), id)
	if rep.OK || !strings.Contains(*rep.Error, "not set up") {
		t.Fatalf("%+v", rep)
	}
}

func TestOneRunAtATimeAndTimeout(t *testing.T) {
	r := newRig(t)
	r.eng.StepTimeout = 100 * time.Millisecond
	block := make(chan struct{})
	r.eng.Do.Slides = func(ctx context.Context, a string) error {
		<-block
		return nil
	}
	defer close(block)
	id := r.add(t, `{"name":"Hang","steps":[{"type":"slides","action":"next"}]}`)
	got := make(chan *Report, 1)
	go func() { rep, _ := r.eng.Run(context.Background(), id); got <- rep }()
	time.Sleep(20 * time.Millisecond)
	if _, err := r.eng.Run(context.Background(), id); !errors.Is(err, ErrConflict) {
		t.Fatalf("second run: %v", err)
	}
	rep := <-got
	if rep.OK || !strings.Contains(*rep.Error, "did not answer") {
		t.Fatalf("%+v", rep)
	}
	if r.eng.Status()["running"] != false {
		t.Fatal("engine wedged")
	}
}

func TestPythonEraRows(t *testing.T) {
	r := newRig(t)
	_, err := r.store.db.Exec(`INSERT INTO macros (name, description, steps, sort, created_at, updated_at)
		VALUES ('PC ⇄ Mic: PC live', 'seed', '[{"type": "wing_mute", "channel": 13, "muted": false}, {"type": "wing_mute", "channelName": "MAIN", "muted": true, "channel": 1}]', 0, 1, 1)`)
	if err != nil {
		t.Fatal(err)
	}
	l, err := r.store.List()
	if err != nil || l[0].Steps[0].Type != "mixer_mute" || l[0].Steps[1].ChannelName != "MAIN" {
		t.Fatalf("%+v %v", l, err)
	}
	rep, _ := r.eng.Run(context.Background(), l[0].ID)
	if !rep.OK {
		t.Fatalf("%+v", rep)
	}
}
