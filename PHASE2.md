# Phase 2 — service + UI skeleton, smooth control, live stream

Built Nov 7, 2026. Scope per the owner's approval: latest components
(verified, not remembered), live stream in the app, smooth — not
jittery — press-and-hold control. Everything below ran **locally in the
sandbox against the mock camera**; nothing was installed or started on
the venue office PC, and nothing was deployed.

## What was built

```
server/
  run.py               entry point (`--mock` spawns the mock camera too)
  requirements.txt     fastapi==0.162.2, uvicorn[standard]==0.54.0
  mock_camera.py       mock VISCA camera (TCP 15678 + HTTP 18081):
                       answers inquiries and integrates drive commands
                       into simulated positions; logs every frame received
  assets/mock-snapshot.jpg
  app/
    visca.py           command builders + reply parsers (pure, unit-checked)
    camera.py          CameraService — the heart of the smoothness design
    store.py           SQLite preset store (stdlib), seeded with the 8 presets
    config.py          env-driven config (PTZ_MOCK=1 → mock endpoints)
    main.py            FastAPI app: REST + WS, serves web/, snapshot proxy
web/                   index.html, style.css, app.js, PWA manifest/icon/SW
stream/                mediamtx.yml + README (gateway config, not installed)
```

### API surface

- `GET /api/status` — connection, power, pan/tilt/zoom (live inquiries),
  focus mode, device info (name/model/serial/firmware via camera CGI)
- `GET /api/config` — stream URLs for the page (WHEP/HLS, HD+SD)
- `GET /api/snapshot` — camera `snapshot.jpg` proxied (tier-3 preview +
  future preset thumbnails)
- Presets: `GET/POST /api/presets`, `PATCH/DELETE /api/presets/{id}`,
  `POST /api/presets/reorder`, `POST /api/presets/{id}/recall`,
  `POST /api/presets/{id}/store`
- `POST /api/ptz/home`, `POST /api/ptz/stop`, `POST /api/focus`
  (auto/manual/onepush/near/far/stop)
- `WS /ws/control` — drive state in, status/position broadcasts out
  (position polled from the camera at ~1.7 Hz while connected)

### Smooth control, as implemented

See PROJECT.md "Smooth control design" for the rationale. In
`camera.py`: one persistent TCP connection with reconnect; atomic frame
writes; a 25 Hz control loop that emits VISCA drive commands **only when
the desired vector or ramped speed changes**; ~200 ms speed ramp at
drive start; STOP exactly once on release; 1.2 s dead-man's switch plus
immediate stop on disconnect; discrete commands force-stop first.

### Stream, as implemented

MediaMTX v1.21.3 config (`stream/mediamtx.yml`) pulls
`rtsp://10.1.10.110:554/1` as path `cam` and `/2` as `cam_sd`
(PTZOptics G2 documented paths; camera is in RTSP workmode with RTSP
auth off per the Nov 7 config snapshot, so no credentials are needed).
The page implements the client side of all three tiers: a dependency-free
WHEP player (raw `RTCPeerConnection`), native-HLS fallback, and the
snapshot tier, with a live badge showing which tier is active and an
HD/SD switch. **The gateway itself has not been run yet** — it runs on
the office PC in the install phase, so the WHEP/HLS tiers are
code-complete but not yet exercised against a real MediaMTX + camera.

## Verification performed (all against the mock, Nov 7, 2026)

- **REST battery:** status (connected, device info parsed from mock CGI),
  preset list shows the 8 seeded presets in order with slots 1–8,
  create/rename/reorder/delete round-trips, recall/home/focus return ok,
  snapshot proxy returns the JPEG, `/api/config` URLs correct.
- **Drive coalescing (the headline test):** from the mock's frame log —
  - 1 s hold at speed 40 → **5 drive frames** with VISCA pan speeds
    ramping 2→4→6→9→10, then **1 stop frame**. Position readout moved
    (pan 0 → 314 in mock units) and settled on release.
  - 60 drive-state updates spammed in ~150 ms (simulated pointer jitter)
    → the **same 5 ramp frames + 1 stop**. 60 messages in, 6 commands out.
  - Client vanished mid-drive (socket closed, no stop) → service stopped
    the camera; position stable across follow-up reads.
