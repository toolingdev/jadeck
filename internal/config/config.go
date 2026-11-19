// Package config loads service configuration from environment
// variables. Variable names match the Python build (PTZ_*), so an
// existing env file works unchanged.
package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	CameraHost string
	CameraPort int    // VISCA over TCP
	CameraHTTP string // base URL for CGI + snapshot
	CameraUser string // digest auth on firmware 6.3.45+
	CameraPass string

	BindHost string
	BindPort int

	DataDir string // presets.db, macros.db live here
	DBPath  string

	DevicesPath string // devices.json: which switcher / presentation / mixer drivers
	MacrosPath  string

	// Signed updates from GitHub Releases.
	UpdateRepo   string // owner/name
	UpdateToken  string // only for a private repo
	UpdatePubKey string // base64 ed25519; default compiled in
	UpdateAPI    string // GitHub API base (default https://api.github.com)

	Mock bool
}

// LoadEnvFile reads KEY=VALUE lines into the environment; variables
// already set win. Secrets (camera credentials) live in this file on
// the host only, never in the repo.
func LoadEnvFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if _, set := os.LookupEnv(k); !set {
			os.Setenv(k, v)
		}
	}
	return sc.Err()
}

func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil {
		return v
	}
	return def
}

// ExeDir is the directory holding the running binary.
func ExeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return filepath.Dir(exe)
}

// FromEnv builds the config. mock forces mock-camera defaults.
func FromEnv(mock bool) Config {
	mock = mock || os.Getenv("PTZ_MOCK") == "1"
	var c Config
	c.Mock = mock
	if mock {
		c.CameraHost = env("PTZ_CAMERA_HOST", "127.0.0.1")
		c.CameraPort = envInt("PTZ_CAMERA_PORT", 15678)
		c.CameraHTTP = env("PTZ_CAMERA_HTTP", "http://127.0.0.1:18081")
	} else {
		c.CameraHost = env("PTZ_CAMERA_HOST", "10.1.10.110")
		c.CameraPort = envInt("PTZ_CAMERA_PORT", 5678)
		c.CameraHTTP = env("PTZ_CAMERA_HTTP", "http://"+c.CameraHost)
	}
	c.CameraHTTP = strings.TrimRight(c.CameraHTTP, "/")
	c.CameraUser = os.Getenv("PTZ_CAMERA_USER")
	c.CameraPass = os.Getenv("PTZ_CAMERA_PASS")
	c.BindHost = env("PTZ_BIND", "0.0.0.0")
	c.BindPort = envInt("PTZ_PORT", 8080)
	c.DataDir = env("PTZ_DATA_DIR", filepath.Join(ExeDir(), "data"))
	c.DBPath = env("PTZ_DB", filepath.Join(c.DataDir, "presets.db"))
	c.MacrosPath = env("PTZ_MACROS_DB", filepath.Join(filepath.Dir(c.DBPath), "macros.db"))
	c.DevicesPath = os.Getenv("PTZ_DEVICES")
	if c.DevicesPath == "" {
		c.DevicesPath = filepath.Join(c.DataDir, "devices.json")
	}
	c.UpdateRepo = os.Getenv("PTZ_UPDATE_REPO")
	c.UpdateToken = os.Getenv("PTZ_UPDATE_TOKEN")
	c.UpdatePubKey = os.Getenv("PTZ_UPDATE_PUBKEY")
	c.UpdateAPI = os.Getenv("PTZ_UPDATE_API")
	return c
}
