# Diagnosing problems

The controller keeps a log you can read from anywhere on a trusted
network (the venue network or Tailscale), even when nobody is at the PC.

## Where the log is

- **Files:** `<data folder>/logs/ptz.log`, rotated at 5 MB, keeping
  `ptz.1.log` … `ptz.5.log`. On Windows the data folder is
  `C:\Program Files\PTZ\data`. They survive restarts and the PC being
  switched off.
- **Memory:** the last 4,000 lines, queryable over the network.
- **Updater:** `<data folder>/updates/helper.log` (what happened during an
  update swap).

Passwords, tokens and keys are never written.

## What gets logged

- Start-up: version, platform, data folder.
- Cameras, vMix, slides and the mixer: when each starts or stops
  answering, and when it comes back (once per change, with the reason).
- Every change made through the API (camera moves aside) and every
  failed request, with the reason given to the browser and the client's
  address.
- Settings changes: what changed (never the values of secrets).
- Stage: messages sent (channel, from, text, who was online), seen,
  timers, channels, and screens connecting / disconnecting.
- Macro runs and where a failed one stopped.
- Updates being applied.
- The stream gateway's warnings and errors.
- **Browser errors:** a page or widget that crashes on any phone or iPad
  reports itself (page, screen name, error, stack). A crashed widget shows
  "hit a problem" and the rest of the screen keeps working.
- Any server crash, with its stack trace.

## Reading it remotely

From any trusted address (e.g. over Tailscale to the office PC):

```
GET  /api/diag                                everything at a glance: version, uptime, cameras,
                                              device health, video, stage screens, last 50 problems
GET  /api/diag/health                         small: up/down per device, problems in the last hour
GET  /api/diag/video                          live bandwidth: per feed, per viewer, vMix SRT link
GET  /api/diag/logs?level=warn&q=vmix&limit=200   recent lines; level, text search, after=<seq>
GET  /api/diag/logs/file?name=ptz.1.log&tail=2000  a log file from disk (or name=helper.log)
POST /api/diag/level {"level":"debug"}        more detail until the next restart
POST /api/diag/report {"note":"tally stuck"}  save a snapshot + the recent log as a report
GET  /api/diag/reports                        saved reports (newest first, last 100 kept)
GET  /api/diag/reports/{name}
```

Reports are written to `<data folder>/reports/`.
