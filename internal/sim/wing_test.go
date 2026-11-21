package sim

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"io"
	"log/slog"
	"net"
	"strconv"
	"testing"
	"time"
)

func startWing(t *testing.T) (*Wing, net.Conn) {
	t.Helper()
	w := NewWing()
	w.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := w.Serve("127.0.0.1", 0, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	o, _ := w.Ports()
	c, err := net.Dial("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(o)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return w, c
}

// ask sends one OSC message and returns the reply (nil after timeout).
func ask(t *testing.T, c net.Conn, addr string, args ...any) (string, []any) {
	t.Helper()
	c.Write(wOSCEncode(addr, args...))
	c.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	buf := make([]byte, 1<<16)
	n, err := c.Read(buf)
	if err != nil {
		return "", nil
	}
	a, v, err := wOSCDecode(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	return a, v
}

func TestWingOSC(t *testing.T) {
	w, c := startWing(t)
	if a, v := ask(t, c, "/?"); a != "/?" || len(v) != 1 || !bytes.HasPrefix([]byte(v[0].(string)), []byte("WING,127.0.0.1,")) {
		t.Fatalf("identity: %s %v", a, v)
	}
	if _, v := ask(t, c, "/ch/3/name"); len(v) != 1 || v[0] != "Lead vox" {
		t.Fatalf("name: %v", v)
	}
	_, v := ask(t, c, "/ch/3/fdr")
	if len(v) != 3 || v[0] != "1.5" || v[2].(float32) != 1.5 {
		t.Fatalf("fader triplet: %v", v)
	}
	// a set is silent; the read-back has it
	c.Write(wOSCEncode("/ch/3/fdr", float32(-7.5)))
	time.Sleep(50 * time.Millisecond)
	if _, v := ask(t, c, "/ch/3/fdr"); v[2].(float32) != -7.5 {
		t.Fatalf("fader read back: %v", v)
	}
	if f, _, _ := w.Value("/ch/3/$fdr"); f != -7.5 {
		t.Fatalf("effective fader: %v", f)
	}
	if a, v := ask(t, c, "/ch/99/fdr"); a != "/*" || v[0] != "NODE NOT FOUND" {
		t.Fatalf("unknown path: %s %v", a, v)
	}
	if a, v := ask(t, c, "/ch/3/fdr", float32(30)); a != "/*" || v[0] != "VALUE ERROR" {
		t.Fatalf("out of range: %s %v", a, v)
	}
	// stereo keys, mono wedge
	if _, v := ask(t, c, "/ch/6/in/set/$mode"); v[0] != "ST" {
		t.Fatalf("keys stereo: %v", v)
	}
	if _, v := ask(t, c, "/bus/1/busmono"); v[2].(int32) != 1 {
		t.Fatalf("wedge mono: %v", v)
	}
	// definitions: children of /ch/1, each a DF record, then DE
	_, v = ask(t, c, "/ch/1", []byte{tokDefReq})
	blob, _ := v[0].([]byte)
	if len(blob) < 20 || blob[0] != tokDef || blob[len(blob)-1] != tokEnd {
		t.Fatalf("dd: %x", blob[:min(len(blob), 16)])
	}
	h := make([]byte, 4)
	binary.BigEndian.PutUint32(h, crc32.ChecksumIEEE([]byte("/ch/1/mute")))
	if !bytes.Contains(blob, h) {
		t.Fatal("dd should carry /ch/1/mute's hash")
	}
	// OSC lock: sets are dropped without a word
	c.Write(wOSCEncode("/$ctl/osc/ronly", int32(1)))
	time.Sleep(30 * time.Millisecond)
	c.Write(wOSCEncode("/ch/1/mute", int32(1)))
	time.Sleep(30 * time.Millisecond)
	if m, _, _ := w.Value("/ch/1/mute"); m != 0 {
		t.Fatal("a set under the OSC lock should be dropped")
	}
}

func TestWingScenesRecorderTalkback(t *testing.T) {
	w, c := startWing(t)
	if _, v := ask(t, c, "/$ctl/lib/$actidx"); v[2].(int32) != 2 {
		t.Fatalf("starts on scene 2: %v", v)
	}
	c.Write(wOSCEncode("/$ctl/lib/$actionidx", int32(4)))
	c.Write(wOSCEncode("/$ctl/lib/$action", "GO"))
	time.Sleep(50 * time.Millisecond)
	if n, _, _ := w.Value("/$ctl/lib/$actidx"); n != 4 {
		t.Fatalf("scene GO: %v", n)
	}
	if m, _, _ := w.Value("/mgrp/1/mute"); m != 1 {
		t.Fatal("the sermon scene mutes the band group")
	}
	if m, _, _ := w.Value("/ch/7/$mute"); m != 2 {
		t.Fatalf("guitar muted by its group: %v", m)
	}
	c.Write(wOSCEncode("/rec/$action", "REC"))
	c.Write(wOSCEncode("/cfg/talk/A/$on", int32(1)))
	time.Sleep(50 * time.Millisecond)
	if _, s, _ := w.Value("/rec/$actstate"); s != "REC" {
		t.Fatalf("recorder: %s", s)
	}
	if n, _, _ := w.Value("/cfg/talk/A/$on"); n != 1 {
		t.Fatal("talkback")
	}
}

func TestWingDiscovery(t *testing.T) {
	w, _ := startWing(t)
	_, n := w.Ports()
	c, err := net.Dial("udp", "127.0.0.1:"+strconv.Itoa(n))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write([]byte("WING?"))
	c.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 128)
	k, err := c.Read(buf)
	if err != nil || !bytes.HasPrefix(buf[:k], []byte("WING,127.0.0.1,Sim WING,")) {
		t.Fatalf("discovery: %q %v", buf[:k], err)
	}
}

// native reads channel-1 payload bytes from the TCP link until want
// matches or the deadline passes.
func nativeUntil(t *testing.T, conn net.Conn, dec *nrpDecoder, want func([]byte) bool) []byte {
	t.Helper()
	var got []byte
	buf := make([]byte, 1<<16)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn.SetReadDeadline(deadline)
		n, err := conn.Read(buf)
		if err != nil {
			break
		}
		dec.feed(buf[:n], func(ch int, db byte) {
			if ch == nrpAudio {
				got = append(got, db)
			}
		})
		if want(got) {
			return got
		}
	}
	t.Fatalf("native: never got it (%d bytes)", len(got))
	return nil
}

func TestWingNativeEventsAndMeters(t *testing.T) {
	w, c := startWing(t)
	_, n := w.Ports()
	conn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(n))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	enc, dec := newNRPEncoder(), newNRPDecoder()
	conn.Write(enc.frame(nrpAudio, []byte{tokRoot, tokDefReq})) // keep-alive
	nativeUntil(t, conn, dec, func(b []byte) bool { return len(b) > 0 && b[len(b)-1] == tokEnd })

	// a change made over OSC is pushed: D7 <hash of /ch/2/mute> 01
	c.Write(wOSCEncode("/ch/2/mute", int32(1)))
	ev := binary.BigEndian.AppendUint32([]byte{tokHash}, crc32.ChecksumIEEE([]byte("/ch/2/mute")))
	ev = append(ev, 1)
	nativeUntil(t, conn, dec, func(b []byte) bool { return bytes.Contains(b, ev) })

	// meters: ask for ch 3 and main 1 on our UDP port
	mc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer mc.Close()
	req := binary.BigEndian.AppendUint16([]byte{tokI16}, uint16(mc.LocalAddr().(*net.UDPAddr).Port))
	req = binary.BigEndian.AppendUint32(append(req, tokI32), 7)
	req = append(req, tokReq, 0xAB, 2, 0xAE, 0, tokEnd)
	conn.Write(enc.frame(nrpMeters, req))
	var levels []float64
	buf := make([]byte, 2048)
	for i := 0; i < 6; i++ {
		mc.SetReadDeadline(time.Now().Add(time.Second))
		k, _, err := mc.ReadFrom(buf)
		if err != nil {
			t.Fatalf("meters: %v", err)
		}
		if k != 4+2*11*2 || binary.BigEndian.Uint32(buf) != 7 {
			t.Fatalf("meter datagram: %d bytes, id %d", k, binary.BigEndian.Uint32(buf))
		}
		mainL := float64(int16(binary.BigEndian.Uint16(buf[4+22+4:]))) / 256
		if mainL < -60 || mainL > 6 {
			t.Fatalf("main level %.1f dB is not believable", mainL)
		}
		levels = append(levels, float64(int16(binary.BigEndian.Uint16(buf[4:])))/256)
	}
	moved := false
	for _, l := range levels[1:] {
		moved = moved || l != levels[0]
	}
	if !moved {
		t.Fatalf("meters should move: %v", levels)
	}
	if w.Clients() != 1 {
		t.Fatalf("clients: %d", w.Clients())
	}
}
