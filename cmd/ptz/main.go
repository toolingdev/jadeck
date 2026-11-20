// Command ptz is the PTZ web controller: one binary that owns the camera
// and the other devices and serves the web UI to every phone, tablet
// and PC on the network.
//
//	ptz [run] [--mock] [--env FILE]     run in the foreground
//	ptz service install|uninstall|start|stop|restart|status
//	ptz version
package main

import (
	"context"
	"crypto/ed25519"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kardianos/service"

	"ptz/internal/api"
	"ptz/internal/auth"
	"ptz/internal/cams"
	"ptz/internal/config"
	"ptz/internal/dashboards"
	"ptz/internal/devices"
	"ptz/internal/logx"
	"ptz/internal/macros"
	"ptz/internal/mockcam"
	"ptz/internal/netinfo"
	"ptz/internal/schedule"
	"ptz/internal/settings"
	"ptz/internal/stage"
	"ptz/internal/svc"
	"ptz/internal/update"
	"ptz/internal/video"
	"ptz/web"

	// Add-ons (vMix, Behringer WING, Proclaim, ...) are separate projects;
	// release builds bundle their signed packages (-tags addonbundle).
	"ptz/internal/addons"
)

// Stamped at build time:
//
//	-ldflags "-X main.Version=v1.2.3 -X main.UpdatePublicKey=<base64> -X main.DefaultRepo=owner/name"
var (
	Version         = "dev"
	UpdatePublicKey = ""
	DefaultRepo     = "" // owner/name the release was built from: updates work out of the box
)

func main() {
	update.ServiceControl = svc.Control
	args := os.Args[1:]
	cmd := "run"
	if len(args) > 0 && (args[0] == "run" || args[0] == "service" || args[0] == "version" || args[0] == "_apply") {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "version":
		fmt.Println(Version)
	case "_apply":
		if err := update.RunHelper(args); err != nil {
			fmt.Fprintln(os.Stderr, "update helper:", err)
			os.Exit(1)
		}
	case "service":
		serviceCmd(args)
	default:
		runCmd(args)
	}
}

func serviceCmd(args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: ptz service install|uninstall|start|stop|restart|status")
		os.Exit(2)
	}
	if args[0] == "status" {
		st, err := svc.Status()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(st)
		return
	}
	if err := svc.Control(args[0], []string{"run"}); err != nil {
		fmt.Fprintln(os.Stderr, "service", args[0]+":", err)
		os.Exit(1)
	}
	fmt.Println("service", args[0], "ok")
}

type flags struct {
	mock    bool
	envFile string
}

