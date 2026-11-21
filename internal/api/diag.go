package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"ptz/internal/video"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Diagnostics, for whoever is helping from elsewhere (over Tailscale or
// the venue network — trusted networks only, never a key):
//
//	GET  /api/diag                      everything at a glance: versions, health, devices, stage, recent problems
//	GET  /api/diag/logs?level=&after=&q=&limit=   recent log lines (memory)
//	GET  /api/diag/logs/file?name=&tail=          a log file from disk (survives restarts)
//	POST /api/diag/level {level}                  debug / info / warn / error, until restart
//	POST /api/diag/client {level, msg, stack, page, screen}   a browser's error, into the log
//	POST /api/diag/report {note, screen, page}    "something's wrong": saves a snapshot + recent log
//	GET  /api/diag/reports                        saved reports, newest first
//	GET  /api/diag/reports/{name}
func (s *Server) diagRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/diag", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, s.diagSnapshot(r))
	})
	mux.HandleFunc("GET /api/diag/video", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, s.videoStats())
	})
	mux.HandleFunc("GET /api/diag/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, s.healthSummary())
	})
	mux.HandleFunc("GET /api/diag/logs", s.diagLogs)
	mux.HandleFunc("GET /api/diag/logs/file", s.diagLogFile)
	mux.HandleFunc("POST /api/diag/level", func(w http.ResponseWriter, r *http.Request) {
		var b struct{ Level string }
		if !decode(w, r, &b) {
			return
		}
		if err := s.Logs.SetLevel(b.Level); err != nil {
			fail(w, 422, err.Error())
			return
		}
		s.Log.Info("log level changed", "level", s.Logs.Level(), "client", clientIP(r))
		writeJSON(w, 200, map[string]string{"level": s.Logs.Level()})
	})
	mux.HandleFunc("POST /api/diag/client", s.diagClient)
	mux.HandleFunc("POST /api/diag/report", s.diagReport)
	mux.HandleFunc("GET /api/diag/reports", s.diagReports)
	mux.HandleFunc("GET /api/diag/reports/{name}", s.diagReportGet)
}

