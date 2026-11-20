# PTZ Controller

A web controller for venue AV: PTZ cameras, video switcher, slides and
sound board, from any phone, tablet or PC on the venue network. It's one
program per computer, with no cloud and no accounts.

- **Cameras:** PTZOptics and VISCA-over-IP PTZ cameras. Hold-to-move or a
  joystick, presets, focus, picture settings, and white balance set by
  tapping a point in the picture. Several cameras, each with its own presets.
- **Live video:** WebRTC under a second of delay, through the built-in
  MediaMTX gateway. Also the vMix program output and slides confidence.
- **Devices:** vMix, Proclaim (MIDI), Behringer WING (OSC), and others
  through drivers.
- **Dashboards:** drag-and-drop layouts for phones, tablets and desktops.
- **Stage screens:** the booth sends "5 minutes" to the pulpit iPad, the
  speaker taps "👍 Got it". Live / next tally, the stream and a shared
  countdown on any phone or tablet. See [STAGE.md](STAGE.md).
- **Macros:** one tap runs mute + switch + slide + preset.
- **Agents:** an MCP server with scoped, revocable API keys.
- **Updates:** signed releases, installed from Settings.

Everything is set up in the browser, with no config files.

## Install

**Windows**: PowerShell **as Administrator**:

```powershell
irm https://github.com/toolingdev/ptz-controller/releases/latest/download/install.ps1 | iex
```

**macOS / Linux**: Terminal:

```bash
curl -fsSL https://github.com/toolingdev/ptz-controller/releases/latest/download/install.sh | sh
```

The setup wizard opens in the browser. See [SETUP.md](SETUP.md).

## Docs

[SETUP.md](SETUP.md) · [DEVICES.md](DEVICES.md) · [DASHBOARD.md](DASHBOARD.md) ·
[STAGE.md](STAGE.md) · [MACROS.md](MACROS.md) · [MCP.md](MCP.md) · [DIAGNOSE.md](DIAGNOSE.md) · [UPDATE.md](UPDATE.md)

## Develop

Needs Go 1.27+ and Node 24+.

```bash
cd frontend && npm ci && npm run build && cd ..
go run ./cmd/ptz --mock      # simulated cameras and devices, http://localhost:8080
go test ./...
```

Release builds (`v*` tags) are made by `.github/workflows/ci.yml`.
