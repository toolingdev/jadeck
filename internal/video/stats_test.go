package video

import (
	"testing"
	"time"
)

func TestStatsRates(t *testing.T) {
	var st statsState
	t0 := time.Now()
	mk := func(at time.Time, in, out, wout, srtIn float64) *raw {
		return &raw{at: at,
			paths: []rawPath{{map[string]any{"name": "cam-a", "online": true, "available": true, "inboundBytes": in, "outboundBytes": out,
				"source":  map[string]any{"type": "rtspSource"},
				"readers": []any{map[string]any{}, map[string]any{}},
				"tracks2": []any{map[string]any{"codec": "H264", "codecProps": map[string]any{"width": 1920.0, "height": 1080.0}}}}}},
			webrtc: []map[string]any{{"id": "w1", "path": "cam-a", "remoteAddr": "10.0.0.9:5000", "state": "read", "outboundBytes": wout,
				"localCandidate": "host/udp/10.0.0.2/8189", "remoteCandidate": "host/udp/10.0.0.9/5000", "created": t0.Format(time.RFC3339Nano)}},
			srt: []map[string]any{{"id": "s1", "path": "program", "state": "publish", "remoteAddr": "10.0.0.3:9000", "bytesReceived": srtIn, "msRTT": 2.5, "packetsReceivedLossRate": 0.1}},
		}
	}
	st.apply(mk(t0, 0, 0, 0, 0))
	if s := st.last; s.InKbps != 0 || len(s.Feeds) != 1 || s.Feeds[0].Viewers != 2 || s.Feeds[0].Width != 1920 {
		t.Fatalf("first sample: %+v", s)
	}
	// 2 s later: 1 MB in, 2 MB out, viewer 1 MB, vMix 0.5 MB.
	st.apply(mk(t0.Add(2*time.Second), 1e6, 2e6, 1e6, 5e5))
	s := st.last
	if s.Feeds[0].InKbps != 4000 || s.Feeds[0].OutKbps != 8000 || s.InKbps != 4000 {
		t.Fatalf("feed rates: %+v", s.Feeds[0])
	}
	if len(s.Viewers) != 1 || s.Viewers[0].Kbps != 4000 || s.Viewers[0].Connect != "host→host" {
		t.Fatalf("viewer: %+v", s.Viewers)
	}
	if len(s.Senders) != 1 || s.Senders[0].Kbps != 2000 || s.Senders[0].RTTms != 2.5 {
		t.Fatalf("sender: %+v", s.Senders)
	}
}
