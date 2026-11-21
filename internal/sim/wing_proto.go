package sim

// Wire formats for the simulated Behringer WING, ported from the
// behringer-wing add-on (codec.go, native.go): OSC with blobs, the native
// protocol's channel framing, value tokens and node-definition records
// (WING Remote Protocols 3.1, p.16-29 and p.94-99).

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
)

// ---------------------------------------------------------------- OSC

func wOSCString(s string) []byte { return pad4(append([]byte(s), 0)) }

// wOSCEncode builds one OSC message. args: int, int32, float32,
// float64, string, []byte (blob).
func wOSCEncode(address string, args ...any) []byte {
	tags := ","
	var body []byte
	for _, a := range args {
		switch v := a.(type) {
		case int:
			tags += "i"
			body = binary.BigEndian.AppendUint32(body, uint32(int32(v)))
		case int32:
			tags += "i"
			body = binary.BigEndian.AppendUint32(body, uint32(v))
		case float32:
			tags += "f"
			body = binary.BigEndian.AppendUint32(body, math.Float32bits(v))
		case float64:
			tags += "f"
			body = binary.BigEndian.AppendUint32(body, math.Float32bits(float32(v)))
		case string:
			tags += "s"
			body = append(body, wOSCString(v)...)
		case []byte:
			tags += "b"
			body = binary.BigEndian.AppendUint32(body, uint32(len(v)))
			body = pad4(append(body, v...))
		}
	}
	out := wOSCString(address)
	out = append(out, wOSCString(tags)...)
	return append(out, body...)
}

var errWOSC = errors.New("osc: malformed message")

func wReadString(b []byte, off int) (string, int, error) {
	end := -1
	for i := off; i < len(b); i++ {
		if b[i] == 0 {
			end = i
			break
		}
	}
	if end < 0 {
		return "", 0, errWOSC
	}
	s := string(b[off:end])
	return s, off + (len(s)+4)/4*4, nil
}

// wOSCDecode parses one OSC message: int32, float32, string or []byte.
func wOSCDecode(b []byte) (string, []any, error) {
	if len(b) == 0 || b[0] != '/' {
		return "", nil, errWOSC
	}
	addr, off, err := wReadString(b, 0)
	if err != nil {
		return "", nil, err
	}
	if off >= len(b) {
		return addr, nil, nil
	}
	tags, off, err := wReadString(b, off)
	if err != nil || !strings.HasPrefix(tags, ",") {
		return "", nil, errWOSC
	}
	var vals []any
	for _, t := range tags[1:] {
		switch t {
		case 'i', 'f':
			if off+4 > len(b) {
				return "", nil, errWOSC
			}
			u := binary.BigEndian.Uint32(b[off:])
			off += 4
			if t == 'i' {
				vals = append(vals, int32(u))
			} else {
				vals = append(vals, math.Float32frombits(u))
			}
		case 's':
			var s string
			if s, off, err = wReadString(b, off); err != nil {
				return "", nil, err
			}
			vals = append(vals, s)
		case 'b':
			if off+4 > len(b) {
				return "", nil, errWOSC
			}
			n := int(binary.BigEndian.Uint32(b[off:]))
			off += 4
			if n < 0 || off+n > len(b) {
				return "", nil, errWOSC
			}
			vals = append(vals, append([]byte(nil), b[off:off+n]...))
			off += (n + 3) / 4 * 4
		default:
			return "", nil, fmt.Errorf("osc: unsupported typetag %q", t)
		}
	}
	return addr, vals, nil
}

// ------------------------------------------------------------- native

const (
	nrpEsc      = 0xDF
	nrpChanBase = 0xD0
	nrpChans    = 14

	nrpAudio  = 1 // DF D1: audio engine & control (the parameter tree)
	nrpMeters = 3 // DF D3: meter requests
)

