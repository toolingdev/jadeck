> **Now app-managed (Nov 18, 2026):** the controller downloads (pinned
> sha256), configures and supervises MediaMTX itself from Settings → Live
> video, see SETUP.md. This folder's `mediamtx.yml` is only for running
> your own gateway (Live video → Advanced → built-in gateway off).

# Stream gateway (MediaMTX)

**Component:** MediaMTX **v1.21.3** — verified latest stable release on
GitHub (bluenviron/mediamtx, released 2026-10-25; checked 2026-11-07).
Single static binary, no runtime dependencies; Windows build available
(`mediamtx_v1.21.3_windows_amd64.zip` from the GitHub release).

## Why it exists

Browsers cannot play the camera's RTSP feed. MediaMTX pulls RTSP from the
camera once and re-serves it in browser-native formats:

| Tier | Path | Latency | Used by |
|---|---|---|---|
| 1 | WebRTC/WHEP `http://<pc>:8889/cam/whep` | sub-second | the controller UI, primary |
| 2 | HLS `http://<pc>:8888/cam/index.m3u8` | ~1–3 s (low-latency variant) | Safari/iOS fallback in the UI |
| 3 | `/api/snapshot` (service-proxied camera `snapshot.jpg`) | stills, 0.9 s refresh | last-resort fallback in the UI |

Low latency matters here: the owner's requirement is that control *feels*
smooth, and a multi-second HLS delay makes press-and-hold PTZ feel broken
even when the control path itself is perfect. WHEP + the server's
coalesced drive loop are designed as a pair.

Paths: `cam` = camera stream 1 (HD), `cam_sd` = stream 2 (SD, for weak
Wi-Fi). The UI's HD/SD switch just points the player at the other path.

## Running it (on the office PC, later — not part of Phase 2)

1. Unzip the MediaMTX Windows build next to this folder.
2. Copy `mediamtx.yml` beside `mediamtx.exe`.
3. Run `mediamtx.exe mediamtx.yml` (later: register as an auto-start
   service alongside the controller, same as the FastAPI service).

Nothing here is installed or started by Phase 2 — config only.

## Alternative considered

**go2rtc** — also excellent and lighter on features we don't need; its
WebRTC path works the same way. MediaMTX won on: single-binary Windows
support, WHEP as a first-class endpoint, and the low-latency HLS variant.
If MediaMTX ever misbehaves with this camera's RTSP, go2rtc is a drop-in
replacement behind the same `/api/config` URLs.
