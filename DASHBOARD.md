# The unifying dashboard

Built Nov 7, 2026, to the owner's design after he approved the mockup:
one page that shows **the stream, the slides, and a quick camera
control** — plus, at his addition, **mute/unmute for his 8 favorite
channels** on the Behringer WING. Everything on it already existed
in the room; the dashboard just puts it in one place.

## Views, not a merge

The app is two views over the one service, with a switcher in both
headers:

- **Camera** (`/`) — the full camera control, exactly as built:
  drive modes, presets with editing, advanced settings, the
  white-point pick and its loupe, updates. Untouched by this work
  beyond gaining the switcher.
- **Dashboard** (`/dashboard`) — the new page. Its camera presence
  is deliberately only the *quick strip* (mini feed, preset recall,
  hold-to-drive pad, zoom, home, stop); nothing from the Camera
  view was absorbed or rebuilt.

Layout is iPad-first (the booth device), worked down to iPhone and
up to desktop: monitors on top (program wider than confidence),
three decks (vMix / Proclaim / camera), the WING strip, a macros
deck, and an action log — every button press lands in the log in
plain words, the same idea the mockup demonstrated.

## The two video panes are NDI you already have

Nothing was rebuilt to fill the panes:

- **Program** = vMix's NDI program output — what the congregation
  sees.
- **Confidence** = Proclaim's NDI confidence-monitor output —
  current slide and next slide are already drawn into that one
  feed.

The browser can't drink NDI directly, so the panes play the
gateway paths named `program` and `confidence` through the same
WHEP → HLS ladder as the camera feed — except there is no snapshot
tier for these, so a dry path shows an honest **no-signal** pane
with a Retry button instead of a black box.

### The gateway finding — read this before touching the feeds

**MediaMTX cannot ingest NDI. Not the pinned v1.21.3, not any
version.** Verified 2026-11-07 against upstream: NDI is absent
from MediaMTX's protocol list and the feature request
(bluenviron/mediamtx#1014) has been open since 2022. So the two
paths in `stream/mediamtx.yml` are **config-gated**: they exist as
publisher reservations (valid config, gateway healthy, camera
paths untouched), and the panes wait in their no-signal state.

Turning the feeds on is therefore a separate, deliberate step —
The owner's call, not something to smuggle into an app update (and the
gateway isn't part of app updates anyway; see UPDATE.md):

1. an NDI-capable gateway in MediaMTX's seat, or
2. a small NDI→RTSP bridge on the office PC publishing into the
   `program` / `confidence` paths (RTSP publish is already valid
   config — the panes light up with zero app changes), or
3. a future MediaMTX that grows NDI support, if it ever does.

Until then the panes are dark *by configuration*, and everything
else on the dashboard — all three control bridges and the camera
strip — is fully functional without them.

## Three control bridges, three native doors

Each deck talks to its system the way that system wants to be
talked to, through the service on the office PC. The page never
touches hardware directly.

### vMix — HTTP API (`server/app/vmix.py`)

vMix's Web Controller (enable it in vMix: Settings → Web
Controller; default port 8088, same PC — `PTZ_VMIX_HOST` /
`PTZ_VMIX_PORT` to move it):

- `GET /api/vmix/status` — inputs with numbers/titles, preview,
  program, streaming/recording, parsed from vMix's `/api/` XML.
- `POST /api/vmix/preview {input}`, `POST /api/vmix/cut`,
  `POST /api/vmix/fade {durationMs}` — the deck's buttons. Every
  action returns the fresh status, so the tally colors on the page
  are vMix's own state, not optimism.

### Proclaim — MIDI bridge (`server/app/proclaim.py`)

Proclaim has **no HTTP API** (verified: even the Companion
ecosystem has no Proclaim module for this reason). Its control
door is MIDI in, so the service acts as a tiny MIDI controller:
`POST /api/proclaim/{prev|next|black|logo|clear}` sends one tapped
note on a virtual MIDI port (loopMIDI or similar) on the PC, and
Proclaim follows the note it was taught.

Default note map (channel and every note overridable —
`PTZ_MIDI_CHANNEL`, `PTZ_MIDI_NOTE_*`, port matched by name
substring via `PTZ_MIDI_PORT`):

