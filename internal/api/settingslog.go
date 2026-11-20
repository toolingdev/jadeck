package api

import (
	"fmt"
	"slices"

	"ptz/internal/settings"
)

// LogSettingsChange writes one line saying what changed in Settings —
// which camera, which switch — never a password or token.
func (s *Server) LogSettingsChange(old, next settings.Settings) {
	var ch []string
	byID := func(l []settings.Camera) map[string]settings.Camera {
		m := map[string]settings.Camera{}
		for _, c := range l {
			m[c.ID] = c
		}
		return m
	}
	o, n := byID(old.Cameras), byID(next.Cameras)
	for id, c := range n {
		p, ok := o[id]
		switch {
		case !ok:
			ch = append(ch, fmt.Sprintf("camera added: %s (%s)", c.Name, c.Host))
		default:
			var what []string
			if p.Name != c.Name {
				what = append(what, "name")
			}
			if p.Host != c.Host || p.ViscaPort != c.ViscaPort || p.HTTPPort != c.HTTPPort || p.RTSPPort != c.RTSPPort {
				what = append(what, fmt.Sprintf("address %s→%s", p.Host, c.Host))
			}
			if p.User != c.User || p.Pass != c.Pass {
				what = append(what, "login")
			}
			if p.Video != c.Video || p.StreamHD != c.StreamHD || p.StreamSD != c.StreamSD {
				what = append(what, "video")
			}
			if len(what) > 0 {
				ch = append(ch, fmt.Sprintf("camera %s: %v", c.Name, what))
			}
		}
	}
	for id, c := range o {
		if _, ok := n[id]; !ok {
			ch = append(ch, "camera removed: "+c.Name)
		}
	}
	if old.MainID != next.MainID {
		ch = append(ch, "main camera changed")
	}
	if old.Video != next.Video {
		ch = append(ch, fmt.Sprintf("video: managed=%v program=%v confidence=%v external=%q", next.Video.Managed, next.Video.Program, next.Video.Confidence, next.Video.External))
	}
	os, ns := old.System, next.System
	if os.Port != ns.Port {
		ch = append(ch, fmt.Sprintf("port %d→%d", os.Port, ns.Port))
	}
	if os.Name != ns.Name {
		ch = append(ch, fmt.Sprintf("network name %q→%q", os.Name, ns.Name))
	}
	if os.TrustLAN != ns.TrustLAN || !slices.Equal(os.TrustedCIDRs, ns.TrustedCIDRs) {
		ch = append(ch, fmt.Sprintf("trusted networks: lan=%v extra=%v", ns.TrustLAN, ns.TrustedCIDRs))
	}
	if os.UpdateRepo != ns.UpdateRepo || os.UpdateToken != ns.UpdateToken || os.Channel != ns.Channel || os.AutoUpdate != ns.AutoUpdate {
		ch = append(ch, fmt.Sprintf("updates: channel=%s auto=%v", ns.Channel, ns.AutoUpdate))
	}
	if old.SetupDone != next.SetupDone {
		ch = append(ch, fmt.Sprintf("setup done=%v", next.SetupDone))
	}
	if len(ch) == 0 {
		return
	}
	s.Log.Info("settings changed", "what", fmt.Sprint(ch))
}
