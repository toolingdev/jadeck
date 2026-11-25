// Command lagprobe measures how late a PTZOptics camera reacts, for
// tuning auto-tracking: it drives the camera left and right over VISCA
// and times, against the moment each command was sent,
//
//   - when the camera's reported position starts and stops changing
//     (command latency, stop coast), from fast pan/tilt inquiries;
//   - when motion starts and stops in the camera's video (what a tracker
//     reading that feed would see), from frame differences of the RTSP
//     stream decoded by ffmpeg;
//   - how fast each pan speed turns (degrees per second).
//
// It moves the camera: never run it on a camera that is on air. The
// camera goes back to where it started when the probe ends.
//
//	lagprobe -host 192.168.1.88 [-rtsp rtsp://192.168.1.88:554/1] [-speeds 3,8,14] [-runs 3] [-out lag.json] -yes
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"ptz/internal/visca"
)

// ------------------------------------------------------------------ VISCA

type cam struct {
	c       net.Conn
	wmu     sync.Mutex
	imu     sync.Mutex // one inquiry at a time
	replies chan []byte
}

func dial(addr string) (*cam, error) {
	c, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return nil, err
	}
	k := &cam{c: c, replies: make(chan []byte, 8)}
	go k.read()
	return k, nil
}

func (k *cam) read() {
	buf := make([]byte, 0, 256)
	tmp := make([]byte, 256)
	for {
		n, err := k.c.Read(tmp)
		if err != nil {
			close(k.replies)
			return
		}
		var frames [][]byte
		frames, buf = visca.SplitFrames(append(buf, tmp[:n]...))
		for _, f := range frames {
			switch visca.FrameKind(f) {
			case visca.KindReply:
				select {
				case k.replies <- f:
				default:
				}
			case visca.KindError:
				fmt.Fprintf(os.Stderr, "camera error reply % X\n", f)
			}
		}
	}
}

func (k *cam) send(f []byte) error {
	k.wmu.Lock()
	defer k.wmu.Unlock()
	_, err := k.c.Write(f)
	return err
}

// pos asks for pan/tilt; at is the midpoint of the round trip.
func (k *cam) pos() (pan, tilt int, at time.Time, rtt time.Duration, err error) {
	k.imu.Lock()
	defer k.imu.Unlock()
	for len(k.replies) > 0 { // stale replies from a timed-out ask
		<-k.replies
	}
	t0 := time.Now()
	if err = k.send(visca.InqPanTiltPos); err != nil {
		return
	}
	for {
		select {
		case f, ok := <-k.replies:
			if !ok {
				err = errors.New("camera closed the connection")
				return
			}
			p, t, ok2 := visca.ParsePanTiltPos(f)
			if !ok2 {
				continue
			}
			t1 := time.Now()
			return p, t, t0.Add(t1.Sub(t0) / 2), t1.Sub(t0), nil
		case <-time.After(700 * time.Millisecond):
			err = errors.New("no answer to the position inquiry")
			return
		}
	}
}

func (k *cam) zoom() (int, bool) {
	k.imu.Lock()
	defer k.imu.Unlock()
	for len(k.replies) > 0 {
		<-k.replies
	}
	if k.send(visca.InqZoomPos) != nil {
		return 0, false
	}
	select {
	case f := <-k.replies:
		return visca.ParseZoomPos(f)
	case <-time.After(700 * time.Millisecond):
		return 0, false
	}
}

// ---------------------------------------------------------------- samples

type posSample struct {
	t    time.Time
	pan  int
	tilt int
	rtt  time.Duration
}

type vidSample struct {
	t    time.Time
	diff float64 // mean absolute difference to the previous frame, 0..255
}

type recorder struct {
	mu   sync.Mutex
	pos  []posSample
	vid  []vidSample
	verr error
}

func (r *recorder) pollPos(ctx context.Context, k *cam) {
	for ctx.Err() == nil {
		p, t, at, rtt, err := k.pos()
		if err != nil {
			fmt.Fprintln(os.Stderr, "position:", err)
			time.Sleep(50 * time.Millisecond)
			continue
		}
		r.mu.Lock()
		r.pos = append(r.pos, posSample{at, p, t, rtt})
		r.mu.Unlock()
	}
}

const vw, vh = 160, 90