func (s *Server) diagSnapshot(r *http.Request) map[string]any {
	set := s.Settings.Get()
	host, _ := os.Hostname()
	out := map[string]any{
		"version":  s.Version,
		"now":      time.Now().Format(time.RFC3339),
		"platform": runtime.GOOS + "/" + runtime.GOARCH,
		"host":     host,
		"mock":     s.Mock,
	}
	if s.Logs != nil {
		out["started"] = s.Logs.Started().Format(time.RFC3339)
		out["uptime"] = time.Since(s.Logs.Started()).Round(time.Second).String()
		out["logLevel"] = s.Logs.Level()
		out["logDir"] = s.Logs.Dir()
		out["logFiles"] = s.Logs.Files()
		out["problems"] = s.Logs.Query(0, slog.LevelWarn, "", 50)
	}

	cams := []map[string]any{}
	live := map[string]*struct{}{}
	for _, u := range s.Cams.List() {
		live[u.Cfg.ID] = &struct{}{}
		st := u.Svc.Snapshot()
		cams = append(cams, map[string]any{
			"id": u.Cfg.ID, "name": u.Cfg.Name, "host": u.Cfg.Host, "viscaPort": u.Cfg.ViscaPort, "httpPort": u.Cfg.HTTPPort,
			"main": u.Cfg.ID == set.MainID || (set.MainID == "" && len(cams) == 0), "video": u.Cfg.Video, "hasLogin": u.Cfg.User != "",
			"connected": st.Connected, "error": st.Error, "power": st.Power, "pan": st.Pan, "tilt": st.Tilt, "zoom": st.Zoom, "device": st.Device,
		})
	}
	for _, c := range set.Cameras {
		if live[c.ID] == nil {
			cams = append(cams, map[string]any{"id": c.ID, "name": c.Name, "host": c.Host, "running": false})
		}
	}
	out["cameras"] = cams
	out["health"] = s.healthSnapshot()

	d := s.Devs()
	dev := func(driver, label string, ready bool, err error) map[string]any {
		return map[string]any{"driver": driver, "label": label, "ready": ready, "error": errText(err)}
	}
	ctx, cancel := short(r)
	defer cancel()
	sw := s.switcherState(ctx, s.Devs())
	out["devices"] = map[string]any{
		"switcher":     dev(d.SwitcherCfg.Driver, d.SwitcherCfg.Label, d.Switcher != nil, d.SwitcherErr),
		"switcherNow":  map[string]any{"connected": sw["connected"], "error": sw["error"], "program": sw["active"], "preview": sw["preview"], "streaming": sw["streaming"], "recording": sw["recording"], "version": sw["version"]},
		"presentation": dev(d.PresentationCfg.Driver, d.PresentationCfg.Label, d.Presentation != nil, d.PresentationErr),
		"mixer":        dev(d.MixerCfg.Driver, d.MixerCfg.Label, d.Mixer != nil, d.MixerErr),
	}
	out["bandwidth"] = s.videoStats()
	if s.Video != nil {
		vs := s.Video.Status()
		out["video"] = map[string]any{"settings": set.Video.Redacted(), "state": vs.State, "error": vs.Error, "version": vs.Version, "paths": vs.Paths, "log": vs.Log}
	}
	out["system"] = map[string]any{"port": set.System.Port, "portLocked": s.PortLocked, "name": set.System.Name,
		"trustLan": set.System.TrustLAN, "trustedCidrs": set.System.TrustedCIDRs, "channel": set.System.Channel,
		"autoUpdate": set.System.AutoUpdate, "updateRepo": set.System.UpdateRepo, "hasToken": set.System.UpdateToken != "", "setupDone": set.SetupDone}
	if s.Updates != nil {
		out["update"] = s.Updates.Status()
	}
	if s.Stage != nil {
		chans := []map[string]any{}
		for _, c := range s.Stage.State(nil) {
			screens := []string{}
			for _, sc := range c.Screens {
				screens = append(screens, sc.Name)
			}
			var last any
			if n := len(c.Messages); n > 0 {
				m := c.Messages[n-1]
				last = map[string]any{"from": m.From, "text": m.Text, "at": m.At, "seen": m.Seen}
			}
			chans = append(chans, map[string]any{"id": c.ID, "name": c.Name, "online": screens, "messagesToday": len(c.Messages), "last": last, "timer": c.Timer})
		}
		out["stage"] = chans
	}
	return out
}

func (s *Server) diagLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	lv := slog.LevelDebug
	if l := q.Get("level"); l != "" {
		if err := lv.UnmarshalText([]byte(l)); err != nil {
			fail(w, 422, "level must be debug, info, warn or error")
			return
		}
	}
	after, _ := strconv.ParseUint(q.Get("after"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 4000 {
		limit = 500
	}
	writeJSON(w, 200, map[string]any{"level": s.Logs.Level(), "entries": s.Logs.Query(after, lv, q.Get("q"), limit)})
}

var logName = regexp.MustCompile(`^ptz(\.[1-9])?\.log$`)

func (s *Server) diagLogFile(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		name = "ptz.log"
	}
	if name == "helper.log" { // the updater's own log
		s.sendTail(w, r, filepath.Join(filepath.Dir(s.Settings.Path()), "updates", "helper.log"))
		return
	}
	if !logName.MatchString(name) || s.Logs.Dir() == "" {
		fail(w, 404, "log files are ptz.log, ptz.1.log … ptz.5.log, or helper.log")
		return
	}
	s.sendTail(w, r, filepath.Join(s.Logs.Dir(), name))
}

