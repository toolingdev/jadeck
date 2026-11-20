package sim

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log/slog"
	"math"
	"net"
	"regexp"
	"strconv"
	"sync"
)

// Mixer answers like a Behringer WING over OSC (UDP 2223): the probe
// "/?" and channel mutes /ch/{n}/mute (1 = muted), names and faders.
type Mixer struct {
	mu    sync.Mutex
	mute  [49]bool
	fader [49]float32
	Names [49]string
	Log   *slog.Logger
}

func NewMixer() *Mixer {
	m := &Mixer{Log: slog.Default()}
	names := []string{"", "Lead Vocal", "Lectern", "Wireless 1", "Wireless 2", "Keys L", "Keys R", "Guitar", "Bass",
		"Kick", "Snare", "OH L", "OH R", "PC Audio", "Video Playback", "Choir L", "Choir R", "Ambient"}
	for i := 1; i <= 48; i++ {
		m.fader[i] = 0.75
		if i < len(names) {
			m.Names[i] = names[i]
		} else {
			m.Names[i] = "Ch " + strconv.Itoa(i)
		}
	}
	m.mute[3] = true // a spare wireless, muted like at the start of a service
	return m
}

var chAddr = regexp.MustCompile(`^/ch/(\d+)/(mute|name|fdr|mix/on)$`)

func (m *Mixer) Serve(addr string) error {
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return fmt.Errorf("mixer (WING) on %s: %w", addr, err)
	}
	go func() {
		buf := make([]byte, 8192)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			if reply := m.handle(buf[:n]); reply != nil {
				pc.WriteTo(reply, from)
			}
		}
	}()
	return nil
}

func (m *Mixer) handle(msg []byte) []byte {
	addr, args, err := oscDecode(msg)
	if err != nil {
		return nil
	}
	if addr == "/?" || addr == "/info" {
		return oscEncode(addr, "WING", "sim", "3.0")
	}
	g := chAddr.FindStringSubmatch(addr)
	if g == nil {
		return nil
	}
	ch, _ := strconv.Atoi(g[1])
	if ch < 1 || ch > 48 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	switch g[2] {
	case "mute", "mix/on":
		on := g[2] == "mix/on" // X32 style: 0 = muted
		if len(args) > 0 {
			v := num(args[0])
			m.mute[ch] = (v >= 0.5) != on
			m.Log.Info("mixer", "channel", ch, "name", m.Names[ch], "muted", m.mute[ch])
		}
		v := int32(0)
		if m.mute[ch] != on {
			v = 1
		}
		return oscEncode(addr, v)
	case "name":
		return oscEncode(addr, m.Names[ch])
	case "fdr":
		if len(args) > 0 {
			m.fader[ch] = float32(num(args[0]))
		}
		return oscEncode(addr, m.fader[ch])
	}
	return nil
}

func num(v any) float64 {
	switch x := v.(type) {
	case int32:
		return float64(x)
	case float32:
		return float64(x)
	}
	return 0
}

// ------------------------------------------------------------- OSC

func pad4(b []byte) []byte {
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}

func oscEncode(addr string, args ...any) []byte {
	var b bytes.Buffer
	b.Write(pad4(append([]byte(addr), 0)))
	tags := []byte{','}
	var data bytes.Buffer
	for _, a := range args {
		switch v := a.(type) {
		case int32:
			tags = append(tags, 'i')
			binary.Write(&data, binary.BigEndian, v)
		case float32:
			tags = append(tags, 'f')
			binary.Write(&data, binary.BigEndian, math.Float32bits(v))
		case string:
			tags = append(tags, 's')
			data.Write(pad4(append([]byte(v), 0)))
		}
	}
	b.Write(pad4(append(tags, 0)))
	b.Write(data.Bytes())
	return b.Bytes()
}

func oscString(b []byte, off int) (string, int, error) {
	i := bytes.IndexByte(b[off:], 0)
	if i < 0 {
		return "", 0, fmt.Errorf("bad string")
	}
	s := string(b[off : off+i])
	n := off + i + 1
	for n%4 != 0 {
		n++
	}
	return s, n, nil
}

func oscDecode(b []byte) (string, []any, error) {
	addr, off, err := oscString(b, 0)
	if err != nil {
		return "", nil, err
	}
	if off >= len(b) {
		return addr, nil, nil
	}
	tags, off, err := oscString(b, off)
	if err != nil || len(tags) == 0 || tags[0] != ',' {
		return addr, nil, nil
	}
	var args []any
	for _, t := range tags[1:] {
		switch t {
		case 'i':
			if off+4 > len(b) {
				return addr, args, nil
			}
			args = append(args, int32(binary.BigEndian.Uint32(b[off:])))
			off += 4
		case 'f':
			if off+4 > len(b) {
				return addr, args, nil
			}
			args = append(args, math.Float32frombits(binary.BigEndian.Uint32(b[off:])))
			off += 4
		case 's':
			var s string
			s, off, err = oscString(b, off)
			if err != nil {
				return addr, args, nil
			}
			args = append(args, s)
		}
	}
	return addr, args, nil
}