func (r *recorder) readVideo(ctx context.Context, ffmpeg, url string) {
	args := []string{"-hide_banner", "-loglevel", "error",
		"-fflags", "nobuffer", "-flags", "low_delay", "-probesize", "500000", "-analyzeduration", "500000"}
	if strings.HasPrefix(url, "rtsp://") {
		args = append(args, "-rtsp_transport", "tcp")
	}
	args = append(args, "-i", url, "-an",
		"-vf", fmt.Sprintf("scale=%d:%d,format=gray", vw, vh),
		"-f", "rawvideo", "-")
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	cmd.Stderr = os.Stderr
	out, err := cmd.StdoutPipe()
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		r.mu.Lock()
		r.verr = err
		r.mu.Unlock()
		return
	}
	br := bufio.NewReaderSize(out, vw*vh*4)
	prev := make([]byte, vw*vh)
	cur := make([]byte, vw*vh)
	first := true
	for {
		if _, err := io.ReadFull(br, cur); err != nil {
			if ctx.Err() == nil {
				r.mu.Lock()
				r.verr = fmt.Errorf("video ended: %v", err)
				r.mu.Unlock()
			}
			_ = cmd.Wait()
			return
		}
		now := time.Now()
		if !first {
			var s int
			for i := range cur {
				d := int(cur[i]) - int(prev[i])
				if d < 0 {
					d = -d
				}
				s += d
			}
			r.mu.Lock()
			r.vid = append(r.vid, vidSample{now, float64(s) / float64(len(cur))})
			r.mu.Unlock()
		}
		first = false
		prev, cur = cur, prev
	}
}

func (r *recorder) snapshot() ([]posSample, []vidSample) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]posSample(nil), r.pos...), append([]vidSample(nil), r.vid...)
}

// ---------------------------------------------------------------- one run

type runResult struct {
	Speed     int     `json:"speed"`
	Dir       int     `json:"dir"`
	PosStart  float64 `json:"posStartMs"`            // command → position starts changing
	PosStop   float64 `json:"posStopMs"`             // stop command → position stops changing
	CoastDeg  float64 `json:"coastDeg"`              // degrees turned after the stop command
	DegPerSec float64 `json:"degPerSec"`             // steady pan speed
	VidStart  float64 `json:"vidStartMs,omitempty"`  // command → motion seen in the video
	VidStop   float64 `json:"vidStopMs,omitempty"`   // stop command → video still again
	VidDelay  float64 `json:"videoDelayMs,omitempty"` // video start − position start
	Notes     string  `json:"notes,omitempty"`
}

func ms(d time.Duration) float64 { return math.Round(float64(d)/1e5) / 10 }

func panAt(ps []posSample, t time.Time) (int, bool) {
	v, ok := 0, false
	for _, s := range ps {
		if s.t.After(t) {
			break
		}
		v, ok = s.pan, true
	}
	return v, ok
}

func analyze(ps []posSample, vs []vidSample, t0, t1, end time.Time, speed, dir int) runResult {
	res := runResult{Speed: speed, Dir: dir}
	var notes []string

	// ---- position
	base, ok := panAt(ps, t0)
	if !ok {
		res.Notes = "no position sample before the command"
		return res
	}
	var startAt time.Time
	for _, s := range ps {
		if s.t.After(t0) && abs(s.pan-base) >= 2 {
			startAt = s.t
			break
		}
	}
	if startAt.IsZero() {
		notes = append(notes, "position never changed")
	} else {
		res.PosStart = ms(startAt.Sub(t0))
	}
	// steady speed: least-squares slope over the middle of the drive
	var xs, ys []float64
	from := startAt.Add(300 * time.Millisecond)
	for _, s := range ps {
		if !startAt.IsZero() && s.t.After(from) && s.t.Before(t1) {
			xs = append(xs, s.t.Sub(t0).Seconds())
			ys = append(ys, float64(s.pan))
		}
	}
	if len(xs) >= 3 {
		res.DegPerSec = math.Round(math.Abs(slope(xs, ys))/visca.UnitsPerDegree*100) / 100
	} else if !startAt.IsZero() {
		notes = append(notes, "drive too short for a speed reading")
	}
	atStop, _ := panAt(ps, t1)
	var after []posSample
	for _, s := range ps {
		if s.t.After(t1) && s.t.Before(end) {
			after = append(after, s)
		}
	}
	if len(after) > 0 {
		final := after[len(after)-1].pan
		last := t1
		for i := 1; i < len(after); i++ {
			if after[i].pan != after[i-1].pan {
				last = after[i].t
			}
		}
		res.PosStop = ms(last.Sub(t1))
		res.CoastDeg = math.Round(float64(abs(final-atStop))/visca.UnitsPerDegree*100) / 100
	}

	// ---- video
	if len(vs) > 0 {
		var bl []float64
		for _, v := range vs {
			if v.t.After(t0.Add(-1200*time.Millisecond)) && v.t.Before(t0) {
				bl = append(bl, v.diff)
			}
		}
		if len(bl) < 3 {
			notes = append(notes, "too few still frames for a video baseline")
		} else {
			m, sd := meanSD(bl)
			thr := math.Max(m+6*sd, m*2+0.4)
			var vStart time.Time
			for i, v := range vs {
				if v.t.After(t0) && v.diff > thr && i+1 < len(vs) && vs[i+1].diff > thr {
					vStart = v.t
					break
				}
			}
			if vStart.IsZero() {
				notes = append(notes, fmt.Sprintf("no motion seen in the video (threshold %.2f)", thr))
			} else {
				res.VidStart = ms(vStart.Sub(t0))
				if !startAt.IsZero() {
					res.VidDelay = ms(vStart.Sub(startAt))
				}
				// still again: first frame after the stop with three quiet frames from it
				for i, v := range vs {
					if !v.t.After(t1) || v.t.After(end) || i+2 >= len(vs) {
						continue
					}
					if v.diff <= thr && vs[i+1].diff <= thr && vs[i+2].diff <= thr {
						res.VidStop = ms(v.t.Sub(t1))
						break
					}
				}
			}
		}
	}
	res.Notes = strings.Join(notes, "; ")
	return res
}

