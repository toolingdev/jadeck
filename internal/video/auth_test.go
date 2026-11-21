package video

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ptz/internal/settings"
)

func feedSettings() settings.Settings {
	var s settings.Settings
	s.Video.Managed = true
	s.Video.Feeds = []settings.Feed{
		{ID: "program", Name: "Program", Source: "push", Key: "0123456789abcdef0123456789abcdef"},
		{ID: "lobby", Name: "Lobby", Source: "pull", URL: "rtsp://10.0.0.5/1"},
		{ID: "nokey", Name: "No key", Source: "push"},
	}
	return s
}

func TestConfigPushFeedsNeedKey(t *testing.T) {
	s := feedSettings()
	conf := Config(s)
	key := s.Video.Feeds[0].Key
	if strings.Contains(conf, key) {
		t.Fatal("the publish key itself must not be in the gateway config (only its hash)")
	}
	for _, want := range []string{
		"authMethod: internal\n",
		// anyone reads; the API answers only this PC
		"- user: any\n  pass: ''\n  ips: []\n  permissions:\n  - action: read\n  - action: playback\n",
		"ips: ['127.0.0.1', '::1']\n  permissions:\n  - action: api\n",
		// the push feed is published only by its own user + key
		"- user: 'program'\n  pass: '" + hashed(key) + "'\n  ips: []\n  permissions:\n  - action: publish\n    path: 'program'\n",
		"  program:\n    source: publisher\n",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("config lacks %q:\n%s", want, conf)
		}
	}
	// nobody may publish without a key: no "any" user has publish, and a
	// keyless push feed gets no publisher at all
	any := strings.Split(conf, "- user: ")
	for _, u := range any[1:] {
		if strings.HasPrefix(u, "any") && strings.Contains(u, "action: publish") {
			t.Errorf("an anonymous user may publish:\n%s", u)
		}
		if strings.HasPrefix(u, "'nokey'") {
			t.Errorf("a push feed without a key got a publisher: %s", u)
		}
	}
	if !strings.HasPrefix(hashed("x"), "sha256:") {
		t.Fatal(hashed("x"))
	}
}

func TestPublishURLs(t *testing.T) {
	if got := RTSPPublishURL("192.168.1.20", "program", "k1"); got != "rtsp://program:k1@192.168.1.20:8554/program" {
		t.Fatal(got)
	}
	if got := SRTPublishURL("192.168.1.20", "program", "k1"); got != "srt://192.168.1.20:8890?streamid=publish:program:program:k1" {
		t.Fatal(got)
	}
	if got := RTSPPublishURL("fe80::1", "a", "k"); got != "rtsp://a:k@[fe80::1]:8554/a" {
		t.Fatal(got)
	}
}