func runCmd(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	var f flags
	fs.BoolVar(&f.mock, "mock", false, "run against the built-in mock camera and simulated devices")
	fs.StringVar(&f.envFile, "env", "", "env file with PTZ_* settings (default: ptz.env beside the binary, or $PTZ_ENV_FILE)")
	fs.Parse(args)
	log := logs.Logger()

	if !svc.Interactive() {
		// Started by the OS service manager.
		p := &program{f: f, log: log}
		s, err := service.New(p, svc.Config([]string{"run"}))
		if err != nil {
			log.Error("service", "err", err)
			os.Exit(1)
		}
		if err := s.Run(); err != nil {
			log.Error("service run", "err", err)
			os.Exit(1)
		}
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, stop, f, "exec", os.Args[1:], log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

// logs is the one log for the process: console, rotating files in the
// data folder (attached once it is known), and the in-memory ring the
// diagnostics API serves.
var logs = logx.New(os.Stderr)

type program struct {
	f      flags
	log    *slog.Logger
	cancel context.CancelFunc
	done   chan struct{}
}

func (p *program) Start(service.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel, p.done = cancel, make(chan struct{})
	go func() {
		defer close(p.done)
		if err := run(ctx, cancel, p.f, "service", nil, p.log); err != nil {
			p.log.Error("fatal", "err", err)
			os.Exit(1) // the service manager restarts us
		}
	}()
	return nil
}

func (p *program) Stop(service.Service) error {
	p.cancel()
	select {
	case <-p.done:
	case <-time.After(10 * time.Second):
	}
	return nil
}

// mockDevices is the simulated rig for --mock: every device kind,
// generic favorite labels.
func mockDevices() *devices.File {
	return &devices.File{Version: 2, Connections: []devices.Connection{
		{ID: "sim-switcher", Addon: "simulated", Type: "sim-switcher", Name: "Simulated switcher"},
		{ID: "sim-slides", Addon: "simulated", Type: "sim-slides", Name: "Simulated slides"},
		{ID: "sim-mixer", Addon: "simulated", Type: "sim-mixer", Name: "Simulated mixer", Favorites: mockFavorites},
	}}
}

var mockFavorites = []devices.Favorite{{Label: "Main", Channel: 1}, {Label: "Lectern", Channel: 2}, {Label: "Wireless 1", Channel: 3}, {Label: "Wireless 2", Channel: 4},
	{Label: "Vocals 1", Channel: 7}, {Label: "Vocals 2", Channel: 8}, {Label: "Guitar", Channel: 10}, {Label: "Audience", Channel: 6}}

// seedSettings is the first-run settings: an existing env file (the
// Python build's, or ptz.env) carries over, everything else defaults.
func seedSettings(cfg config.Config) settings.Settings {
	s := settings.Settings{
		Video: settings.Video{Managed: true},
		System: settings.System{Port: cfg.BindPort, Name: "ptz", TrustLAN: os.Getenv("PTZ_TRUST_LAN") != "0",
			UpdateRepo: firstNonEmpty(cfg.UpdateRepo, DefaultRepo), UpdateToken: cfg.UpdateToken, Channel: "stable"},
	}
	for _, c := range strings.Split(os.Getenv("PTZ_TRUSTED_CIDRS"), ",") {
		if c = strings.TrimSpace(c); c != "" {
			s.System.TrustedCIDRs = append(s.System.TrustedCIDRs, c)
		}
	}
	fresh := cfg.Mock && os.Getenv("PTZ_MOCK_FRESH") == "1" // try the first-run wizard
	if (os.Getenv("PTZ_CAMERA_HOST") != "" || cfg.Mock) && !fresh {
		c := settings.Camera{ID: settings.NewID(), Name: "Sanctuary", Host: cfg.CameraHost, ViscaPort: cfg.CameraPort,
			User: cfg.CameraUser, Pass: cfg.CameraPass, Video: true, Presets: "presets.db"}
		if cfg.Mock {
			c.Name = "Sanctuary (simulated)"
		}
		if u, err := url.Parse(cfg.CameraHTTP); err == nil {
			if p, err := strconv.Atoi(u.Port()); err == nil {
				c.HTTPPort = p
			}
		}
		if cfg.DBPath != filepath.Join(cfg.DataDir, "presets.db") {
			c.Presets = cfg.DBPath
		}
		c.Defaults()
		s.Cameras, s.MainID, s.SetupDone = []settings.Camera{c}, c.ID, true
	}
	return s
}

// httpServer serves the app and can move to another port live.
type httpServer struct {
	host    string
	handler http.Handler
	log     *slog.Logger

	mu   sync.Mutex
	srv  *http.Server
	port int
	errc chan error
}

func (h *httpServer) listen(port int) error {
	ln, err := net.Listen("tcp", net.JoinHostPort(h.host, strconv.Itoa(port)))
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: h.handler, ReadHeaderTimeout: 10 * time.Second}
	h.mu.Lock()
	old := h.srv
	h.srv, h.port = srv, port
	h.mu.Unlock()
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			h.errc <- err
		}
	}()
	if old != nil {
		go func() { // let the "saved" answer reach the browser first
			time.Sleep(1500 * time.Millisecond)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			old.Shutdown(ctx)
		}()
		h.log.Info("now serving on a new port", "port", port)
	}
	return nil
}

func (h *httpServer) Port() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.port
}

func (h *httpServer) shutdown() error {
	h.mu.Lock()
	srv := h.srv
	h.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}