| Action    | Note |
|-----------|------|
| Prev      | 60   |
| Next      | 62   |
| Black     | 64   |
| Logo      | 65   |
| Clear     | 66   |

**Setup (two minutes, on the PC, once):** install a virtual MIDI
port (e.g. loopMIDI, port name e.g. "Proclaim Control"), then in
Proclaim's MIDI settings teach each slide action the note above —
press the dashboard button while Proclaim is listening, or enter
the note directly where the version allows. If you change a note
in Proclaim, change the matching `PTZ_MIDI_NOTE_*` to agree.

The sender hides behind an interface: real winmm MIDI out via
ctypes on Windows; a recording stand-in in mock mode and wherever
MIDI can't open (status reports the recorder honestly instead of
pretending a port exists).

### WING — OSC (`server/app/wing.py`)

The console speaks OSC over UDP 2223 and replies to the caller.
Mute is an integer at `/ch/<n>/mute` (1 = muted); a bare address
reads it back. The bridge implements exactly the owner's ask —
**mute/unmute on 8 favorite channels** — and nothing else:

- `GET /api/wing/status` — probe (`/?`) + each slot's state,
  read from the console.
- `POST /api/wing/mute {slot, muted?}` — set, or toggle when
  `muted` is omitted; the answer is the state **read back** after
  the write, so the red MUTE button means the desk really muted.