func (s *Server) sendTail(w http.ResponseWriter, r *http.Request, path string) {
	f, err := os.Open(path)
	if err != nil {
		fail(w, 404, "no such log file yet")
		return
	}
	defer f.Close()
	tail, _ := strconv.Atoi(r.URL.Query().Get("tail"))
	if tail <= 0 {
		tail = 1000
	}
	lines := make([]string, 0, tail)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		if len(lines) == tail {
			lines = lines[1:]
		}
		lines = append(lines, sc.Text())
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, strings.Join(lines, "\n"))
}

// ------------------------------------------------------- browser errors

var clientLimit = struct {
	sync.Mutex
	n map[string][]time.Time
}{n: map[string][]time.Time{}}

// diagClient logs a browser error (a page crash on the pulpit iPad is
// otherwise invisible). At most 20 a minute per address.
func (s *Server) diagClient(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Level, Msg, Stack, Page, Screen, Agent string
	}
	if !decode(w, r, &b) {
		return
	}
	ip := clientIP(r)
	clientLimit.Lock()
	now := time.Now()
	keep := clientLimit.n[ip][:0]
	for _, t := range clientLimit.n[ip] {
		if now.Sub(t) < time.Minute {
			keep = append(keep, t)
		}
	}
	over := len(keep) >= 20
	if !over {
		keep = append(keep, now)
	}
	clientLimit.n[ip] = keep
	clientLimit.Unlock()
	if over {
		w.WriteHeader(429)
		return
	}
	cut := func(s string, n int) string {
		if len(s) > n {
			return s[:n] + "…"
		}
		return s
	}
	attrs := []any{"msg", cut(b.Msg, 500), "page", cut(b.Page, 200), "screen", cut(b.Screen, 40), "client", ip}
	if b.Stack != "" {
		attrs = append(attrs, "stack", cut(b.Stack, 2000))
	}
	if b.Agent == "" {
		b.Agent = r.UserAgent()
	}
	attrs = append(attrs, "agent", cut(b.Agent, 200))
	if b.Level == "warn" || b.Level == "info" {
		s.Log.Warn("browser", attrs...)
	} else {
		s.Log.Error("browser", attrs...)
	}
	w.WriteHeader(204)
}

// ------------------------------------------------------------- reports

func (s *Server) reportDir() string {
	return filepath.Join(filepath.Dir(s.Settings.Path()), "reports")
}

var reportName = regexp.MustCompile(`^report-[0-9]{8}-[0-9]{6}(-[0-9]+)?\.json$`)

func (s *Server) diagReport(w http.ResponseWriter, r *http.Request) {
	var b struct{ Note, Screen, Page string }
	if !decode(w, r, &b) {
		return
	}
	if len(b.Note) > 2000 {
		b.Note = b.Note[:2000]
	}
	dir := s.reportDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail(w, 500, err.Error())
		return
	}
	now := time.Now()
	name := "report-" + now.Format("20060102-150405") + ".json"
	for i := 2; ; i++ {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			break
		}
		name = fmt.Sprintf("report-%s-%d.json", now.Format("20060102-150405"), i)
	}
	rep := map[string]any{
		"note": b.Note, "screen": b.Screen, "page": b.Page, "client": clientIP(r), "agent": r.UserAgent(), "at": now.Format(time.RFC3339),
		"snapshot": s.diagSnapshot(r),
	}
	if s.Logs != nil {
		rep["log"] = s.Logs.Query(0, slog.LevelDebug, "", 800)
	}
	data, _ := json.MarshalIndent(rep, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.Log.Warn("problem reported", "report", name, "note", b.Note, "screen", b.Screen, "client", clientIP(r))
	pruneReports(dir, 100)
	writeJSON(w, 201, map[string]string{"name": name})
}

