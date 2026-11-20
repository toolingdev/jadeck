// Command ptzsim simulates a whole booth for trying jadeck without gear:
// N PTZOptics cameras with live video of a drawn venue (pan, tilt and
// zoom move the picture), a vMix switcher (Web Controller API, program
// output over SRT), Proclaim slides (network control on 52195) and a
// Behringer WING (OSC on 2223). jadeck and its add-ons talk to them
// exactly as to the real things.
//
//	ptzsim [--cameras 4] [--setup http://127.0.0.1:8080]
//
// Needs ffmpeg (video encoding) and MediaMTX (each camera's RTSP
// server; jadeck unpacks one into <data>/video/bin).
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"time"

	"ptz/internal/sim"
)

func findMediaMTX() string {
	if p, err := exec.LookPath("mediamtx"); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	for _, p := range []string{
		filepath.Join(home, "ptz-demo", "data", "video", "bin", "mediamtx"),
		filepath.Join("data", "video", "bin", "mediamtx"),
	} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

func main() {
	var c sim.Config
	flag.IntVar(&c.Cameras, "cameras", 4, "how many cameras (1-8 get names; more are numbered)")
	flag.StringVar(&c.Host, "host", "127.0.0.1", "address the gear listens on (0.0.0.0 for other machines)")
	flag.IntVar(&c.ViscaBase, "visca", 15700, "VISCA port of camera 1 (camera n: +n-1)")
	flag.IntVar(&c.HTTPBase, "http", 18100, "HTTP port of camera 1")
	flag.IntVar(&c.RTSPPort, "rtsp", 18554, "RTSP port for every camera's video")
	flag.StringVar(&c.Switcher, "vmix", "127.0.0.1:8088", "vMix Web Controller address")
	flag.StringVar(&c.Slides, "proclaim", "127.0.0.1:52195", "Proclaim network control address")
	flag.StringVar(&c.Mixer, "wing", "127.0.0.1:2223", "WING OSC address (UDP)")
	flag.StringVar(&c.Program, "program", "rtsp://127.0.0.1:8554/program", "where to publish the program picture: jadeck's gateway (RTSP, or srt://…?streamid=publish:program if your ffmpeg has SRT); \"\" = off")
	flag.IntVar(&c.FPS, "fps", 15, "frames per second")
	flag.StringVar(&c.FFmpeg, "ffmpeg", "ffmpeg", "ffmpeg to encode video with")
	flag.StringVar(&c.MediaMTX, "mediamtx", findMediaMTX(), "MediaMTX binary for the cameras' RTSP server")
	setup := flag.String("setup", "", "add the gear to this jadeck (e.g. http://127.0.0.1:8080)")
	flag.Parse()

	c.Log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	for _, a := range []*string{&c.Switcher, &c.Slides, &c.Mixer} {
		if c.Host != "127.0.0.1" {
			_, p, _ := cut(*a)
			*a = c.Host + ":" + p
		}
	}
	rig, err := sim.Start(ctx, c)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ptzsim:", err)
		os.Exit(1)
	}
	if *setup != "" {
		time.Sleep(time.Second)
		rig.Setup(ctx, *setup)
	}
	c.Log.Info("simulator running — Ctrl-C to stop")
	<-ctx.Done()
}

func cut(a string) (string, string, bool) {
	for i := len(a) - 1; i >= 0; i-- {
		if a[i] == ':' {
			return a[:i], a[i+1:], true
		}
	}
	return a, "", false
}
