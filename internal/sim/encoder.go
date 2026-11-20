package sim

import (
	"context"
	"fmt"
	"image"
	"io"
	"log/slog"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

// Output is one place an encoder publishes to.
type Output struct {
	URL   string // rtsp://… (publish) or srt://… (MPEG-TS)
	Scale string // e.g. "640:360"; blank = full size
	Kbps  int
}

// Encoder turns RGBA frames into H.264 with ffmpeg and publishes them,
// restarting ffmpeg when it stops (e.g. the server isn't up yet).
type Encoder struct {
	FFmpeg  string
	W, H    int
	FPS     int
	Outputs []Output
	Log     *slog.Logger
	Name    string

	mu sync.Mutex
	in io.WriteCloser
}

func (e *Encoder) args() []string {
	a := []string{"-hide_banner", "-loglevel", "error", "-f", "rawvideo", "-pix_fmt", "rgba",
		"-s", fmt.Sprintf("%dx%d", e.W, e.H), "-r", strconv.Itoa(e.FPS), "-i", "-"}
	for _, o := range e.Outputs {
		a = append(a, "-map", "0:v")
		if o.Scale != "" {
			a = append(a, "-vf", "scale="+o.Scale)
		}
		k := o.Kbps
		if k == 0 {
			k = 2500
		}
		a = append(a, "-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency", "-pix_fmt", "yuv420p",
			"-g", strconv.Itoa(e.FPS*2), "-b:v", fmt.Sprintf("%dk", k), "-maxrate", fmt.Sprintf("%dk", k*3/2), "-bufsize", fmt.Sprintf("%dk", k))
		if len(o.URL) > 6 && o.URL[:6] == "srt://" {
			a = append(a, "-f", "mpegts", o.URL)
		} else {
			a = append(a, "-f", "rtsp", "-rtsp_transport", "tcp", o.URL)
		}
	}
	return a
}

// Run keeps ffmpeg going until ctx ends.
func (e *Encoder) Run(ctx context.Context) {
	for ctx.Err() == nil {
		cmd := exec.CommandContext(ctx, e.FFmpeg, e.args()...)
		in, err := cmd.StdinPipe()
		if err == nil {
			err = cmd.Start()
		}
		if err != nil {
			e.Log.Error("video encoder did not start", "for", e.Name, "err", err)
			sleep(ctx, 5*time.Second)
			continue
		}
		e.mu.Lock()
		e.in = in
		e.mu.Unlock()
		err = cmd.Wait()
		e.mu.Lock()
		e.in = nil
		e.mu.Unlock()
		if ctx.Err() == nil {
			e.Log.Warn("video encoder stopped; restarting", "for", e.Name, "err", err)
			sleep(ctx, 2*time.Second)
		}
	}
}

// Frame sends one frame; dropped while ffmpeg (re)starts.
func (e *Encoder) Frame(img *image.RGBA) {
	e.mu.Lock()
	in := e.in
	e.mu.Unlock()
	if in != nil {
		in.Write(img.Pix)
	}
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
