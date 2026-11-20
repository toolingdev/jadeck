package cams

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"ptz/internal/digest"
	"ptz/internal/settings"
	"ptz/internal/visca"
)

// ProbeResult is what the Add camera form's Test button shows.
type ProbeResult struct {
	OK           bool   `json:"ok"`
	Message      string `json:"message"`
	Control      bool   `json:"control"` // VISCA answered
	ControlError string `json:"controlError,omitempty"`
	Picture      bool   `json:"picture"` // snapshot.jpg came back
	PictureError string `json:"pictureError,omitempty"`
	Login        string `json:"login"` // ok | none | needed | rejected | unknown
	Model        string `json:"model,omitempty"`
	Firmware     string `json:"firmware,omitempty"`
	Snapshot     string `json:"snapshot,omitempty"` // data: URL
}

var confRe = regexp.MustCompile(`(\w+)\s*=\s*"([^"]*)"`)

func httpClient(c settings.Camera, timeout time.Duration) *http.Client {
	var rt http.RoundTripper = &http.Transport{DialContext: (&net.Dialer{Timeout: timeout}).DialContext,
		ResponseHeaderTimeout: timeout}
	if c.User != "" {
		rt = &digest.Transport{User: c.User, Pass: c.Pass, Base: rt}
	}
	return &http.Client{Transport: rt, Timeout: timeout + time.Second}
}

func get(ctx context.Context, cl *http.Client, url string, limit int64) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, err
	}
	resp, err := cl.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	return resp.StatusCode, b, err
}

// viscaAnswers dials the control port and asks the power state.
func viscaAnswers(ctx context.Context, addr string, timeout time.Duration) error {
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(timeout))
	if _, err := conn.Write(visca.InqPower); err != nil {
		return err
	}
	buf := make([]byte, 0, 64)
	tmp := make([]byte, 64)
	for {
		n, err := conn.Read(tmp)
		if n > 0 {
			frames, rest := visca.SplitFrames(append(buf, tmp[:n]...))
			buf = append(buf[:0], rest...)
			for _, f := range frames {
				if visca.FrameKind(f) == visca.KindReply {
					return nil
				}
			}
		}
		if err != nil {
			return errors.New("connected, but no VISCA answer — is this the camera's control port?")
		}
	}
}

func friendly(err error) string {
	var ne net.Error
	switch {
	case err == nil:
		return ""
	case errors.As(err, &ne) && ne.Timeout():
		return "no answer (powered off, or wrong IP?)"
	case strings.Contains(err.Error(), "refused"):
		return "the camera refused the connection on that port"
	case strings.Contains(err.Error(), "no route"), strings.Contains(err.Error(), "unreachable"):
		return "that address isn't reachable from this PC"
	}
	return err.Error()
}

// Probe tests a camera before it is saved: control, picture, login.
func Probe(ctx context.Context, c settings.Camera) ProbeResult {
	c.Defaults()
	var r ProbeResult
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		err := viscaAnswers(ctx, net.JoinHostPort(c.Host, fmt.Sprint(c.ViscaPort)), 3*time.Second)
		r.Control = err == nil
		r.ControlError = friendly(err)
	}()
	go func() {
		defer wg.Done()
		cl := httpClient(c, 4*time.Second)
		code, body, err := get(ctx, cl, c.HTTPBase()+"/cgi-bin/param.cgi?get_device_conf", 64<<10)
		switch {
		case err != nil:
			r.Login = "unknown"
		case code == 401 && c.User == "":
			r.Login = "needed"
		case code == 401:
			r.Login = "rejected"
		case code == 200:
			r.Login = "none"
			if c.User != "" {
				r.Login = "ok"
			}
			dev := map[string]string{}
			for _, m := range confRe.FindAllSubmatch(body, -1) {
				dev[string(m[1])] = string(m[2])
			}
			r.Model = dev["devname"]
			r.Firmware = firmware(dev["versioninfo"])
		default:
			r.Login = "unknown"
		}
		code, img, err := get(ctx, cl, c.HTTPBase()+"/snapshot.jpg", 8<<20)
		switch {
		case err != nil:
			r.PictureError = friendly(err)
		case code == 401:
			r.PictureError = "the camera wants a login for its picture"
		case code != 200 || len(img) < 4 || img[0] != 0xFF || img[1] != 0xD8:
			r.PictureError = fmt.Sprintf("no picture (HTTP %d)", code)
		default:
			r.Picture = true
			r.Snapshot = "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(img)
		}
	}()
	wg.Wait()
	r.OK = r.Control && r.Login != "needed" && r.Login != "rejected"
	switch {
	case !r.Control && !r.Picture && r.Login == "unknown":
		r.Message = "Nothing answered at " + c.Host + ". Check the IP, and that the camera is on."
	case r.Login == "needed":
		r.Message = "The camera wants a login. Type its user name and password."
	case r.Login == "rejected":
		r.Message = "The camera rejected that user name or password."
	case !r.Control:
		r.Message = "The camera's web page answered, but control (VISCA) didn't: " + r.ControlError
	case !r.Picture:
		r.Message = "Control works. No picture yet: " + r.PictureError
	default:
		r.Message = "Camera answered"
	}
	return r
}

