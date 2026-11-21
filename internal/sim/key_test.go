package sim

import "testing"

func TestWithPublishKey(t *testing.T) {
	for in, want := range map[string]string{
		"rtsp://127.0.0.1:8554/program":                 "rtsp://program:k1@127.0.0.1:8554/program",
		"srt://127.0.0.1:8890?streamid=publish:program": "srt://127.0.0.1:8890?streamid=publish:program:program:k1",
	} {
		if got := WithPublishKey(in, "program", "k1"); got != want {
			t.Errorf("%s: %s", in, got)
		}
	}
}
