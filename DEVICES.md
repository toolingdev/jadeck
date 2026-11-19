# Devices and drivers

The controller ships generic. Everything venue-specific lives in a
`devices.json`, not in code. It picks one driver per device kind:

| Kind | Interface | Drivers included |
|---|---|---|
| Switcher | preview / cut / fade, state | `vmix`, `sim` |
| Presentation | named actions (next, prev, black, …) | `midi` (Proclaim, anything MIDI-learnable), `http` (ProPresenter-style REST), `sim` |
| Mixer | per-channel mute with read-back | `osc` with presets `wing`, `x32` (or custom addresses), `sim` |

Camera: PTZOptics VISCA over TCP (the camera layer is its own package;
more camera protocols are a planned driver kind).

## Setting devices up

Use **Settings → Devices** in the app: pick a driver per device, fill in
the form, press **Test**, then **Save & apply**. Changes go live at once,
no restart. Only the trusted network (this PC, the LAN, Tailscale) can
change them; API keys can't.

The app saves the result to `<data dir>/devices.json` (or `PTZ_DEVICES`).
Nobody needs to edit that file. `ptz --mock` starts with simulated
devices until something is saved.

Examples: `examples/devices.example.json` (vMix + Proclaim over MIDI +
WING) and `examples/devices.example.json` (vMix + ProPresenter + X32).

## Driver notes

- **midi**: Windows uses winmm and Linux uses ALSA raw MIDI
  (`/dev/snd/midiC*D*`). macOS isn't supported yet. On Windows, install
  a virtual port (loopMIDI), name it in `port`, and teach the slide
  software each note. If the port isn't there, buttons fail loudly
  instead of pretending, and the port is picked up when it appears.
- **http**: one request per action. `method` defaults to GET. `body`
  is sent as JSON. `headers` apply to every request.
- **osc**: `preset` sets the port, mute address and polarity. Any field
  overrides it: `port`, `muteAddress` (`{n}` = channel, `{nn}` =
  2-digit), `mutedValue`, `probe`, `maxChannel`. The `x32` preset is
  written from the published protocol but untested on a real desk.
- **favorites**: up to 8 quick-mute slots. `channel: 0` marks a
  placeholder. Macros can refer to favorites by `channelName`.

## Adding a driver

1. Create `internal/devices/<name>/` and implement `devices.Switcher`,
   `devices.Presentation` or `devices.Mixer`.
2. In `init()`, call `devices.RegisterSwitcher("<name>", factory)` (or
   `RegisterPresentation` / `RegisterMixer`). The factory gets the raw
   JSON of its settings. Also call `devices.Describe(devices.Meta{...})`
   with the driver's fields; that's what builds its form in Settings.
3. Blank-import the package in `cmd/ptz/main.go`.
4. Test it against a local fake: see `vmix_test.go` (httptest) and
   `osc_test.go` (a real UDP console stand-in).

Rules every driver keeps:

- Status calls never take the app down. Report errors; don't panic.
- A mixer `SetMute` may be fire-and-forget. The core always reads back
  through `GetMute` and refuses to call a change done until the console
  confirms it.
- No silent fallbacks that claim success.