The slots are config, not code, and they ship **assigned**:
The owner named his eight on Nov 8, 2026 (names verified against his
console's snapshot), in this display order:

| Slot | Name          | Channel |
|------|---------------|---------|
| 1    | MAIN          | 1       |
| 2    | WIRELESS GRAY | 2       |
| 3    | WIFI MIC 2    | 3       |
| 4    | VOX BK 1      | 7       |
| 5    | VOX BK 2      | 8       |
| 6    | ZGTAR         | 10      |
| 7    | MONTAGE       | 12      |
| 8    | AUDIENCE      | 6       |

The strip lays out 4 × 2 on iPad and desktop, 2 × 4 on phone
(full parity; on phone the channel label drops under the name
rather than truncating). To change the picks, replace the whole
list on the PC: `PTZ_WING_CHANNELS="1,2,3,7,8,10,12,6"` and
`PTZ_WING_NAMES="MAIN|WIRELESS GRAY|WIFI MIC 2|VOX BK 1|VOX BK 2|ZGTAR|MONTAGE|AUDIENCE"`.
An entry blanked in `PTZ_WING_CHANNELS` leaves that slot
unassigned: it renders as a placeholder and a mute attempt
answers 409 with the reason. Console address: `PTZ_WING_HOST`
(its IP on the venue LAN), `PTZ_WING_PORT` (default 2223).

## Macros — chained actions on the WING (`server/app/macros.py`)

Added Nov 9, 2026, at the owner's direction: *the ability to build
macros in the software, so we can chain functions, add delays
etc.* A macro is a name plus an ordered list of steps, built in
the Dashboard view (or authored as JSON — the contract is
**MACROS.md** at the repo root), and run with one tap. V1 steps:

- **`wing_mute`** — set any console channel's mute, addressed by
  favorite name (`MAIN`, …) or by channel number 1–48;
- **`delay`** — wait 0–60000 ms between steps.

**Safety semantics** (these steps move live audio): steps run
sequentially; every mute step is set through the bridge above and
then **read back from the console** — the step counts only if the
read-back matches; any failure (bridge error, mismatch, a step
over 10 s, a run over 180 s) **stops the macro at that step** and
the run report names the step and the reason; one run at a time;
the engine is idle again after every run. The per-step ✓/✗ report
is stored as the macro's `lastRun` and shown on its card.

Macros live in their **own store** (`server/data/macros.db`,
beside — never inside — the preset DB; like all of `server/data/`
it is live data and never enters an update package).

**Seeded:** the PC ⇄ Mic swap as a toggle pair ("PC ⇄ Mic: PC
live" / "PC ⇄ Mic: Mic live"), each unmuting what should be live
before muting the other. MAIN is channel 1, a verified favorite.
**The PC channel (13) is a PLACEHOLDER** — the owner recalled it as
"channel 13 or something", unverified; both seed descriptions
say so in as many words. At the first live session, verify the PC
channel *by name* on the console, then edit the seeds to the real
number. Nothing anywhere presents 13 as confirmed.

**Later, by design (not built):** vMix and Proclaim steps slot
into the same step model as new step types — e.g.
`{"type": "vmix_cut"}` or `{"type": "proclaim", "action":
"next"}` — validated by the same validator, executed by the same
engine through the existing bridges, with the same
stop-on-failure rule. One honest caveat to design around when
that day comes: neither bridge can verify like the WING can
(vMix actions return fresh status, which is close; a Proclaim
MIDI note is fire-and-forget with nothing to read back), so those
step types will carry a weaker verification story than
`wing_mute`'s, and the report should say so per step.

## Mock mode

`PTZ_MOCK=1` stands in all three bridges (the mockup's four vMix
inputs, a recording MIDI out, a fake console holding the owner's 8
favorite channels — and, like the real desk, all 48 channels, so
macros can address any of them) next to the mock camera, so the
entire dashboard is exercisable with no hardware anywhere near —
which is also how the battery verifies it.

## Tests

`server/tests_dashboard.py`:

- **Units:** the OSC codec byte-for-byte; the vMix XML parser on
  canned state; the MIDI note DWORD packing; the WING slot config
  parsing (8 slots, partial lists, the shipped defaults and the
  env override that replaces them).
- **Wire-level fakes:** the real WING client against a fake
  console over an actual UDP socket (get/set/toggle/probe); the
  real vMix client against a fake Web Controller over HTTP
  (status parse + recorded function calls).
- **Endpoints** against the mock-mode service: statuses, preview →
  cut → fade state flow, Proclaim next = Ch 1 · Note 62, WING mute
  set + read-back and toggle on each of the 8 slots (none of them
  409), unassigned-slot 409, out-of-range slot 422,
  `/api/config` carrying the program/confidence URLs, and both
  views served with the switcher.
- **UI over CDP** (iPad, desktop, phone): panes settle into
  no-signal without a gateway; the vMix deck previews and CUT
  moves the program tally; Proclaim Next flashes the note readout;
  a dashboard preset recall reaches the mock camera; the WING
  mute flips and flips back; no horizontal overflow on the phone.
  Previews: `ui-preview-dashboard-{ipad,desktop,mobile}.png`.

`server/tests_macros.py` (81 checks):

- **Validator** — the whole MACROS.md rulebook: favorite
  resolution (case-insensitive), every range and type rule,
  unknown fields/types refused with step-numbered messages.
- **Store** — seeds land exactly once with the placeholder note;
  CRUD, case-insensitive duplicate refusal, run recording; the
  macro DB and the preset DB provably hold only their own tables.
- **Engine** — step ordering and real delay elapsed time;
  stop-on-failure (later steps never execute); per-step timeout
  and run deadline; a second concurrent run refused; state reset
  proven by the run after a failure; the seeded pair flipping
  real state on the mock console both directions; the WING
  service's own read-back mismatch raising instead of passing.
- **Endpoints** against the mock-mode service: CRUD, run reports
  persisted as `lastRun`, import (single / list / wrapper,
  all-or-nothing with per-macro errors, `validateOnly` writing
  nothing).
- **UI over CDP** (phone, desktop): the seeded cards, a seeded
  run landing all-green with per-step chips, a macro built in the
  editor, a macro imported by pasting JSON, no horizontal
  overflow. Previews: `ui-preview-dashboard-macros-{mobile,desktop}.png`.

## Verified / unverified (flying blind, stated plainly)

The venue PC, camera, vMix, Proclaim and the console were all
**off** while this was built. Everything above is proven in
mock/local mode only. Still unverified on real hardware, in the
order a first power-on session should burn them down:

1. vMix Web Controller answering on the PC + the function calls
   landing (one preview, one cut).
2. The virtual MIDI port existing on the PC, Proclaim taught the
   note map, and a Next press advancing a real slide.
3. The WING's address on the venue LAN + one mute read/set
   against the real desk. (The channels themselves are named:
   the owner picked his 8 on Nov 8, 2026 — the table above.)
4. The NDI feeds — blocked on the gateway decision above, by
   configuration, not by code.
