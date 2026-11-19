package osc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"ptz/internal/devices"
)

// Preset is a console's OSC dialect.
type Preset struct {
	Port        int    `json:"port"`
	MuteAddress string `json:"muteAddress"` // {n} = channel, {nn} = 2-digit
	MutedValue  int    `json:"mutedValue"`  // value meaning "muted"
	Probe       string `json:"probe"`
	MaxChannel  int    `json:"maxChannel"`
}

var Presets = map[string]Preset{
	// WING Remote Protocols (P.-G. Maillot). Verified shape in the
	// Python build's tests; live read-back confirms every write.
	"wing": {Port: 2223, MuteAddress: "/ch/{n}/mute", MutedValue: 1, Probe: "/?", MaxChannel: 48},
	// X32/M32 OSC (unofficial X32 OSC protocol doc). /mix/on is "on",
	// so 0 = muted. Untested against a real desk — experimental.
	"x32": {Port: 10023, MuteAddress: "/ch/{nn}/mix/on", MutedValue: 0, Probe: "/info", MaxChannel: 32},
}

type Mixer struct {
	addr    string
	p       Preset
	timeout time.Duration
}

func init() {
	devices.RegisterMixer("osc", func(raw json.RawMessage) (devices.Mixer, error) {
		var c struct {
			Name string `json:"preset"`
			Host string `json:"host"`
			Preset
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, err
		}
		p := Preset{MaxChannel: 64}
		if c.Name != "" {
			pp, ok := Presets[c.Name]
			if !ok {
				return nil, fmt.Errorf("unknown OSC preset %q", c.Name)
			}
			p = pp
		}
		// Explicit fields override the preset.
		if c.Port != 0 {
			p.Port = c.Port
		}
		if c.MuteAddress != "" {
			p.MuteAddress = c.MuteAddress
		}
		if c.Probe != "" {
			p.Probe = c.Probe
		}
		if c.MaxChannel != 0 {
			p.MaxChannel = c.MaxChannel
		}
		if strings.Contains(string(raw), `"mutedValue"`) {
			p.MutedValue = c.MutedValue
		}
		if c.Host == "" {
			return nil, fmt.Errorf("enter the console's IP address")
		}
		if p.MuteAddress == "" || p.Port == 0 {
			return nil, fmt.Errorf("osc mixer needs a preset or port + muteAddress")
		}
		return New(net.JoinHostPort(c.Host, strconv.Itoa(p.Port)), p), nil
	})
}

func init() {
	devices.Describe(devices.Meta{Kind: "mixer", Driver: "osc", Label: "OSC mixer (Behringer WING, X32/M32)",
		Description: "Mutes over OSC, every change read back from the console. Pick your console; the advanced fields are only for other OSC desks.",
		Fields: []devices.Field{
			{Key: "preset", Label: "Console", Type: "select", Default: "wing", Required: true, Options: []devices.Option{
				{Value: "wing", Label: "Behringer WING"}, {Value: "x32", Label: "Behringer X32 / Midas M32 (experimental)"}, {Value: "", Label: "Other OSC desk (fill in advanced)"}}},
			{Key: "host", Label: "Console IP address", Type: "text", Placeholder: "10.1.10.50", Required: true,
				Help: "On the WING: Setup → Network shows the address."},
			{Key: "port", Label: "OSC port", Type: "number", Advanced: true, Help: "Blank = the console's default."},
			{Key: "muteAddress", Label: "Mute address", Type: "text", Advanced: true, Placeholder: "/ch/{n}/mute", Help: "{n} = channel, {nn} = two digits."},
			{Key: "mutedValue", Label: "Value meaning muted", Type: "number", Advanced: true},
			{Key: "probe", Label: "Probe address", Type: "text", Advanced: true, Placeholder: "/?"},
			{Key: "maxChannel", Label: "Channels", Type: "number", Advanced: true},
		}})
}

func New(addr string, p Preset) *Mixer {
	return &Mixer{addr: addr, p: p, timeout: 600 * time.Millisecond}
}

func (m *Mixer) MaxChannel() int { return m.p.MaxChannel }

func (m *Mixer) muteAddr(ch int) string {
	a := strings.ReplaceAll(m.p.MuteAddress, "{nn}", fmt.Sprintf("%02d", ch))
	return strings.ReplaceAll(a, "{n}", strconv.Itoa(ch))
}

// exchange sends one message on a fresh UDP socket (consoles reply to
// the sender's port) and optionally waits for the reply.
func (m *Mixer) exchange(ctx context.Context, msg []byte, wantReply bool) (string, []any, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "udp", m.addr)
	if err != nil {
		return "", nil, fmt.Errorf("mixer unreachable: %w", err)
	}
	defer c.Close()
	dl := time.Now().Add(m.timeout)
	if t, ok := ctx.Deadline(); ok && t.Before(dl) {
		dl = t
	}
	c.SetDeadline(dl)
	if _, err := c.Write(msg); err != nil {
		return "", nil, fmt.Errorf("mixer unreachable: %w", err)
	}
	if !wantReply {
		return "", nil, nil
	}
	buf := make([]byte, 8192)
	n, err := c.Read(buf)
	if err != nil {
		return "", nil, fmt.Errorf("mixer at %s did not answer", m.addr)
	}
	return Decode(buf[:n])
}

func (m *Mixer) Probe(ctx context.Context) (string, error) {
	msg, _ := Encode(m.p.Probe)
	addr, vals, err := m.exchange(ctx, msg, true)
	if err != nil {
		return "", err
	}
	if addr != m.p.Probe {
		return "", fmt.Errorf("unexpected reply %q to the mixer probe", addr)
	}
	var parts []string
	for _, v := range vals {
		parts = append(parts, fmt.Sprint(v))
	}
	return strings.Join(parts, " "), nil
}

func (m *Mixer) check(ch int) error {
	if ch < 1 || ch > m.p.MaxChannel {
		return fmt.Errorf("channel %d is outside 1..%d", ch, m.p.MaxChannel)
	}
	return nil
}

func (m *Mixer) GetMute(ctx context.Context, ch int) (bool, error) {
	if err := m.check(ch); err != nil {
		return false, err
	}
	a := m.muteAddr(ch)
	msg, _ := Encode(a)
	addr, vals, err := m.exchange(ctx, msg, true)
	if err != nil {
		return false, err
	}
	if addr != a || len(vals) == 0 {
		return false, fmt.Errorf("unexpected mute reply for channel %d", ch)
	}
	var v int
	switch x := vals[0].(type) {
	case int32:
		v = int(x)
	case float32:
		v = int(x + 0.5)
	default:
		return false, fmt.Errorf("unexpected mute value type for channel %d", ch)
	}
	return v == m.p.MutedValue, nil
}

func (m *Mixer) SetMute(ctx context.Context, ch int, muted bool) error {
	if err := m.check(ch); err != nil {
		return err
	}
	v := 1 - m.p.MutedValue // "live" value
	if muted {
		v = m.p.MutedValue
	}
	msg, _ := Encode(m.muteAddr(ch), int32(v))
	_, _, err := m.exchange(ctx, msg, false) // OSC set is fire-and-forget; callers read back
	return err
}
