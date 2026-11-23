// Package svc wraps the OS service manager (Windows SCM, systemd,
// launchd) so ptz installs, runs and restarts the same way everywhere.
package svc

import (
	"github.com/kardianos/service"
)

const Name = "ptz-controller"

func Config(args []string) *service.Config {
	return &service.Config{
		Name:        Name,
		DisplayName: "jadeck",
		Description: "Web controller for PTZ cameras, switchers, slides and mixers.",
		Arguments:   args,
		Option: service.KeyValue{
			// Restart if it ever exits unexpectedly.
			"Restart":                "always", // systemd
			"KeepAlive":              true,     // launchd
			"RunAtLoad":              true,     // launchd
			"OnFailure":              "restart",
			"OnFailureDelayDuration": "5s", // windows
			"OnFailureResetPeriod":   600,
			"DelayedAutoStart":       false,
		},
	}
}

type noop struct{}

func (noop) Start(service.Service) error { return nil }
func (noop) Stop(service.Service) error  { return nil }

// Control runs install | uninstall | start | stop | restart.
func Control(action string, args []string) error {
	if label, plist, ok := launchAgent(); ok && action != "install" && action != "uninstall" {
		return agentControl(action, label, plist)
	}
	s, err := service.New(noop{}, Config(args))
	if err != nil {
		return err
	}
	return service.Control(s, action)
}

// Status reports the installed service's state as text.
func Status() (string, error) {
	if label, _, ok := launchAgent(); ok {
		return agentStatus(label)
	}
	s, err := service.New(noop{}, Config(nil))
	if err != nil {
		return "", err
	}
	st, err := s.Status()
	if err != nil {
		return "", err
	}
	switch st {
	case service.StatusRunning:
		return "running", nil
	case service.StatusStopped:
		return "stopped", nil
	}
	return "unknown", nil
}

// Interactive is false when the OS service manager started us.
func Interactive() bool { return service.Interactive() }