// TestGatewayRejectsPublishWithoutKey runs the real MediaMTX with the
// generated config and publishes with ffmpeg. Opt-in: set
// JADECK_MEDIAMTX to a mediamtx binary (e.g. internal/video/bundle/
// mediamtx.bin) and have ffmpeg on PATH.
func TestGatewayRejectsPublishWithoutKey(t *testing.T) {
	bin := os.Getenv("JADECK_MEDIAMTX")
	ff, ferr := exec.LookPath("ffmpeg")
	if bin == "" || ferr != nil {
		t.Skip("set JADECK_MEDIAMTX and install ffmpeg to run the gateway auth test")
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
	s := feedSettings()
	conf := Config(s)
	conf = strings.Replace(conf, fmt.Sprintf("rtspAddress: :%d", PortRTSP), fmt.Sprintf("rtspAddress: 127.0.0.1:%d", rtsp), 1)
	conf = strings.Replace(conf, "apiAddress: "+apiAddr, fmt.Sprintf("apiAddress: 127.0.0.1:%d", api), 1)
	conf = strings.Replace(conf, "webrtc: yes", "webrtc: no", 1) // its fixed UDP port may be taken
	conf = strings.Replace(conf, fmt.Sprintf("hlsAddress: :%d", PortHLS), fmt.Sprintf("hlsAddress: 127.0.0.1:%d", hls), 1)
	conf = strings.Replace(conf, "srt: yes", "srt: no", 1)
	dir := t.TempDir()
	cfg := filepath.Join(dir, "mediamtx.yml")
	if err := os.WriteFile(cfg, []byte(conf), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var gwLog strings.Builder
	gw := exec.CommandContext(ctx, bin, cfg)
	gw.Stdout, gw.Stderr = &gwLog, &gwLog
	if err := gw.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); gw.Wait() }()
	apiURL := fmt.Sprintf("http://127.0.0.1:%d/v3/paths/get/program", api)
	for i := 0; ; i++ {
		if resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/v3/paths/list", api)); err == nil {
			resp.Body.Close()
			break
		}
		if i > 50 {
			t.Fatalf("gateway did not start:\n%s", gwLog.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
	publish := func(url string, secs int) *exec.Cmd {
		return exec.CommandContext(ctx, ff, "-hide_banner", "-loglevel", "error", "-re", "-f", "lavfi", "-i", "testsrc=size=160x120:rate=10",
			"-t", fmt.Sprint(secs), "-c:v", "libx264", "-preset", "ultrafast", "-g", "10", "-pix_fmt", "yuv420p", "-f", "rtsp", "-rtsp_transport", "tcp", url)
	}
	key := s.Video.Feeds[0].Key
	host := "127.0.0.1"
	withPort := func(u string) string {
		return strings.Replace(u, fmt.Sprintf(":%d/", PortRTSP), fmt.Sprintf(":%d/", rtsp), 1)
	}

	// no key, a wrong key, and the right key on another feed's path: all refused
	for name, url := range map[string]string{
		"no key":    fmt.Sprintf("rtsp://%s:%d/program", host, rtsp),
		"wrong key": withPort(RTSPPublishURL(host, "program", "not-the-key")),
		"keyless":   withPort(RTSPPublishURL(host, "nokey", key)),
		"other":     strings.Replace(withPort(RTSPPublishURL(host, "program", key)), "/program", "/nokey", 1),
	} {
		out, err := publish(url, 2).CombinedOutput()
		if err == nil {
			t.Errorf("%s: publish was accepted", name)
		}
		t.Logf("%s: refused (%v) %s", name, err, strings.TrimSpace(string(out)))
	}

	// the right key: accepted, and anyone can read it without a key
	p := publish(withPort(RTSPPublishURL(host, "program", key)), 15)
	var pubOut strings.Builder
	p.Stdout, p.Stderr = &pubOut, &pubOut
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	ready := false
	for i := 0; i < 50 && !ready; i++ {
		time.Sleep(100 * time.Millisecond)
		if resp, err := http.Get(apiURL); err == nil {
			var b strings.Builder
			buf := make([]byte, 4096)
			n, _ := resp.Body.Read(buf)
			b.Write(buf[:n])
			resp.Body.Close()
			ready = strings.Contains(b.String(), `"ready":true`)
		}
	}
	if !ready {
		t.Fatalf("publishing with the key did not make the path ready: %s\ngateway:\n%s", pubOut.String(), gwLog.String())
	}
	probe, _ := exec.LookPath("ffprobe")
	if probe != "" {
		out, err := exec.CommandContext(ctx, probe, "-hide_banner", "-loglevel", "error", "-rtsp_transport", "tcp",
			"-show_entries", "stream=codec_name", "-of", "csv=p=0", fmt.Sprintf("rtsp://%s:%d/program", host, rtsp)).CombinedOutput()
		if err != nil || !strings.Contains(string(out), "h264") {
			t.Errorf("reading without a key failed: %v %s", err, out)
		}
	}
	// a browser's HLS read needs no key either
	hlsOK, last := false, 0
	for i := 0; i < 80 && !hlsOK; i++ {
		if resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/program/index.m3u8", hls)); err == nil {
			last = resp.StatusCode
			hlsOK = resp.StatusCode == 200
			resp.Body.Close()
		}
		if !hlsOK {
			time.Sleep(200 * time.Millisecond)
		}
	}
	if !hlsOK {
		t.Errorf("HLS read without a key failed (last HTTP %d)", last)
	}
	p.Wait()
	if strings.Contains(gwLog.String(), key) {
		t.Error("the gateway log shows the publish key")
	}
}

// TestGatewayCutsOldKeyPublisher: a sender publishing with the old key
// is dropped once the key changes and cannot reconnect with it; the new
// key works. Opt-in like TestGatewayRejectsPublishWithoutKey.
func TestGatewayCutsOldKeyPublisher(t *testing.T) {
	bin := os.Getenv("JADECK_MEDIAMTX")
	ff, ferr := exec.LookPath("ffmpeg")
	if bin == "" || ferr != nil {
		t.Skip("set JADECK_MEDIAMTX and install ffmpeg to run the gateway auth test")
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
	s := feedSettings()
	oldKey := s.Video.Feeds[0].Key
	dir := t.TempDir()
	cfg := filepath.Join(dir, "mediamtx.yml")
	if err := os.WriteFile(cfg, []byte(render(s)), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var gwLog strings.Builder
	gw := exec.CommandContext(ctx, bin, cfg)
	gw.Stdout, gw.Stderr = &gwLog, &gwLog
	if err := gw.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); gw.Wait() }()
	g := &Gateway{API: fmt.Sprintf("127.0.0.1:%d", api)}
	for i := 0; ; i++ {
		if code, _, err := g.apiDo(ctx, "GET", "/v3/paths/list", nil); err == nil && code == 200 {
			break
		}
		if i > 50 {
			t.Fatalf("gateway did not start:\n%s", gwLog.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
	url := func(key string) string {
		return strings.Replace(RTSPPublishURL("127.0.0.1", "program", key), fmt.Sprintf(":%d/", PortRTSP), fmt.Sprintf(":%d/", rtsp), 1)
	}
	publish := func(key string, secs int) *exec.Cmd {
		return exec.CommandContext(ctx, ff, "-hide_banner", "-loglevel", "error", "-re", "-f", "lavfi", "-i", "testsrc=size=160x120:rate=10",
			"-t", fmt.Sprint(secs), "-c:v", "libx264", "-preset", "ultrafast", "-g", "10", "-pix_fmt", "yuv420p", "-f", "rtsp", "-rtsp_transport", "tcp", url(key))
	}
	ready := func() bool {
		code, b, err := g.apiDo(ctx, "GET", "/v3/paths/get/program", nil)
		return err == nil && code == 200 && strings.Contains(string(b), `"ready":true`)
	}
	waitReady := func(want bool, what string) {
		for i := 0; i < 80; i++ {
			if ready() == want {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("%s: path ready never became %v\ngateway:\n%s", what, want, gwLog.String())
	}

	// on air with the old key
	old := publish(oldKey, 60)
	if err := old.Start(); err != nil {
		t.Fatal(err)
	}
	oldDone := make(chan error, 1)
	go func() { oldDone <- old.Wait() }()
	waitReady(true, "old key")

	// new key: the config file is rewritten (as Apply does) and the sender cut off
	s.Video.Feeds[0].Key = "fedcba9876543210fedcba9876543210"
	if err := os.WriteFile(cfg, []byte(render(s)), 0o600); err != nil {
		t.Fatal(err)
	}
	kicked, err := g.CutPublishers(ctx, s, "program")
	if err != nil {
		t.Fatalf("cut: %v\ngateway:\n%s", err, gwLog.String())
	}
	if len(kicked) != 1 || kicked[0].Kind != "rtspsessions" {
		t.Fatalf("kicked %+v, want the one RTSP publisher", kicked)
	}
	select {
	case err := <-oldDone:
		t.Logf("old-key sender dropped (%v)", err)
	case <-time.After(10 * time.Second):
		t.Fatal("the old-key sender is still connected after the new key")
	}
	waitReady(false, "after cut")

	// the old key no longer gets in (right after the cut, and after the file reload)
	for _, wait := range []time.Duration{0, 2 * time.Second} {
		time.Sleep(wait)
		if out, err := publish(oldKey, 2).CombinedOutput(); err == nil {
			t.Fatalf("the old key published again (after %v): %s", wait, out)
		}
	}
	// the new key does
	nw := publish(s.Video.Feeds[0].Key, 5)
	if err := nw.Start(); err != nil {
		t.Fatal(err)
	}
	waitReady(true, "new key")
	nw.Wait()
	if strings.Contains(gwLog.String(), oldKey) || strings.Contains(gwLog.String(), s.Video.Feeds[0].Key) {
		t.Error("the gateway log shows a publish key")
	}
}
