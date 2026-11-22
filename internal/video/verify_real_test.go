package video

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"ptz/internal/settings"
)

// TestGatewayMissedReloadRepaired reproduces the venue bug against the
// real MediaMTX: feeds written ~100 ms apart leave the last one out of
// the running gateway until the verifier repairs it; a wrong key is
// recorded as a refused sender. Opt-in like
// TestGatewayRejectsPublishWithoutKey (JADECK_MEDIAMTX + ffmpeg).
func TestGatewayMissedReloadRepaired(t *testing.T) {
	bin := os.Getenv("JADECK_MEDIAMTX")
	ff, ferr := exec.LookPath("ffmpeg")
	if bin == "" || ferr != nil {
		t.Skip("set JADECK_MEDIAMTX and install ffmpeg to run the gateway reload test")
	}
	free := func() int {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		return l.Addr().(*net.TCPAddr).Port
	}
	rtsp, api, hls := free(), free(), free()
	render := func(s settings.Settings) string {
		conf := Config(s)
		conf = strings.Replace(conf, fmt.Sprintf("rtspAddress: :%d", PortRTSP), fmt.Sprintf("rtspAddress: 127.0.0.1:%d", rtsp), 1)
		conf = strings.Replace(conf, "apiAddress: "+apiAddr, fmt.Sprintf("apiAddress: 127.0.0.1:%d", api), 1)
		conf = strings.Replace(conf, "webrtc: yes", "webrtc: no", 1)
		conf = strings.Replace(conf, fmt.Sprintf("hlsAddress: :%d", PortHLS), fmt.Sprintf("hlsAddress: 127.0.0.1:%d", hls), 1)
		return strings.Replace(conf, "srt: yes", "srt: no", 1)
	}
	g := &Gateway{Dir: t.TempDir(), API: fmt.Sprintf("127.0.0.1:%d", api), render: render}
	first := twoFeeds()
	first.Video.Feeds = first.Video.Feeds[:1]
	if err := g.writeConfig(first); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var gwLog strings.Builder
	var logMu sync.Mutex
	gw := exec.CommandContext(ctx, bin, g.configPath())
	lw := &lineWriter{g: g}
	gw.Stdout = writerFunc(func(p []byte) (int, error) {
		logMu.Lock()
		gwLog.Write(p)
		logMu.Unlock()
		return lw.Write(p)
	})
	gw.Stderr = gw.Stdout
	if err := gw.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); gw.Wait() }()
	logText := func() string {
		logMu.Lock()
		defer logMu.Unlock()
		return gwLog.String()
	}
	for i := 0; ; i++ {
		if code, _, err := g.apiDo(ctx, "GET", "/v3/paths/list", nil); err == nil && code == 200 {
			break
		}
		if i > 50 {
			t.Fatalf("gateway did not start:\n%s", logText())
		}
		time.Sleep(100 * time.Millisecond)
	}
	paths := func() string {
		code, b, err := g.apiDo(ctx, "GET", "/v3/config/paths/list", nil)
		if err != nil || code != 200 {
			return ""
		}
		var l struct{ Items []struct{ Name string } }
		json.Unmarshal(b, &l)
		var n []string
		for _, it := range l.Items {
			n = append(n, it.Name)
		}
		sort.Strings(n)
		return strings.Join(n, ",")
	}

	// the venue: one feed added, then another ~100 ms later (no verifier yet)
	time.Sleep(1200 * time.Millisecond)
	mid := first
	mid.Video.Feeds = append(append([]settings.Feed(nil), first.Video.Feeds...),
		settings.Feed{ID: "slides", Name: "Slides", Source: "push", Key: "aaaabbbbccccddddaaaabbbbccccdddd"})
	g.writeConfig(mid)
	time.Sleep(100 * time.Millisecond)
	all := mid
	all.Video.Feeds = append(append([]settings.Feed(nil), mid.Video.Feeds...), twoFeeds().Video.Feeds[1])
	g.writeConfig(all)
	time.Sleep(2 * time.Second)
	if got := paths(); got == "program,slides" {
		t.Logf("reproduced: running paths %q after writing program, slides, confidence", got)
	} else {
		t.Logf("MediaMTX did not miss the last write this time (paths %q); still checking the verifier", got)
	}

	pub := func(path, key string) error {
		u := fmt.Sprintf("rtsp://%s:%s@127.0.0.1:%d/%s", path, key, rtsp, path)
		return exec.CommandContext(ctx, ff, "-hide_banner", "-loglevel", "error", "-re", "-f", "lavfi", "-i", "testsrc=size=160x120:rate=10",
			"-t", "1", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-f", "rtsp", "-rtsp_transport", "tcp", u).Run()
	}
	// a wrong key is recorded as a refused sender
	pub("program", "wrong-key")
	time.Sleep(300 * time.Millisecond)
	if r := g.Refused([]string{"program", "slides", "confidence"}, nil); r["program"].Reason != "key" {
		t.Errorf("wrong key not recorded: %+v\n%s", r, logText())
	}

	// now the verifier, as Run starts it
	g.mu.Lock()
	g.init()
	g.state, g.cmd = "running", gw
	cp := all
	g.expect = &cp
	g.mu.Unlock()
	go g.verifyLoop(ctx)
	g.pokeVerify()
	for i := 0; paths() != "confidence,program,slides"; i++ {
		if i > 100 {
			t.Fatalf("not repaired: %q\n%s", paths(), logText())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if d, err := g.check(ctx, all); err != nil || !d.empty() {
		t.Fatalf("after repair: %+v %v", d, err)
	}
	// and the right key now publishes on confidence
	if err := pub("confidence", all.Video.Feeds[2].Key); err != nil {
		t.Fatalf("confidence publish after repair: %v\n%s", err, logText())
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