func listReports(dir string) []string {
	es, _ := os.ReadDir(dir)
	out := []string{}
	for _, e := range es {
		if reportName.MatchString(e.Name()) {
			out = append(out, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out
}

func pruneReports(dir string, keep int) {
	l := listReports(dir)
	for i := keep; i < len(l); i++ {
		os.Remove(filepath.Join(dir, l[i]))
	}
}

func (s *Server) diagReports(w http.ResponseWriter, r *http.Request) {
	out := []map[string]any{}
	for _, n := range listReports(s.reportDir()) {
		item := map[string]any{"name": n}
		if b, err := os.ReadFile(filepath.Join(s.reportDir(), n)); err == nil {
			var h struct{ Note, Screen, At string }
			json.Unmarshal(b, &h)
			item["note"], item["screen"], item["at"] = h.Note, h.Screen, h.At
		}
		out = append(out, item)
	}
	writeJSON(w, 200, out)
}

func (s *Server) diagReportGet(w http.ResponseWriter, r *http.Request) {
	n := r.PathValue("name")
	if !reportName.MatchString(n) {
		fail(w, 404, "no such report")
		return
	}
	b, err := os.ReadFile(filepath.Join(s.reportDir(), n))
	if err != nil {
		fail(w, 404, "no such report")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

// videoStats is the gateway's live bandwidth with friendly feed names.
func (s *Server) videoStats() map[string]any {
	out := map[string]any{"running": false, "feeds": []any{}, "viewers": []any{}, "senders": []any{}}
	if s.Video == nil {
		return out
	}
	st := s.Video.Stats()
	names := map[string]string{"program": "Program (vMix)", "confidence": "Confidence"}
	for _, u := range s.Cams.List() {
		names["cam-"+u.Cfg.ID] = u.Cfg.Name + " · HD"
		names["cam-"+u.Cfg.ID+"-sd"] = u.Cfg.Name + " · SD"
	}
	feeds := []map[string]any{}
	for _, f := range st.Feeds {
		n := names[f.Path]
		if n == "" {
			n = f.Path
		}
		feeds = append(feeds, map[string]any{"path": f.Path, "name": n, "online": f.Online, "source": f.Source, "codec": f.Codec,
			"width": f.Width, "height": f.Height, "inKbps": f.InKbps, "outKbps": f.OutKbps, "viewers": f.Viewers})
	}
	viewers := []map[string]any{}
	for _, v := range st.Viewers {
		n := names[v.Path]
		if n == "" {
			n = v.Path
		}
		viewers = append(viewers, map[string]any{"kind": v.Kind, "feed": n, "path": v.Path, "remote": v.Remote, "kbps": v.Kbps,
			"since": v.Since, "lost": v.Lost, "connect": v.Connect})
	}
	out["running"], out["at"], out["inKbps"], out["outKbps"] = st.Running, st.At, st.InKbps, st.OutKbps
	senders := st.Senders
	if senders == nil {
		senders = []video.SenderStat{}
	}
	out["feeds"], out["viewers"], out["senders"] = feeds, viewers, senders
	return out
}

// healthSummary is the small, cheap view a dashboard widget polls.
func (s *Server) healthSummary() map[string]any {
	out := map[string]any{"version": s.Version, "health": s.healthSnapshot()}
	if s.Logs != nil {
		out["uptime"] = time.Since(s.Logs.Started()).Round(time.Second).String()
		since := time.Now().Add(-time.Hour)
		n, last := 0, any(nil)
		for _, e := range s.Logs.Query(0, slog.LevelWarn, "", 0) {
			if e.Time.After(since) {
				n++
				last = map[string]any{"at": e.Time, "level": e.Level, "msg": e.Msg, "attrs": e.Attrs}
			}
		}
		out["problemsLastHour"], out["lastProblem"] = n, last
	}
	if s.Video != nil {
		st := s.Video.Status()
		out["video"] = map[string]any{"state": st.State, "error": st.Error}
	}
	if s.Stage != nil {
		screens := map[string]bool{}
		for _, c := range s.Stage.State(nil) {
			for _, sc := range c.Screens {
				screens[sc.Name] = true
			}
		}
		l := []string{}
		for n := range screens {
			l = append(l, n)
		}
		sort.Strings(l)
		out["stageScreens"] = l
	}
	return out
}