var fwRe = regexp.MustCompile(`v?(\d+\.\d+\.\d+)`)

func firmware(v string) string {
	if m := fwRe.FindStringSubmatch(v); m != nil {
		return m[1]
	}
	return v
}

// Found is one camera the network scan saw.
type Found struct {
	Host      string `json:"host"`
	ViscaPort int    `json:"viscaPort"`
	HTTPPort  int    `json:"httpPort"`
	Model     string `json:"model,omitempty"`
	Login     bool   `json:"login"` // needs a login
	Known     string `json:"known"` // name of the saved camera at this address
}

// Candidate is an address to try (the scan builds them from this PC's
// networks; mock mode adds the simulated cameras).
type Candidate struct {
	Host      string
	ViscaPort int
	HTTPPort  int
}

// LocalCandidates lists every address on this PC's private IPv4
// networks (up to /22 each), at the standard PTZOptics ports.
func LocalCandidates() []Candidate {
	var out []Candidate
	seen := map[string]bool{}
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipn.IP.To4()
			if ip4 == nil || !ip4.IsPrivate() {
				continue
			}
			ones, _ := ipn.Mask.Size()
			if ones < 22 {
				ones = 24 // big networks: scan our own /24 only
			}
			mask := net.CIDRMask(ones, 32)
			base := ip4.Mask(mask)
			n := 1 << (32 - ones)
			for i := 1; i < n-1; i++ {
				v := uint32(base[0])<<24 | uint32(base[1])<<16 | uint32(base[2])<<8 | uint32(base[3])
				v += uint32(i)
				ip := net.IPv4(byte(v>>24), byte(v>>16), byte(v>>8), byte(v)).String()
				if ip == ip4.String() || seen[ip] {
					continue
				}
				seen[ip] = true
				out = append(out, Candidate{Host: ip, ViscaPort: 5678, HTTPPort: 80})
			}
		}
	}
	return out
}

// Scan tries every candidate's control port and identifies what answers.
func Scan(ctx context.Context, cands []Candidate) []Found {
	var mu sync.Mutex
	var out []Found
	sem := make(chan struct{}, 128)
	var wg sync.WaitGroup
	for _, c := range cands {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			d := net.Dialer{Timeout: 600 * time.Millisecond}
			conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(c.Host, fmt.Sprint(c.ViscaPort)))
			if err != nil {
				return
			}
			conn.Close()
			f := Found{Host: c.Host, ViscaPort: c.ViscaPort, HTTPPort: c.HTTPPort}
			cam := settings.Camera{Host: c.Host, HTTPPort: c.HTTPPort}
			code, body, err := get(ctx, httpClient(cam, 2*time.Second), cam.HTTPBase()+"/cgi-bin/param.cgi?get_device_conf", 64<<10)
			if err == nil {
				f.Login = code == 401
				if m := regexp.MustCompile(`devname\s*=\s*"([^"]*)"`).FindSubmatch(body); m != nil {
					f.Model = string(m[1])
				}
			}
			mu.Lock()
			out = append(out, f)
			mu.Unlock()
		}()
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool {
		a, b := net.ParseIP(out[i].Host).To4(), net.ParseIP(out[j].Host).To4()
		if a != nil && b != nil && !a.Equal(b) {
			return string(a) < string(b)
		}
		return out[i].ViscaPort < out[j].ViscaPort
	})
	return out
}