// Value / navigation tokens (Table 2, p.97).
const (
	tokIntMax   = 0x3F
	tokStrLo    = 0x80
	tokEmptyStr = 0xD0
	tokStr      = 0xD1
	tokI16      = 0xD3
	tokI32      = 0xD4
	tokF32      = 0xD5
	tokRawF     = 0xD6
	tokHash     = 0xD7
	tokRoot     = 0xDA
	tokReq      = 0xDC
	tokDefReq   = 0xDD
	tokEnd      = 0xDE
	tokDef      = 0xDF
)

// nrpEncoder escapes payload bytes for one channel (the doc's data_tx).
type nrpEncoder struct{ ch int }

func newNRPEncoder() *nrpEncoder { return &nrpEncoder{ch: -1} }

func (e *nrpEncoder) frame(ch int, data []byte) []byte {
	var out []byte
	if e.ch != ch {
		e.ch = ch
		out = append(out, nrpEsc, byte(nrpChanBase+ch))
	}
	esc := false
	for _, db := range data {
		if db == nrpEsc {
			esc = true
		} else {
			if esc && db >= nrpChanBase && db <= nrpChanBase+nrpChans {
				out = append(out, nrpEsc-1) // DF DE stands for a literal DF here
			}
			esc = false
		}
		out = append(out, db)
	}
	if esc {
		out = append(out, nrpEsc-1)
	}
	return out
}

// nrpDecoder undoes the framing (the doc's nrpc_data_rx).
type nrpDecoder struct {
	escf bool
	ch   int
}

func newNRPDecoder() *nrpDecoder { return &nrpDecoder{ch: -1} }

func (d *nrpDecoder) feed(b []byte, emit func(ch int, db byte)) {
	for _, db := range b {
		if db == nrpEsc && !d.escf {
			d.escf = true
			continue
		}
		if d.escf && db != nrpEsc {
			d.escf = false
			if db == nrpEsc-1 {
				db = nrpEsc
			} else if db >= nrpChanBase && db < nrpChanBase+nrpChans {
				d.ch = int(db - nrpChanBase)
				continue
			} else if d.ch >= 0 {
				emit(d.ch, nrpEsc)
			}
		}
		if d.ch >= 0 {
			emit(d.ch, db)
		}
	}
}

// nval is one value token: 'i' int, 'f' float, 's' string.
type nval struct {
	kind byte
	i    int64
	f    float64
	s    string
}

func encodeValue(v nval) []byte {
	switch v.kind {
	case 'i':
		switch {
		case v.i >= 0 && v.i <= tokIntMax:
			return []byte{byte(v.i)}
		case v.i >= math.MinInt16 && v.i <= math.MaxInt16:
			return binary.BigEndian.AppendUint16([]byte{tokI16}, uint16(int16(v.i)))
		default:
			return binary.BigEndian.AppendUint32([]byte{tokI32}, uint32(int32(v.i)))
		}
	case 'f':
		return binary.BigEndian.AppendUint32([]byte{tokF32}, math.Float32bits(float32(v.f)))
	case 's':
		n := len(v.s)
		switch {
		case n == 0:
			return []byte{tokEmptyStr}
		case n <= 64:
			return append([]byte{byte(tokStrLo + n - 1)}, v.s...)
		default:
			if n > 256 {
				n = 256
			}
			return append([]byte{tokStr, byte(n - 1)}, v.s[:n]...)
		}
	}
	return nil
}

// Node types (flags bits 4-7).
const (
	typeNode = iota
	typeLinF
	typeLogF
	typeFader
	typeInt
	typeEnum
	typeFEnum
	typeString
)

// nodeDef is one node-definition record (p.97, p.29).
type nodeDef struct {
	Parent, Hash uint32
	Index        int
	Name, Long   string
	Flags        uint16
	Min, Max     float64
	Steps        int
	Items        []string
	ItemLong     []string
	MaxLen       int
}

func (d nodeDef) Type() int { return int(d.Flags>>4) & 0x0F }

