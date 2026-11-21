# Install and set up

Everything is set up from the browser. Nobody needs to edit a file or
open a terminal after the one install command.

## Install (one line per OS)

**Windows**: open PowerShell **as Administrator** (right-click PowerShell,
"Run as administrator"), then run:

```powershell
irm https://github.com/toolingdev/jadeck/releases/latest/download/install.ps1 | iex
```

**macOS or Linux**: open Terminal, then run:

```bash
curl -fsSL https://github.com/toolingdev/jadeck/releases/latest/download/install.sh | sh
```

The release build points the installers at the repository it was built
from. Each installer:

- downloads the latest release for that computer;
- checks its SHA-256 against the release's `SHA256SUMS`;
- installs it as a service that starts with the computer and restarts if it
  stops (Windows service, launchd or systemd);
- on Windows, opens the firewall for the app and its video ports on
  private networks only;
- opens the setup wizard at `http://localhost:8080/setup`.

Run the same command again to reinstall. Normal updates come from
**Settings → System → Updates**.

Install folders: `C:\Program Files\PTZ` on Windows, `/usr/local/ptz` on
macOS, `/opt/ptz` on Linux. Everything the app saves is in the `data`
folder inside the install folder.

## First-run wizard

The wizard opens by itself until it is finished or skipped:

1. **Welcome**
2. **Camera**: cameras found on the network are listed. Tap one, or type
   an IP address. **Test** shows the camera's own picture and checks
   control (VISCA), picture (snapshot) and login.
3. **Switcher & slides**: vMix, Proclaim and others. Optional.
4. **Sound**: Behringer WING and others. Optional.
5. **Dashboard**: the default dashboard for phones, tablets and desktops.
6. **Done**: the addresses to open on phones, and a QR code.

To run it again, use the link at the bottom of Settings.

## Settings

| Section | What it holds |
|---|---|
| Cameras | Any number of cameras: add by IP or from the network scan, test, edit, remove, and pick the main camera. Presets belong to their camera. Macros and agents use the main camera. |
| Connections | Video switchers, slides and mixers, any number of each, through add-ons (see DEVICES.md). |
| Video feeds | Pictures dashboards can show: each camera, plus feeds sent here by SRT (vMix Program, Slides…) or pulled from an address; the stream gateway (MediaMTX) settings. |
| Add-ons | What's installed, what each adds, updates, install from a file (see ADDONS.md). |
| System | Port and network name (`ptz.local`), who can use it without a key, and updates (channel, automatic install, release source). |
| Dashboards, API keys, Audit | As before. |

Changes apply live, with no restart. Changing the port moves the
controller to the new port, and open screens are sent to the new
address.

Camera passwords and the update token are kept on this PC only, in
`data/settings.json`. On macOS and Linux the file is mode 0600. On Windows
the installer limits the `data` folder to SYSTEM and Administrators. The
browser never receives them back.

## Live video

Browsers can't play a camera's RTSP feed, so the app runs MediaMTX, which
pulls RTSP once and serves WebRTC (under a second of delay) and HLS
(Safari fallback). **MediaMTX is built into the release.** There is
nothing extra to download or install. On start, the app unpacks it to
`data/video/bin`, writes its config from Settings, runs it, and restarts it
if it stops. An app update brings the new MediaMTX with it. Each bundled
copy is checked against a pinned SHA-256 when the release is built (see
THIRD_PARTY_NOTICES.md).

Development builds (and Windows on ARM, which has no MediaMTX build) don't
include it. There, **Settings → Video feeds → Download & start** fetches the
same pinned version.

Already running your own MediaMTX? Go to **Video feeds → Stream gateway → Advanced** and
switch off the built-in gateway. Then type the IP of the PC that runs it.

Video ports: 8889 (WebRTC), 8888 (HLS), UDP 8189 (WebRTC media), UDP 8890
(SRT in, only when a feed is sent here; publishing needs the feed's key), TCP 8554 (RTSP), and 127.0.0.1:9997 (the
gateway's control API, this PC only).

### Where the work happens

| Step | Who | Network |
|---|---|---|
| Encode the camera picture | The camera (HD and SD streams at once, on its own chip) | |
| Camera → controller PC | MediaMTX pulls RTSP, **once per feed**, only while someone watches | TCP |
| Encode the program feed | vMix (its SRT output) | |
| vMix → controller PC | SRT | UDP 8890 |
| Controller PC → screens | MediaMTX repackages, **no re-encoding** (tiny CPU) | WebRTC: UDP 8189 · HLS: TCP 8888 |
| Decode | Each phone / iPad / PC (hardware video) | |

Everything is one-to-one (no multicast). The cost of many video widgets
is Wi-Fi and the viewing device, so the app keeps it down by itself:

- Several widgets showing the same feed on one screen share **one**
  connection.
- A feed nobody can see — the screen off, the tab hidden, the widget
  scrolled away — **pauses** and resumes when it's visible again.
- Live view's **Auto** quality (the default) uses HD when it's the only
  video on the screen and the camera's lighter SD stream when there are
  several, or on a phone.
- The **Video bandwidth** widget (Metrics group) shows it live: per feed,
  who's watching, and the vMix SRT link's delay and loss.

### Sending the program (or any vMix output) by SRT

Add a feed under **Settings → Video feeds → Add feed → Sent to this
controller**, named e.g. Program. Each feed sent here has its own
**publish key**: the gateway takes a stream only from a sender that gives
it, so nobody else on the network can push a picture into your feeds
(watching needs no key). The feed's panel shows the address with the key,
e.g. `srt://192.168.1.20:8890?streamid=publish:program:program:<key>`, with
copy buttons and **New key** (the old key stops working). (Proclaim only
sends NDI: add its NDI as a vMix input and send a vMix output here the same
way.) In vMix: **Settings → Outputs / NDI / SRT → Output 1 (Program) → ⚙ →
SRT**, type **Caller**, paste the address's host and port, and set the
stream ID to the one shown (`publish:program:program:<key>`). A modest
setting is plenty for phones: 1280×720, H.264, 2–3 Mbps, latency 200 ms.
This is separate from the YouTube stream; it doesn't change it. The
confidence feed works the same way with its own stream ID. RTSP senders
(ffmpeg, encoders) use `rtsp://program:<key>@192.168.1.20:8554/program`.

Upgrading from a version without keys: every feed sent here gets a key on
first start, and senders set up before must be given the new stream ID
(or address) once.

## Moving from env files

On first start, an existing `ptz.env` seeds the settings once. It carries
over `PTZ_CAMERA_HOST`, `PTZ_CAMERA_USER` and `PTZ_CAMERA_PASS`, the camera
ports, `PTZ_TRUST_LAN`, `PTZ_TRUSTED_CIDRS` and `PTZ_UPDATE_REPO` or
`PTZ_UPDATE_TOKEN`. The existing `presets.db` stays with that camera. After
that, Settings is the source of truth.

`PTZ_PORT`, `PTZ_BIND` and `PTZ_DATA_DIR` still win when they are set,
which is meant for development. Settings then shows the port as fixed.
`ptz --mock` runs two simulated cameras: one already set up, and one
waiting to be found by Add camera. `PTZ_MOCK_FRESH=1` starts with no
camera, so you can try the wizard.
