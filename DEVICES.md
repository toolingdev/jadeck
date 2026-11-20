# Connections

The gear the controller talks to lives under **Settings → Connections**.
There can be any number of each kind, and each connection comes from an
add-on (see [ADDONS.md](ADDONS.md)):

| Kind | What it does | Add-ons |
|---|---|---|
| Video switcher | preview / cut / fade, state, tally, live | vMix, Simulated |
| Slides | next, prev, blank, logo, clear (plus the add-on's own actions) | Proclaim (network), MIDI notes, HTTP API, Simulated |
| Audio mixer | per-channel mute with read-back, quick channels | Behringer WING (X32/M32 experimental), Simulated |

Camera: PTZOptics VISCA over TCP, built into the controller.

**Setting up.** Add a connection, fill in its form, press **Test**, then
**Save**. Changes go live at once, with no restart. Only the trusted
network (this PC, the LAN, Tailscale) can change connections; API keys
can't.

**Where it's saved.** The app saves connections in
`<data dir>/devices.json` (version 2) and owns that file. A version-1 file
(one switcher, one slides, one mixer) is migrated on start, and a backup
is kept. `ptz --mock` starts with the Simulated add-on's three
connections.

## Notes per add-on

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