// encodeDef builds one DF record.
func encodeDef(d nodeDef) []byte {
	var body []byte
	body = binary.BigEndian.AppendUint32(body, d.Parent)
	body = binary.BigEndian.AppendUint32(body, d.Hash)
	body = binary.BigEndian.AppendUint16(body, uint16(d.Index))
	body = append(append(body, byte(len(d.Name))), d.Name...)
	body = append(append(body, byte(len(d.Long))), d.Long...)
	body = binary.BigEndian.AppendUint16(body, d.Flags)
	switch d.Type() {
	case typeLinF, typeLogF:
		body = binary.BigEndian.AppendUint32(body, math.Float32bits(float32(d.Min)))
		body = binary.BigEndian.AppendUint32(body, math.Float32bits(float32(d.Max)))
		body = binary.BigEndian.AppendUint32(body, uint32(d.Steps))
	case typeInt:
		body = binary.BigEndian.AppendUint32(body, uint32(int32(d.Min)))
		body = binary.BigEndian.AppendUint32(body, uint32(int32(d.Max)))
	case typeEnum:
		body = binary.BigEndian.AppendUint16(body, uint16(len(d.Items)))
		for i, it := range d.Items {
			long := ""
			if i < len(d.ItemLong) {
				long = d.ItemLong[i]
			}
			body = append(append(body, byte(len(it))), it...)
			body = append(append(body, byte(len(long))), long...)
		}
	case typeString:
		body = binary.BigEndian.AppendUint16(body, uint16(d.MaxLen))
	}
	out := []byte{tokDef}
	if len(body) <= 0xFFFF {
		out = binary.BigEndian.AppendUint16(out, uint16(len(body)))
	} else {
		out = binary.BigEndian.AppendUint16(out, 0)
		out = binary.BigEndian.AppendUint32(out, uint32(len(body)))
	}
	return append(out, body...)
}

// ------------------------------------------------------------- strips

// wStrip is one strip: Kind is ch, aux, bus, main, mtx, dca or mgrp.
type wStrip struct {
	Kind string
	N    int
}

func (s wStrip) Path() string { return fmt.Sprintf("/%s/%d", s.Kind, s.N) }

var wKinds = map[string]struct {
	count  int
	chLike bool
}{
	"ch": {40, true}, "aux": {8, true}, "bus": {16, true}, "main": {4, true}, "mtx": {8, true},
	"dca": {16, false}, "mgrp": {8, false},
}

var wKindOrder = []string{"ch", "aux", "bus", "main", "mtx", "dca", "mgrp"}

func wAllStrips() []wStrip {
	var out []wStrip
	for _, k := range wKindOrder {
		for n := 1; n <= wKinds[k].count; n++ {
			out = append(out, wStrip{k, n})
		}
	}
	return out
}

// Meter families (p.98): token and int16 words per strip.
var wMeterFamily = map[string]struct {
	tok   byte
	words int
}{
	"ch": {0xAB, 11}, "aux": {0xAC, 11}, "bus": {0xAD, 11}, "main": {0xAE, 11}, "mtx": {0xAF, 11},
	"dca": {0xA5, 4},
}

// The WING fader law: normalized position for dB (the add-on's reading
// of the doc: 0.75 = 0 dB, X32 segments below -10 dB).
var wFaderLaw = [][2]float64{{0, -144}, {0.0625, -60}, {0.25, -30}, {0.5, -10}, {0.75, 0}, {1, 10}}

func wDBToPos(db float64) float64 {
	if math.IsNaN(db) || db <= -144 {
		return 0
	}
	if db >= 10 {
		return 1
	}
	for i := 1; i < len(wFaderLaw); i++ {
		p0, d0 := wFaderLaw[i-1][0], wFaderLaw[i-1][1]
		p1, d1 := wFaderLaw[i][0], wFaderLaw[i][1]
		if db <= d1 {
			if i == 1 {
				return p1 * (db - d0) / (d1 - d0)
			}
			return p0 + (p1-p0)*(db-d0)/(d1-d0)
		}
	}
	return 1
}