// ------------------------------------------------------------------- main

type report struct {
	Camera       string      `json:"camera"`
	Video        string      `json:"video,omitempty"`
	When         time.Time   `json:"when"`
	ZoomPos      int         `json:"zoomPos"`
	HFOVDeg      float64     `json:"hfovDeg"`
	InquiryRTTMs float64     `json:"inquiryRttMs"` // median; position times are ± half of it
	FrameMs      float64     `json:"frameMs,omitempty"`
	Runs         []runResult `json:"runs"`
}

func main() {
	host := flag.String("host", "", "camera address")
	port := flag.Int("visca", 5678, "camera VISCA TCP port")
	rtsp := flag.String("rtsp", "", "camera video to watch (default rtsp://<host>:554/1; \"off\" = position only)")
	ffmpeg := flag.String("ffmpeg", "ffmpeg", "ffmpeg binary")
	speedsF := flag.String("speeds", "3,8,14", "pan speeds to test (1-24)")
	runs := flag.Int("runs", 3, "runs per speed (direction alternates)")
	drive := flag.Duration("drive", 1500*time.Millisecond, "how long each move lasts")
	settle := flag.Duration("settle", 2*time.Second, "wait after each stop")
	outF := flag.String("out", "", "write the report as JSON here")
	yes := flag.Bool("yes", false, "confirm the camera may move (it is not on air)")
	flag.Parse()
	if *host == "" {
		flag.Usage()
		os.Exit(2)
	}
	if !*yes {
		fmt.Fprintln(os.Stderr, "lagprobe moves the camera. Make sure it is not on air, then run again with -yes.")
		os.Exit(2)
	}
	var speeds []int
	for _, s := range strings.Split(*speedsF, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil || n < 1 || n > 24 {
			fmt.Fprintln(os.Stderr, "bad speed:", s)
			os.Exit(2)
		}
		speeds = append(speeds, n)
	}
	video := *rtsp
	if video == "" {
		video = fmt.Sprintf("rtsp://%s:554/1", *host)
	}
	if video == "off" {
		video = ""
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	k, err := dial(fmt.Sprintf("%s:%d", *host, *port))
	if err != nil {
		fmt.Fprintln(os.Stderr, "camera:", err)
		os.Exit(1)
	}
	homePan, homeTilt, _, _, err := k.pos()
	if err != nil {
		fmt.Fprintln(os.Stderr, "camera:", err)
		os.Exit(1)
	}
	z, _ := k.zoom()
	rep := report{Camera: *host, Video: video, When: time.Now(), ZoomPos: z,
		HFOVDeg: math.Round(visca.HFOVDegrees(z)*10) / 10}
	restore := func() {
		_ = k.send(visca.PanTiltStop())
		time.Sleep(200 * time.Millisecond)
		_ = k.send(visca.PanTiltAbsolute(homePan, homeTilt, 12, 10))
	}
	defer restore()

	rec := &recorder{}
	pctx, pcancel := context.WithCancel(ctx)
	defer pcancel()
	go rec.pollPos(pctx, k)
	if video != "" {
		go rec.readVideo(pctx, *ffmpeg, video)
		// wait for frames
		deadline := time.Now().Add(10 * time.Second)
		for {
			_, vs := rec.snapshot()
			rec.mu.Lock()
			verr := rec.verr
			rec.mu.Unlock()
			if verr != nil {
				fmt.Fprintln(os.Stderr, "video:", verr, "(carrying on with position only)")
				video = ""
				rep.Video = ""
				break
			}
			if len(vs) >= 15 {
				break
			}
			if time.Now().After(deadline) {
				fmt.Fprintln(os.Stderr, "video: no frames in 10 s (carrying on with position only)")
				video = ""
				rep.Video = ""
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	fmt.Printf("camera %s  zoom %d (≈%.1f° wide)  video %s\n", *host, z, rep.HFOVDeg, orDash(video))
	dir := 1
	for _, sp := range speeds {
		for i := 0; i < *runs && ctx.Err() == nil; i++ {
			time.Sleep(1300 * time.Millisecond) // still baseline
			t0 := time.Now()
			_ = k.send(visca.PanTiltDrive(dir, 0, sp, 1))
			sleepCtx(ctx, *drive)
			t1 := time.Now()
			_ = k.send(visca.PanTiltStop())
			sleepCtx(ctx, *settle)
			end := time.Now()
			ps, vs := rec.snapshot()
			if video == "" {
				vs = nil
			}
			r := analyze(ps, vs, t0, t1, end, sp, dir)
			rep.Runs = append(rep.Runs, r)
			printRun(r)
			dir = -dir
		}
	}
	pcancel()
	ps, vs := rec.snapshot()
	var rtts []float64
	for _, s := range ps {
		rtts = append(rtts, ms(s.rtt))
	}
	rep.InquiryRTTMs = median(rtts)
	if len(vs) > 1 {
		rep.FrameMs = ms(vs[len(vs)-1].t.Sub(vs[0].t) / time.Duration(len(vs)-1))
	}
	printSummary(rep)
	if *outF != "" {
		b, _ := json.MarshalIndent(rep, "", "  ")
		if err := os.WriteFile(*outF, b, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
		} else {
			fmt.Println("report:", *outF)
		}
	}
}

func printRun(r runResult) {
	d := "right"
	if r.Dir < 0 {
		d = "left "
	}
	line := fmt.Sprintf("speed %2d %s  moves after %4.0f ms  %5.1f°/s  stops after %4.0f ms (coast %.2f°)",
		r.Speed, d, r.PosStart, r.DegPerSec, r.PosStop, r.CoastDeg)
	if r.VidStart > 0 {
		line += fmt.Sprintf("  | video moves after %4.0f ms (+%.0f), still after %4.0f ms", r.VidStart, r.VidDelay, r.VidStop)
	}
	if r.Notes != "" {
		line += "  [" + r.Notes + "]"
	}
	fmt.Println(line)
}

func printSummary(rep report) {
	fmt.Printf("\ninquiry round trip %.0f ms (position times ±%.0f ms)", rep.InquiryRTTMs, rep.InquiryRTTMs/2)
	if rep.FrameMs > 0 {
		fmt.Printf(", video frame every %.0f ms", rep.FrameMs)
	}
	fmt.Println()
	by := map[int][]runResult{}
	var keys []int
	for _, r := range rep.Runs {
		if _, ok := by[r.Speed]; !ok {
			keys = append(keys, r.Speed)
		}
		by[r.Speed] = append(by[r.Speed], r)
	}
	sort.Ints(keys)
	fmt.Println("speed  °/s    move-lag  stop-lag  coast°  video-lag  video-stop")
	for _, k := range keys {
		var a, b, c, d, e, f []float64
		for _, r := range by[k] {
			a = append(a, r.DegPerSec)
			b = append(b, r.PosStart)
			c = append(c, r.PosStop)
			d = append(d, r.CoastDeg)
			if r.VidStart > 0 {
				e = append(e, r.VidStart)
				f = append(f, r.VidStop)
			}
		}
		fmt.Printf("%5d  %5.1f  %6.0f ms %6.0f ms  %5.2f  %7s   %7s\n", k, median(a), median(b), median(c), median(d), msOr(e), msOr(f))
	}
}

// ---------------------------------------------------------------- helpers

func msOr(v []float64) string {
	if len(v) == 0 {
		return "-"
	}
	return fmt.Sprintf("%.0f ms", median(v))
}

func orDash(s string) string {
	if s == "" {
		return "off"
	}
	return s
}

func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func meanSD(v []float64) (float64, float64) {
	var s float64
	for _, x := range v {
		s += x
	}
	m := s / float64(len(v))
	var q float64
	for _, x := range v {
		q += (x - m) * (x - m)
	}
	return m, math.Sqrt(q / float64(len(v)))
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	c := append([]float64(nil), v...)
	sort.Float64s(c)
	return c[len(c)/2]
}

func slope(xs, ys []float64) float64 {
	mx, _ := meanSD(xs)
	my, _ := meanSD(ys)
	var num, den float64
	for i := range xs {
		num += (xs[i] - mx) * (ys[i] - my)
		den += (xs[i] - mx) * (xs[i] - mx)
	}
	if den == 0 {
		return 0
	}
	return num / den
}