func run(ctx context.Context, exit func(), f flags, mode string, execArgs []string, log *slog.Logger) error {
	envFile := f.envFile
	if envFile == "" {
		envFile = os.Getenv("PTZ_ENV_FILE")
	}
	if envFile == "" {
		envFile = filepath.Join(config.ExeDir(), "ptz.env")
	}
	if err := config.LoadEnvFile(envFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("env file %s: %w", envFile, err)
	}
	cfg := config.FromEnv(f.mock)
	if err := logs.AttachDir(filepath.Join(cfg.DataDir, "logs")); err != nil {
		log.Warn("log files not written — logging to the console only", "dir", filepath.Join(cfg.DataDir, "logs"), "err", err)
	}
	defer logs.Close()
	log.Info("starting", "version", Version, "mode", mode, "os", runtime.GOOS+"/"+runtime.GOARCH, "data", cfg.DataDir, "mock", cfg.Mock)

	if cfg.Mock {
		// Two simulated cameras: the first is set up, the second waits
		// to be found by Settings → Cameras → Add camera.
		for i, ports := range [][2]int{{cfg.CameraPort, 0}, {cfg.CameraPort + 1, 1}} {
			m := mockcam.New()
			m.Log = log
			httpAddr := hostPort(cfg.CameraHTTP)
			if ports[1] == 1 {
				h, p, _ := net.SplitHostPort(httpAddr)
				n, _ := strconv.Atoi(p)
				httpAddr = net.JoinHostPort(h, strconv.Itoa(n+1))
			}
			va, ha, err := m.Serve(ctx, net.JoinHostPort(cfg.CameraHost, strconv.Itoa(ports[0])), httpAddr)
			if err != nil {
				return fmt.Errorf("mock camera %d: %w", i+1, err)
			}
			log.Info("mock camera running", "visca", va, "http", ha)
		}
	}

	setStore, err := settings.Open(filepath.Join(cfg.DataDir, "settings.json"), func() settings.Settings { return seedSettings(cfg) })
	if err != nil {
		return err
	}
	set := setStore.Get()
	portLocked := os.Getenv("PTZ_PORT") != ""
	port := set.System.Port
	if portLocked {
		port = cfg.BindPort
	}

	// Devices come from the Settings screen (saved by the app as
	// devices.json). Mock mode starts simulated until something is saved.
	host := addons.New(filepath.Join(cfg.DataDir, "addons"), log)
	defer host.Close()
	// The release key also signs add-on packages (bundled and updates).
	var addonKey ed25519.PublicKey
	if k := firstNonEmpty(cfg.UpdatePubKey, UpdatePublicKey); k != "" {
		addonKey, _ = update.ParsePublicKey(k)
	}
	host.PubKey = addonKey
	host.InstallBundled(addonKey)
	var devFile *devices.File
	if df, err := devices.LoadFile(cfg.DevicesPath); err == nil {
		devFile = df
		if raw, _ := os.ReadFile(cfg.DevicesPath); !strings.Contains(string(raw), `"connections"`) {
			// First start after the add-on update: keep the old file, save the new shape.
			os.WriteFile(cfg.DevicesPath+".v1.bak", raw, 0o644)
			if err := api.WriteDevices(cfg.DevicesPath, df); err == nil {
				log.Info("devices moved to connections (add-ons)", "connections", len(df.Connections), "backup", cfg.DevicesPath+".v1.bak")
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		log.Error("saved device settings unreadable — starting with none", "path", cfg.DevicesPath, "err", err)
	} else if cfg.Mock {
		devFile = mockDevices()
	} else {
		devFile = &devices.File{Version: 2}
		log.Info("no connections set up yet — use Settings → Connections", "path", cfg.DevicesPath)
	}
	dev := devices.Open(devFile, host.Open)
	defer dev.Close()
	for _, l := range dev.Conns {
		if l.Err != nil {
			log.Warn("connection not started", "connection", l.Name, "addon", l.Addon, "err", l.Err)
		} else {
			log.Info("connection open", "connection", l.Name, "addon", l.Addon, "can", strings.Join(l.Caps, ","))
		}
	}

	ms, err := macros.OpenStore(cfg.MacrosPath)
	if err != nil {
		return fmt.Errorf("macro store %s: %w", cfg.MacrosPath, err)
	}
	defer ms.Close()

	pubKey := cfg.UpdatePubKey
	if pubKey == "" {
		pubKey = UpdatePublicKey
	}
	um := &update.Manager{Current: Version, Dir: filepath.Join(cfg.DataDir, "updates"), Log: log,
		Source: api.UpdateSource(set.System, cfg.UpdateAPI)}
	if pubKey != "" {
		if k, err := update.ParsePublicKey(pubKey); err == nil {
			um.PubKey = k
		} else {
			log.Error("update signing key unusable — updates disabled", "err", err)
		}
	}

	keys, err := auth.OpenStore(filepath.Join(cfg.DataDir, "keys.db"))
	if err != nil {
		return fmt.Errorf("key store: %w", err)
	}
	defer keys.Close()
	trust, err := auth.NewTrust(set.System.TrustLAN, strings.Join(set.System.TrustedCIDRs, ","))
	if err != nil {
		return fmt.Errorf("trusted networks: %w", err)
	}

	dash, err := dashboards.Open(filepath.Join(cfg.DataDir, "dashboards.db"))
	if err != nil {
		return fmt.Errorf("dashboard store: %w", err)
	}
	defer dash.Close()

	stg, err := stage.Open(filepath.Join(cfg.DataDir, "stage.json"))
	if err != nil {
		return fmt.Errorf("stage messages: %w", err)
	}

	reg := cams.New(ctx, cfg.DataDir, log)
	defer reg.Close()
	gw := &video.Gateway{Dir: filepath.Join(cfg.DataDir, "video"), ExeDir: config.ExeDir(), Log: log}
	names := &netinfo.Responder{Log: log}
	defer names.Close()

	hs := &httpServer{host: cfg.BindHost, log: log, errc: make(chan error, 1)}
	srv := &api.Server{Logs: logs, Stage: stg, Keys: keys, Trust: trust, Dashboards: dash, Cams: reg, Settings: setStore, Video: gw, Mock: cfg.Mock,
		Names: names, BindHost: cfg.BindHost, PortLocked: portLocked, Port: hs.Port,
		Web: web.Dist, Legacy: web.Legacy, Log: log, Version: Version,
		Dev: dev, DevFile: devFile, DevicesPath: cfg.DevicesPath, Addons: host, MacroStore: ms, Updates: um,
		Restart: api.Restarter{Mode: mode, ExecArgs: execArgs, ServiceArg: []string{"run"}, Exit: exit}}
	srv.Macros = macros.NewEngine(ms, srv.MacroActions())
	host.Grant = srv.GrantAddon
	sched, err := schedule.Open(filepath.Join(cfg.DataDir, "schedule.json"), srv.ScheduleRunner(), log)
	if err != nil {
		return fmt.Errorf("scheduled actions: %w", err)
	}
	srv.Schedule = sched
	go sched.Run(ctx)
	hs.handler = srv.Handler() // sets reg.OnStatus before any camera starts

	reg.Apply(set)
	if err := gw.Apply(set); err != nil {
		log.Error("stream gateway config not written", "err", err)
	}
	names.Set(set.System.Name)
	setStore.OnChange(func(old, next settings.Settings) {
		srv.LogSettingsChange(old, next)
		srv.ApplySettings(next)
		if !portLocked && next.System.Port != old.System.Port {
			if err := hs.listen(next.System.Port); err != nil {
				log.Error("could not move to the new port", "port", next.System.Port, "err", err)
			} else {
				srv.OpenControllers()
			}
		}
	})
	go gw.Run(ctx)
	go um.Run(ctx)
	go srv.AutoUpdate(ctx)

	if err := hs.listen(port); err != nil {
		return err
	}
	srv.OpenControllers() // now the AI add-on can be told this controller's address
	log.Info("PTZ controller up", "version", Version, "port", port, "mode", mode,
		"cameras", len(set.Cameras), "mock", cfg.Mock, "data", cfg.DataDir, "setupDone", set.SetupDone)

	select {
	case <-ctx.Done():
	case err := <-hs.errc:
		return err
	}
	log.Info("shutting down: stopping camera motion")
	reg.StopAll()
	return hs.shutdown()
}

func hostPort(base string) string {
	for _, p := range []string{"http://", "https://"} {
		if len(base) > len(p) && base[:len(p)] == p {
			return base[len(p):]
		}
	}
	return base
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