- **UI render:** headless Chromium (Playwright shell) screenshots at
  1280×900 and 390×844 against the running service + mock. Both layouts
  render correctly; DOM checks confirm all 8 preset names, live status
  header (name, model, firmware string), position readouts, and the
  stream pane correctly landing on the **SNAPSHOT PREVIEW** tier with
  badges when no gateway is present. Phone layout stacks with **the same
  full control set** — parity verified visually, nothing hidden or cut.

## Install — office PC deployment (Nov 7, 2026, post-flash)

Deployed to the venue office PC (booth-pc) at the owner's
direction, after the flash verified SOC v6.3.45:

- Layout `C:\PTZControl\`: `runtime\` (CPython 3.15.2 embeddable +
  pip; fastapi 0.162.2 / uvicorn 0.54.0 / websockets / wsproto —
  uvicorn[standard]'s PyYAML has no cp315 wheel yet, and the app needs
  none of the other extras), `app\` (server + web), `mediamtx\`
  (MediaMTX v1.21.3 + production mediamtx.yml: cam/cam_sd as in the
  repo config, plus the management API on 127.0.0.1:9997 only),
  `config\ptzcontrol.env` (camera credentials + env — PC-local only,
  loaded via PTZ_ENV_FILE), `logs\`, `tools\nssm.exe`.
- Services (NSSM, LocalSystem, Automatic, restart-on-failure):
  **PTZControlApp** (python run.py) and **PTZMediaMTX**. Both verified
  Running. Firewall: inbound allow, LocalSubnet scope only — TCP
  8080 (app), TCP 8888/8889 + UDP 8189 (MediaMTX). AC sleep was
  already Never (High performance plan); no power change made.
- Digest auth: firmware 6.3.45 requires it on param.cgi +
  snapshot.jpg. Python urllib's digest handler works (Windows curl's
  did not). The service reads device info, image conf and snapshots
  through it — `/api/status` shows SOC v6.3.45 from the camera itself.
- Read-only live verification: VISCA inquiries return live position
  (pan 40 / tilt 0 / zoom 7961 at check time); MediaMTX reports cam +
  cam_sd ready; HLS master/variant/segment fetches return real bytes;
  WHEP POST returns 201 + SDP answer. **No motion commands were sent,
  no camera config was written.** First motion test is a separate,
  planned moment with eyes on the camera.
- Image-value drift noticed across the flash: bright 11→6, contrast
  10→8, hue 8→7 (saturation/sharpness unchanged) — the vendor Restore
  did not carry those three; flagged for the owner, not corrected (writes
  were out of scope for the install).
- Phone URL (venue Wi-Fi): http://10.1.10.241:8080

## Live camera verification — PENDING (firmware gate)

**Status: not yet done, deliberately.** Safety rule for today: the owner is
flashing the camera to SOC 6.3.45 during his lunch break; while it still
reports 6.3.12 the camera is not to be touched beyond version reads.

- Firmware reads (via the office PC, `get_device_conf`): **SOC v6.3.12**
  at 20:08, 20:13, and 20:23 UTC, Nov 7, 2026 — the flash had not
  happened yet as of the last check in this run.
- Accordingly: **zero** VISCA traffic was sent to the real camera, and no
  HTTP calls beyond the `get_device_conf` version reads. No movement or
  preset commands of any kind.

Remaining live steps, in order, once it reports 6.3.45 (inquiry-only —
all read-only, safe in a live venue):

1. `get_device_conf` + `get_image_conf` via the office PC — confirm the
   new SOC/ARM strings and that image settings survived the flash.
2. VISCA inquiries relayed from the office PC (PowerShell `TcpClient`):
   power (`81 09 04 00`), pan/tilt position (`81 09 06 12`), zoom
   position (`81 09 04 47`), focus mode (`81 09 04 38`) — confirms raw
   VISCA-over-TCP framing against real 6.3.45 firmware.
3. `snapshot.jpg` fetch via the office PC.

Still later (Phase 3, planned non-service window): first motion test —
and with it, byte-level confirmation of the one open protocol question,
**preset slot numbering** (`visca.py` sends the slot byte as the preset
number itself, 1–127, matching the legacy app's settings model; a
0-based offset can't be ruled out by inquiries alone, so presets must
never be recalled blind before that test).

## Also pending (not Phase 2 scope)

- Install phase on the office PC: CPython, the service as an auto-start
  service, MediaMTX beside it; first full end-to-end on the venue LAN.
- Preset thumbnails from `/api/snapshot` in the grid (data path exists).
- Per-preset exposure capture/re-apply (store schema already carries an
  `exposure` column; capture flow not wired yet).

## UI notes — joystick control mode (added Nov 7, 2026)

The owner asked for a toggle between the button pad and a joystick ("tap and
hold and move around… move the camera freely"), so the control area now
carries a **Buttons | Joystick** segmented toggle (persisted in
`localStorage["ptzControlMode"]`; identical in both layouts). Joystick
mode swaps only the 3×3 pad — zoom column, stop, speed slider, focus row
and presets are untouched.

- **Drag = drive**, through the same WebSocket drive-state channel. The
  server protocol takes direction signs + one speed, so the stick maps:
  direction = signs of the deflection unit vector with a per-axis 0.35
  gate (near-axis drags stay axial; true diagonals drive both axes);
  speed = slider × deflection, with deflection rescaled out of a 12%
  dead zone — push farther, go faster, slider is the cap. Emission is
  rAF-throttled, latest-wins; the server still ramps and coalesces.
- **Release = stop**: the knob springs back (180 ms), a zero state goes
  out immediately, and pointercancel / window blur take the same path —
  the server dead-man's switch remains the backstop.
- **Home = a dedicated side button** (changed Nov 7, 2026, later the
  same day): joystick mode originally made a quick tap on the stick
  (<250 ms, <12 px travel) fire Home. The owner flagged the risk — an
  accidental tap, or a drag cut short by a dropped connection being
  misread as a tap, could send the camera Home mid-service. The tap
  detection is **removed entirely**: the stick now only drives, and a
  plain tap does nothing. In its place, a **⌂ HOME button** sits at the
  top of the zoom column (above TELE), visible only in Joystick mode,
  wired with plain button `click` semantics (no pointer handlers), so a
  press that starts elsewhere and releases over it cannot fire it.
  Button mode is unchanged (pad-center Home). The mode hint now reads
  "Drag the stick to move · Home is on the side."
- Verified headless against the mock (Nov 7, 2026): toggle swaps and
  persists across reload; drags at 60% / diagonal / small deflection sent
  the expected sign + scaled-speed payloads and moved the mock camera;
  release stopped it; button mode re-verified after the change.
  Re-verified after the Home change (21/21 checks): a plain tap on the
  stick and a quick 8 px drag inside the old tap window both fired
  **zero** Home calls; a pointercancel mid-drag fired zero and stopped
  the camera; the side ⌂ HOME button fired exactly one Home call and
  returned the mock to pan 0 / tilt 0; press-on-stick-release-over-HOME
  and press-HOME-release-off-button fired nothing; pad-center Home and
  pad drive/stop unchanged. Previews:
  `ui-preview-mobile-joystick.png`, `ui-preview-desktop-joystick.png`.
  (One headless quirk worth knowing: Chromium headless stops producing
  rAF frames when idle, so a single jump-to-the-rim CDP sample can read
  one event stale; the vector→drive mapping itself was verified
  deterministically at the function level — mag 1.0 at/beyond the rim,
  0.545 at 60%, 0 inside the dead zone.)

## Advanced settings (added Nov 7, 2026)

The owner's parity requirement: the vendor app exposes white balance and
the rest of the camera's image controls in its advanced settings, so
ours does too — a collapsible **Advanced settings** card after Presets,
**identical fields in both layouts**, each group with a one-line
plain-language help text (the start of his in-app help requirement).
Deliberately excluded, by design: network/IP configuration, camera
passwords, RTSP auth, factory reset, firmware — those live elsewhere.

### API + camera layer

- `GET /api/camera/image` — the camera's image config, typed.
- `POST /api/camera/image` — partial update. **Read-modify-write**: the
  service fetches the camera's current `get_image_conf`, merges only
  the changed fields, and posts the whole conf back via
  `post_image_conf`. Untouched keys — including keys we don't model,
  e.g. `ldc` — go back **byte-identical**, mirroring the camera's own
  web-UI form. The response is a fresh read-back from the camera.
- `POST /api/camera/image/wb-trigger` — one-push WB trigger
  (`post_image_value&wbmode&trigger`, the documented G2 CGI form).
- The whole field map lives in one table, `server/app/imageconf.py`:
  JSON name ↔ conf key ↔ kind/range/enum, with a source note per group.
  Fixing a key name or range after live verification is a one-line edit
  there and nowhere else.

### Field map (29 modeled fields)

- **Image**: brightness, saturation, contrast, sharpness, hue
  (all 0–14), flip, mirror. Conf keys and ranges: the real camera's
  Nov 7 param.cgi snapshot + PTZOptics' G2 HTTP-CGI Control Sheet
  Rev 1.4. (The G2 OSD menu lists sharpness as Auto/0–15; the HTTP
  surface we're writing to documents 0–14, so 0–14 it is.)
- **White balance**: mode (auto / indoor 3000K / outdoor 5000K /
  onepush / manual / var — G2 CGI sheet enum), red/blue gain 0–255
  (Manual), red/blue tuning −10…+10 (Auto/Indoor/Outdoor), AWB
  sensitivity (low/normal/high), color temp (VAR), One Push trigger
  button. Gains/tuning/sensitivity ranges: G2 user manual (PT12X-SDI-G2
  Rev 1.2) COLOR menu + VISCA inquiries — same V63C platform family.
- **Exposure**: mode (auto / manual / shutter=SAE / iris=AAE / bright
  — G2 CGI sheet), shutter (17 documented steps 1/30–1/10000, Manual +
  SAE), iris (F1.8…F11.0 + Close, Manual + AAE), gain (Manual),
  bright level 0–17 (Bright), exp-comp on/off + −7…+7 (Auto),
  backlight (Auto), gain limit 0–15 (Auto/AAE/Bright), anti-flicker
  (off/50 Hz/60 Hz, Auto/Bright), DRC 0–8. UI rows show/hide by mode,
  matching the camera menus' "effective only in" notes.
- **Focus extras**: AF sensitivity (low/normal/high) and AF zone
  (top/center/bottom) — both exist on this camera per the G2 manual's
  VISCA inquiry list (CAM_AFSensitivityInq, CAM_AFZoneInq).
- **Noise reduction**: 2D NR (off/auto/1–5), 3D NR (off/1–8) — G2
  manual NOISE REDUCTION menu.

### Honest gaps — confirm at the live read step

- Only the image basics (bright/saturation/contrast/sharpness/hue/
  flip/mirror/ldc + their values) were ever read from the real camera.
  The WB/exposure/focus/NR **conf key names** are reconstructions from
  the documented CGI/VISCA vocabulary, and the mock's seed values for
  those groups are G2-manual menu defaults, **not** the real camera's
  current values. The post-flash `get_image_conf` read (already step 1
  of the live verification list above) settles all of it: reconcile key
  names/ranges in `imageconf.py`, then re-run the mock battery.
- Also unpinned: the VAR color-temperature range (2800–6500K per
  PTZOptics' published color docs), the Manual-mode gain range (0–15),
  shutter/iris encoding inside the conf (we store the documented step
  labels), and whether the real camera takes `post_image_conf` as a
  whole-form post (the platform's post_* convention) vs per-value
  `post_image_value` posts.
- Documented on the platform but intentionally not built: B&W mode,
  gamma, image style, LDC toggle (ldc rides along as a pass-through
  key only), dynamic hot pixel.

### Verification (mock + headless Chromium, Nov 7, 2026)

- REST battery: GET serves the seeded real values (brightness 11,
  saturation 4, contrast 10, sharpness 3, hue 8). POST {wbMode:
  manual, brightness: 9} → read-back reflects both; the conf actually
  posted to the mock differed from the seed in **exactly those two
  keys** — all 29 other keys byte-identical, including unmodeled
  `ldc`. WB trigger increments the mock's trigger counter. Bad enum →
  422, out-of-range → 422, bad shutter label → 400.
- UI battery (14/14 checks, CDP-driven): panel loads the seeded
  values; all five group help lines render; Red gain hides in Auto WB
  and appears in Manual; Gain hides in Auto exposure; changing WB mode
  + the brightness slider through the real page round-trips the mock;
  after a page reload the new values persist (read back from the
  mock). Previews with the card expanded:
  `ui-preview-mobile-advanced.png`, `ui-preview-desktop-advanced.png`
  — same fields, same values, both layouts.

## Phase 3 notes — pick a white point (added Nov 7, 2026)

The owner's idea, same day: *"for white balance couldn't we make it smart
where we can pick a point on the stream to use as our white?"* — plus
his refinement an hour later: a first fingertip tap is not accurate
enough, so the picker must work like a **magnifier loupe**, never
making the user guess where the point landed. This is now the
**working white-balance path** in the app: the Advanced panel's WB
mode/gain fields ride the CGI image conf, which the real camera's
`get_image_conf` does not serve at all (verified live on 6.3.45), so
that group stays dormant; the pick flow talks **VISCA only**.

### The interaction (identical on phone and desktop)

- Entry: **◎ Pick a white point** in the Advanced → White balance
  group, with the plain-language help line. Entering pick mode
  switches the stage to the snapshot/stills element (never pixel
  sampling from the WebRTC element) and fits a pick layer to the
  image's *actual rendered rect* — the pane letterboxes
  (`object-fit: contain`), and every mapping (loupe source, marker,
  posted coordinates) goes through that rect, not the element box.
- Press-and-drag: a 128 px circular loupe follows with pointer
  capture, rAF-throttled latest-wins (the same smoothness discipline
  as the joystick). It floats offset from the finger — above it in
  the pane's lower half, below it in the upper half; the wrap allows
  overflow while picking so the bubble is never clipped — and shows a
  ~3× zoomed canvas view whose source rect is centered on the current
  point, so the **fixed center dot + ring mark the exact pixel** that
  will be used. The first tap only rough-places; dragging refines
  continuously.
- Release: the loupe collapses to a pinned dot+ring marker at the
  refined point (re-draggable before confirming). The pinned point —
  including any move still queued in an animation frame at release,
  which the release path applies first — is the only thing Confirm
  can post. A bug exactly there (a pending rAF move discarded at
  release → the stale first-tap point posted) was caught by the UI
  check below and fixed before this shipped.
- No-guess feedback next to Confirm: a swatch of the exact sampled
  pixel (5×5 average off the snapshot canvas) plus a plain-language
  read — "leans warm" / "leans cool" / "looks neutral" from R vs B —
  and a gentle hint when the spot is dark (luma < 55) or strongly
  tinted ("whites work best — try a brighter, paler spot").
  Guidance only, never a block.
- The confirm bar warns plainly: **"the camera will move for a few
  seconds"** while it aims, balances, and comes back.

### The server flow — `POST /api/camera/wb-pick {x, y}`

`CameraService.wb_pick` (holds an **operation lock** end to end):

1. Force-stop any drive; read the current absolute pan/tilt/zoom
   from fresh VISCA inquiries (polled status as fallback). Store as
   the origin.
2. Set WB mode to One Push over VISCA: `81 01 04 35 03 FF`
   (`visca.wb_mode`), *not* CGI.
3. Compute the aim target with the FOV model (below) and drive there
   with the VISCA absolute-position command
   `81 01 06 02 VV WW 0Y0Y0Y0Y 0Z0Z0Z0Z FF` (`visca.pantilt_absolute`,
   speeds 18/16). Zoom is never touched. A center tap skips the move.
4. Settle: poll position inquiries until two reads ~200 ms apart
   agree (3 s timeout), then require the position within 24 units
   (~1.7°) of the target — a gross miss aborts to the return step.
5. Fire One Push: `81 01 04 35 05 FF` (`visca.wb_onepush_trigger`);
   wait 1.5 s for the balance to settle.
6. Absolute-move back to the stored origin, settle the same way, and
   verify within 8 units (~0.6°) — "back where it was" is the
   promise, and a miss is reported as an error, not smoothed over.
7. On **any** failure after motion began, the return move is still
   attempted before the error is reported.

Concurrency (the simpler safe option, chosen deliberately): while
the lock is held (~5 s), drive input over the WebSocket is **dropped,
not queued**, and the discrete REST commands (home, preset
recall/store, focus) answer **409**. Nothing interleaves with the
sequence.

### FOV model and its tolerance

- Position scale: both axes are **14.4 position units per degree** —
  the camera's mechanical spans (pan ±170° ↔ ±2448 units; tilt
  −30…+90° ↔ −432…+1296), the same spans the mock integrates against.
- Horizontal FOV at zoom position z: log-interpolated between the
  PT30X's endpoints, **60.7° at 0x0000 (wide) and 3.4° at 0x4000
  (optical tele)** — FOV narrows roughly geometrically with zoom, so
  the interpolation is exponential, clamped at optical max.
  Vertical FOV from the 16:9 relation
  `vFOV = 2·atan(tan(hFOV/2)·9/16)`. Deltas: `pan += (x−0.5)·hFOV`,
  `tilt += −(y−0.5)·vFOV` (screen y grows down, tilt grows up),
  converted at 14.4 units/° and clamped to the mechanical limits.
  Assumes flip/mirror off (the camera's standing baseline; both on
  would mirror the required aim direction).
- Tolerance: One Push averages a **center region**, not a single
  pixel, so the point only needs to land near center — the model's
  error (a degree or two at mid-zooms) is comfortably inside that.
  No visual refinement pass was built: the tapped point cannot be
  tracked in a fresh snapshot without image processing, and the
  region-average makes it unnecessary.

### Mock + tests — `server/tests_wb_pick.py` (34/34, Nov 7, 2026)

- The mock now animates absolute moves toward their target at a
  finite rate (settle polling sees real travel), records CAM_WB
  frames, logs every command frame (`/__test__/visca`), and supports
  failure injection (`/__test__/dropabs?n=` ACKs but silently drops
  the Nth absolute move).
- Unit: builder bytes (mode/trigger/absolute incl. clamping), FOV
  endpoints + monotonic narrowing, center tap = zero delta, corner
  tap clamps to the pan limit.
- End-to-end against the mock: frame order **mode → aim move →
  trigger → return move**; return target equals the stored origin
  *exactly* and the mock ends exactly there with zoom untouched;
  center tap sends mode + trigger and **no** motion frames; corner
  tap at wide zoom clamps to 2448 and returns; home and preset
  recall answer **409** mid-flow while the pick itself completes;
  dropped return move → the flow still *attempts* the return (frame
  in the log), reports **502**, and the camera's honest end state is
  the aim point; out-of-range coordinates → 422.
- UI (CDP): a synthetic drag from (0.25, 0.25) refined to
  (0.66, 0.44) posts exactly the **refined** coordinates. Previews:
  `ui-preview-mobile-wbpick-loupe.png` (loupe mid-drag: bubble,
  zoomed view, center ring/crosshair/dot) and
  `ui-preview-desktop-wbpick.png` (pinned marker, swatch + color
  read, confirm bar).

### Live status — NOT yet verified on the real camera

Everything above ran against the mock in the sandbox. **Nothing in
this feature has touched the real camera or the office PC** (built
in the repo only; deployment is a separate step). The VISCA WB mode
+ trigger frames, the absolute-position command's behavior, and the
settle tolerances ride the same first-motion test window as the
preset slot-byte question — the owner calls that moment, with eyes on
the camera. One specific thing to confirm there: the trigger frame
`81 01 04 35 05 FF` is per the PTZOptics G2 command set; the generic
VISCA table also assigns 0x05 in that command to Manual mode, so the
live check is: set One Push mode, fire the trigger at a known
off-white target, and watch the picture's white point actually move.

## Staged, hash-verified updates (added Nov 7, 2026)

The owner's update design, implemented the same day: new builds are
pushed to a staging directory on the PC (`C:\PTZControl\updates\
pending\`, sibling of the install root); the webapp notices and
shows an **Update** button; on Yes it performs a clean update —
backup, manifest-driven swap, health check, **automatic rollback**.
His hash-check requirement is the gate: a package is offered only
after every file's SHA-256 matches its manifest, and the detached
helper re-verifies everything again before touching a file, so an
accidental file, zip or tar in the staging folder is reported as
ignored and can never be installed. The app never reaches out to
any server at update time. Full design, package format, staging
procedure and invariants: **UPDATE.md**.

- Code: `server/app/updates.py` (detection/status/launch),
  `server/updater.py` (the detached helper — stdlib only, imports
  nothing from the app package), `tools/make_update_package.py`
  (builds `dist-update/<version>/` from HEAD; refuses a dirty tree),
  endpoints `GET /api/update/status` + `POST /api/update/apply`
  (409 while driving or mid-operation). Packaged payloads carry
  `server/VERSION`; the pre-updater build on the PC reports itself
  as "pre-updater build" until its replacement lands by manual
  deploy, after which updates flow through this system.
- UI: banner only when there is something to say (ready / ignored /
  outcome), an "Updating…" overlay that rides out the restart and
  reloads onto the new build, and an **Advanced → Updates** row with
  current version + staged state + the same button. PWA shell cache
  bumped to `ptz-shell-v2` so web changes reach installed clients.
- Verification: `server/tests_update.py`, **57/57** — validation
  battery (flipped byte, extra file, lone zip, traversal, DB-naming
  manifest, symlink), the packaging tool on a temp repo, a full
  apply against a fake install root with a real subprocess service
  (preset DB byte-identical through it), corruption injected after
  authorization (the helper's re-verification stops it; nothing
  changes), and a fingerprint-honest but broken build rolling back
  byte-for-byte. The pre-existing battery still passes (34/34).
  Previews: `ui-preview-{mobile,desktop}-update.png`,
  `ui-preview-desktop-updating.png`,
  `ui-preview-mobile-update-rolledback.png`.

## Phone landscape layout (added Nov 7, 2026)

The owner turned his phone sideways mid-service: everything worked,
but the portrait stack just stretched — the picture didn't own the
screen and the controls fell away below. Landscape on short
screens now gets its own arrangement (same DOM, same features —
the parity rule holds):

- Trigger: `@media (orientation: landscape) and (max-height:
  540px)` — phones sideways, nothing else. It is the last block in
  `style.css`, so it also wins if a wide landscape phone could
  match the desktop block.
- The page is a fixed `100dvh` column that never scrolls: slim
  33px header (name + PAN/TILT/ZOOM/STOPPED; the model/firmware
  sub-line yields its height), the update banner in flow when it
  has something to say, then the stage (left) and a **324px
  control rail** (right). The three control cards are wrapped in a
  `.rail` div that is `display: contents` everywhere else (the
  cards stay direct grid items of `main` — portrait and desktop
  are byte-for-byte the old layout) and a real, internally
  scrolling column only here.
- The stage takes the full height; the stream is letterboxed as
  large as physics allows (`object-fit: contain` does the work).
  Safe-area insets keep flowing through the body padding.
- The joystick base is capped by viewport height too
  (`min(272px, 100%, 52dvh)`) so the whole stick + HOME column
  stay on screen at 360px heights.
- Pick-a-white-point adjustments: entering pick mode no longer
  `scrollIntoView`s (the stage already *is* the screen; the scroll
  shoved the header off). The pick bar becomes a floating
  bottom-sheet over the stage instead of a flex sibling — its
  wrapped text would otherwise crush the picture and the pick
  layer to zero height — and the loupe's z-index rises above it,
  because the magnifier must never hide behind the panel. Loupe
  placement in landscape is computed against the **viewport**
  (above/below the finger as usual; on very short screens where
  neither fits, it goes *beside* the finger). Portrait keeps the
  original placement math untouched.
- PWA shell cache bumped to `ptz-shell-v3`.

Verification: `server/tests_landscape.py`, **18/18** — media
query + non-scrolling page + full-height stage + internal rail
scroll at 844x390; presets and Advanced (WB pick, Updates row)
reachable inside the rail; joystick fully on screen at 740x360;
update banner visible and tappable in landscape; loupe on-screen
mid-drag at 844x390 and in-viewport at five extremes at 700x260,
including a forced beside-the-finger placement; portrait and
desktop regression checks. The existing batteries still pass
(34/34 white-point, 57/57 updates). Previews:
`ui-preview-landscape-{buttons,joystick,presets,advanced,update,
wbpick}.png` (+ refreshed `ui-preview-{mobile,desktop}.png`).
This build ships through the staged-update system: staged on the
office PC, offered by the banner, applied only when the owner taps.

## Fullscreen / immersive video (added Nov 7, 2026)

The owner, using the app live in the service: tapping the video should
make the on-video "web stuff" disappear, and there should be a
proper fullscreen — the square button at the bottom right — where
a tap toggles between just-the-video and the controls, in portrait
or landscape. Implemented:

- **Tap toggles the chrome.** A genuine tap (short, <10px of
  movement — a drag never counts) on the video surface toggles the
  `chrome-off` class on the stage: the stream badge, the tier bar,
  and the fullscreen button fade out; tap again and they're back.
  In windowed view only those on-video overlays hide (the page
  header and rails stay — fullscreen is what removes those); the
  tier bar keeps its layout space so the page never jumps.
- **The button.** A 48px button at the video's bottom right
  (safe-area aware), expand-corners icon windowed, compress icon
  in fullscreen, shown whenever the chrome is shown. Clicks on it
  (or any real control) never double as a tap toggle.
- **Fullscreen target is the stage** — the whole video pane with
  its overlays — never the bare `<video>` (whose native fullscreen
  would strand our chrome). Entry paths, in order: (1) the
  Fullscreen API on the stage; (2) on iPhone, where element
  fullscreen doesn't exist, the video element's native fullscreen
  for the video tiers (the platform's own player takes over there);
  (3) a CSS *maximized* stage — fixed over the full viewport,
  topmost — for anything else, e.g. the snapshot tier on iPhone.
  All three exit back to exactly the prior layout. In fullscreen
  the tier bar becomes a bottom overlay over the picture, so
  hiding the chrome leaves literally just the video, letterboxed
  as large as the screen allows, portrait (full width, centered)
  or landscape (whole display). `F` toggles from the keyboard;
  `Esc` exits (natively for the API path, handled for the CSS one).
- **Auto-hide.** In fullscreen the chrome fades on its own after
  ~3 s without a touch; any touch inside the stage restarts the
  countdown, and a tap brings the chrome straight back.
- **Pick mode owns the pane.** While picking a white point there
  is no fullscreen button (CSS hides it under `.picking`) and no
  tap toggle — the loupe flow is untouched. The drive pad and
  joystick are separate elements and unaffected. The update
  banner/overlay live outside the stage, so they never render
  over fullscreen video (and sit below the maximized stage's
  z-index on the fallback path); they return on exit.
- PWA shell cache bumped to `ptz-shell-v4`.

Verification: `server/tests_fullscreen.py`, **26/26** — button
geometry (bottom-right, ≥44px) at portrait/landscape/desktop;
tap toggles the chrome off and on; a drag does not toggle; real
Fullscreen API enter/exit at 390x844 and 844x390 (stage fills the
viewport exactly, compress icon in fullscreen, windowed geometry
restored byte-for-byte after exit, and entering via the button
does not also trip the tap toggle); auto-hide after ~3 s with a
tap waking it; pick mode showing no button and ignoring taps.
The existing batteries still pass (34/34 white-point, 57/57
updates, 18/18 landscape). Previews:
`ui-preview-mobile-fsbtn.png`,
`ui-preview-fullscreen-{portrait-on,portrait-off,landscape-on}.png`.
Ships through the staged-update system like the landscape build —
it replaces that staged package (this build includes it), and the
banner offers this newer version instead.
