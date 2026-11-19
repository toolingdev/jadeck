# PTZOptics Web Controller — Project Brief

A modern, web-based replacement for the legacy PTZOptics desktop camera-control
app (v1.4.1, final release, now filed by PTZOptics under "Legacy Apps"), built
for the a venue's PTZ camera. Started Nov 7, 2026.

## Why

- The vendor app is end-of-life: v1.4.1 is the last release and PTZOptics has
  moved on (its successor, Horizon, only supports the newer camera line).
- Its main window caps at **9 presets** — and our preset grid is already full.
- The operator is chained to the desktop app on one PC. We want full control
  from a phone or tablet anywhere in the sanctuary.
- We keep our own copies of the vendor app and firmware in
  `~/workspace/venue-av/ptzoptics-archive/` for troubleshooting and fallback.

## Confirmed requirements (the owner, Nov 7, 2026)

1. **A proper web app.** Served locally at venue. A small server talks to the
   camera (VISCA over IP + the camera's HTTP CGI API); the browser is the only
   client. No cloud dependency — it must work on the venue LAN alone.
2. **Full feature parity on mobile.** Phone and tablet get the *complete*
   control set. Never a neutered mobile version — the owner explicitly hates
   cut-down mobile versions. Layouts are touch-first; the same capabilities
   are on every screen, arranged for the screen, never removed.
3. **Dark theme** (standing UI preference).
4. **Presets without the 9-cap.** Unlimited named presets (the camera itself
   stores 127 VISCA preset slots — see RECON.md), ordered for a service, with
   snapshot thumbnails. Existing presets carry over by name:
   **Main, Whole Venue, Specials, Piano, Special Piano, Band, Whole TV View,
   Announcements.**
5. **Service mode:** a big-button layout for the sanctuary PC and phones —
   the shots a service actually uses, one tap each, hard to mis-hit.
6. **Side-by-side rollout.** v1.4.1 stays installed on the venue PCs until
   the new app has run a full service cleanly.

## Controls in scope

- Pan / tilt / zoom with variable speed (press-and-hold + speed control)
- Focus: auto / manual, near / far
- Home position
- Presets: set, recall, rename, reorder, delete; per-preset exposure storage
  (a behavior of the old app worth keeping)
- Exposure: mode (Auto, Manual, SAE, AAE, Bright), iris, shutter, gain
- Image: brightness, saturation, contrast, sharpness, hue, flip / mirror
  - Current camera baseline (from the camera itself, Nov 7, 2026):
    brightness 11, saturation 4, contrast 10, sharpness 3, hue 8
- White balance: mode + OnePush trigger
- Camera status readout (name, model, firmware, connection state)
- **Live stream in the app** (added by the owner, Nov 7, 2026): the operator
  must *see* the camera while controlling it. Browsers can't play RTSP, so
  a MediaMTX gateway re-serves the feed as WebRTC (primary), HLS and
  snapshot fallbacks — see "Stream architecture" below. (This supersedes
  the Phase 1 snapshot-only preview plan.)
- **Smooth control** (added by the owner, Nov 7, 2026): press-and-hold motion
  must feel smooth, never jittery — see "Smooth control design" below.

Out of scope for v1: USB/UVC cameras, multi-camera (the venue has one PTZ;
the design should not preclude a second), OBS/vMix switcher control (the old
app's embedded web server had it; revisit after the camera core is proven),
gamepads, recording/streaming control.

## The camera

| | |
|---|---|
| Model | PTZOptics PT30X-NDI-G2 (30X G2, NDI) |
| LAN | 10.1.10.110, venue network |
| Platform | devtype V63C, model F16.HI, serial rf11130176 |
| Firmware | SOC v6.3.12 installed (update to v6.3.45 tracked separately) |
| Control | VISCA over TCP port 5678; VISCA-over-IP UDP port 1259 also listens |
| HTTP | `/cgi-bin/param.cgi?get_*` config reads (no auth on current firmware), `/snapshot.jpg`, RTSP streams 1 (HD) / 2 (SD), NDI (program feed to vMix) |

## Proposed stack (boring on purpose)

- **Server: Python 3 + FastAPI + uvicorn**, one small service on the venue
  office PC (Windows). Holds one VISCA TCP connection to the camera and
  arbitrates all browser clients, so phones and the PC can never fight over
  the camera (a real failure mode of the old app + its web server).
  Runs as an auto-start Windows service; no build tooling, no database server.
- **State:** preset names/order/exposure and thumbnails in a small SQLite
  file (or JSON) next to the server; preset *positions* stay in the camera's
  own VISCA slots.
- **Browser UI:** one responsive HTML/CSS/JS app, no framework, no build
  step. WebSocket for press-and-hold PTZ and status; the same DOM serves
  desktop, tablet, and phone. Installable as a PWA so it lives on the home
  screen. Dark theme only.
- **Protocol layer:** raw VISCA command builder + inquiry parser, and a thin
  client for the camera's CGI config endpoints — both reconstructed in
  RECON.md and verifiable against the real camera with read-only commands.

Alternatives considered: Node (works, but Python is the owner's daily language);
Go single binary (nice deployment, wrong toolbox for this maintainer);
Electron/Tauri (defeats the web-app requirement).

## Phases

1. **Recon** — understand the old app and the camera protocols. *(Done, see
   RECON.md.)*
2. **Protocol proof** — server + UI skeleton with the smooth-control and
   streaming design implemented, exercised end-to-end against a mock
   camera. *(Built Nov 7, 2026 — see PHASE2.md. Live inquiry verification
   against the real camera waits on the 6.3.45 firmware flash; first live
   motion test happens in a planned window, non-service day.)*
3. **Full UI** — presets with names/thumbnails/order, service mode, image and
   exposure panels, status. Dark, touch-first.
4. **Service trial** — run one full Saturday alongside v1.4.1; only after a
   clean run does the new app become the default.

## Component versions (verified latest, Nov 7, 2026)

The owner's rule: latest components, verified at build time — not from memory.
Each version below was checked against its official source the day it was
pinned (PyPI JSON API for Python packages, GitHub releases for MediaMTX,
endoflife.date/python.org for CPython).

| Component | Version | Role | Verified |
|---|---|---|---|
| CPython | 3.15.2 latest stable (deploy target); code runs on 3.10+, developed/tested on 3.12.3 | server runtime | endoflife.date, 2026-11-07 |
| FastAPI | **0.162.2** | REST + WebSocket service | PyPI, 2026-11-07 |
| uvicorn | **0.54.0** (`uvicorn[standard]`) | ASGI server | PyPI, 2026-11-07 |
| Starlette | 1.10.0 | FastAPI's framework layer (transitive pin at install) | PyPI, 2026-11-07 |
| pydantic | 2.14.3 | request models (transitive pin at install) | PyPI, 2026-11-07 |
| MediaMTX | **v1.21.3** (released 2026-10-25) | RTSP → WebRTC/HLS gateway | GitHub releases, 2026-11-07 |
| SQLite | 3.45.1 (Python stdlib) | preset store | runtime |
| Browser UI | **zero dependencies** — native RTCPeerConnection/WHEP, Pointer Events, WebSocket, CSS grid; no framework, no build step | client | — |

Pins live in `server/requirements.txt`. Re-verify before any future
upgrade; don't bump blindly mid-season.

## Stream architecture

Browsers cannot play the camera's RTSP, so video takes its own path,
parallel to control:

```
camera RTSP /1 (HD) ─┐
camera RTSP /2 (SD) ─┴─► MediaMTX (office PC) ──► WebRTC/WHEP :8889  (tier 1, sub-second)
                                              ──► HLS        :8888  (tier 2, Safari/iOS)
camera /snapshot.jpg ──► FastAPI proxy /api/snapshot                (tier 3, stills @0.9 s)
```

- The UI tries tiers in order and badges which one is live (`STREAM:
  WEBRTC · LIVE`, `HLS · LIVE`, or `SNAPSHOT PREVIEW`). HD/SD switches
  between the two MediaMTX paths (`cam`, `cam_sd`); URLs are handed to the
  page by `/api/config` so nothing is hard-coded per client.
- The FastAPI service never proxies live video — control and video stay
  independent, so a video hiccup can never stall a stop command.
- Config: `stream/mediamtx.yml`; rationale + alternatives in
  `stream/README.md`. NDI remains the program feed to vMix, untouched.

## Smooth control design

Why camera control feels jittery in naive web apps: a new connection (or
an HTTP request) per jog, repeated drive commands for as long as a button
is held, and full-speed starts. This design removes all three:

1. **One persistent VISCA TCP connection**, owned by the service
   (`server/app/camera.py`), with auto-reconnect. Frames are written
   atomically under one lock; nothing reconnects per command.
2. **Drive is a state, not a stream.** The page sends its desired drive
   vector (pan/tilt/zoom/speed) over the WebSocket only when it changes,
   plus a 500 ms keepalive while held. A 25 Hz control loop in the
   service compares desired vs. applied and emits a VISCA command *only
   on change* — pointer jitter and chatty clients are coalesced away.
   Measured against the mock camera: a 1 s hold produced 6 frames total
   (5-step speed ramp + stop); a burst of 60 state updates in 150 ms
   produced the same 6. (PHASE2.md has the frame log.)
3. **True press-and-hold semantics:** drive command on press with the
   speed parameter, one STOP on release — never repeated jog requests.
4. **Speed ramps** from 0 to target over ~200 ms at drive start, so
   starts/stops don't jolt the shot; the UI speed slider (Precise↔Fast)
   maps to VISCA pan 1–24 / tilt 1–20 / zoom 0–7.
5. **Dead-man's switch:** if the driving client goes silent > 1.2 s
   (closed tab, lost Wi-Fi) the service stops the camera on its own; a
   clean disconnect stops it immediately. Discrete commands (preset
   recall, home) force-stop any active drive first.
6. **Low-latency video** (tier 1 WebRTC, above) closes the loop — smooth
   commands with a 3-second-delayed picture would still *feel* broken.

## Rules for this project

- Nothing changes on the venue PCs or the camera mid-service.
- Firmware flashing is a weekday decision, never a service day.
- Read-only camera queries are always safe; motion commands only in a
  planned test window.
- The vendor archive (app + firmware) stays untouched in
  `~/workspace/venue-av/ptzoptics-archive/`.
