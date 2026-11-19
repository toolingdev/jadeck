# Staged, hash-verified updates

Built Nov 7, 2026, to the owner's design: *we push update code to a
directory on the PC; a button shows up in the webapp when an update
is staged; if we say yes, it performs a clean update* — with his
addition: a **hash check** so an accidental file, zip or tar that
lands in the staging folder can never be mistaken for an update.

The app never depends on reaching any server at update time. Gitea
is the repo's home and backup; the PC neither knows nor cares.

## Layout

The app resolves its install root from its own location (the folder
holding `server/` and `web/` — repo root in dev, `C:\PTZControl\app`
on the office PC). The updates folder is its sibling, overridable
with `PTZ_UPDATES_DIR`:

```
C:\PTZControl\updates\
  pending\            the staged package (manifest.json + payload\)
  applied\<version>\  packages that installed successfully
  rejected\           packages that failed verification or rolled back
  backup\<version>-<timestamp>\   the files each apply replaced,
                                   restored byte-for-byte on rollback
  last-result.json    the persisted outcome the app surfaces
  state.json          transient "applying" marker (written by the app,
                      cleared by the helper; ignored when stale)
  updater.log         the helper's step-by-step log
  updater_run.py      the helper copy the app launches (see below)
```

## Package format

Built by `tools/make_update_package.py` from the repo at HEAD:

```
<version>/
  manifest.json   {app: "ptzoptics-webapp", version: "<short commit>",
                   commit: "<full hash>", created: ISO timestamp,
                   files: [{path, sha256, size}, ...]}
  payload/server/**   the server tree (code only)
  payload/web/**      the web tree
```

- Paths in the manifest are payload-relative, forward slashes.
- `payload/server/VERSION` is written at package time
  (`{version, commit, created}`) and listed in the manifest like any
  other file; it is how the app knows what it is running. Builds from
  before the updater have no VERSION — the app reports
  "pre-updater build" and carries on.
- **No archives anywhere in the format.** A staged `.zip`/`.tar` is
  structurally not an update.
- Include/exclude rules for the payload: everything under `server/`
  and `web/`, except `__pycache__/` and `*.pyc`/`*.pyo`,
  `server/data/**` (**live data — the preset DB — is never in a
  package**), `server/tests_*.py`, and `server/VERSION` (regenerated).
- The tool refuses to package a dirty tree unless `--allow-dirty`:
  a package is the build its manifest claims.

## How a release is staged (Muse's side)

1. Commit the work in this repo (push to Gitea as usual).
2. `python3 tools/make_update_package.py` → `dist-update/<version>/`.
3. Copy the package's *contents* (`manifest.json` + `payload/`) into
   `C:\PTZControl\updates\pending\` on the office PC (same SSH file
   path as any other PC deploy).
4. The app notices on its next status check (≤45 s, or on load) and
   shows the banner. Nothing installs until someone taps **Update
   now** in the app.

## Detection and the hash check

`GET /api/update/status` (app side: `server/app/updates.py`) returns
one of `none | available | invalid | applying | applied |
rolled_back | failed`. A staged package is only ever `available`
after **full validation**, in this order:

1. **Structure** — the staging folder holds exactly `manifest.json`
   + `payload/`; anything else (a stray file, any archive) fails it.
   The manifest must parse, name this app, a version, a commit, and
   a non-empty file list.
2. **Path safety** — every listed path must be relative, use forward
   slashes, contain no `..`/`.`/empty segments, live under `server/`
   or `web/`, and not name data (`server/data/…`, `*.db`, …).
   Duplicates fail. Symlinks anywhere in the payload fail.
3. **Fingerprints** — the payload's file set must equal the
   manifest's exactly (an unlisted extra file fails the package),
   and **every file's size and SHA-256 must match**. The failure
   reason names the offending file.

An invalid package yields the `invalid` state with the reason in the
status response; the UI shows a quiet, dismissible notice — "A
staged update failed its safety check and was ignored." Never
silence, never partial trust.

## Applying — the clean update

`POST /api/update/apply`:

- **Guards first:** 409 while the camera is being driven, and 409
  while a sequenced camera operation (white-balance pick) holds the
  camera. 409 when no verified package is staged — which includes a
  package that has *become* invalid since it was first detected.
- The app copies `server/updater.py` to `updates/updater_run.py` and
  launches that copy **detached** (it double-detaches: the launcher
  re-spawns a worker and exits, so the worker is outside the app
  process's tree when the service stop arrives). The page switches
  to the "Updating…" overlay and polls for the app's return.
- The helper (`server/updater.py`, stdlib only, imports nothing from
  the app package — it runs while that code is stopped and
  half-swapped, and carries its own copy of the validator for the
  same reason; **keep the two validators in step**) then:
  1. **Re-verifies the whole package** — every hash, again. Bytes
     that changed after authorization stop the update here, before
     anything is touched; the package moves to `rejected/` and the
     result is `failed`. Detection-time trust is never enough.
  2. Backs up the current build's copies of exactly the files the
     swap can touch (new manifest ∪ previous manifest, from
     `.installed-manifest.json` at the install root) into
     `backup/<current-version>-<timestamp>/`.
  3. Stops the `PTZControlApp` service (`sc stop`) and confirms the
     app no longer answers before moving a single file. A service
     that won't stop aborts the update with the package left staged.
  4. **Manifest-driven swap only:** files in the new manifest are
     copied in; files in the previous manifest but not the new one
     are removed; the new manifest becomes `.installed-manifest.json`.
     Nothing else on disk is touched — the preset DB, the PC-local
     config, and the MediaMTX service are all outside the swap.
  5. Starts the service and polls `http://127.0.0.1:8080/api/status`
     (the app's own port) for up to 45 s (`PTZ_UPDATE_HEALTH_TIMEOUT_S`).
  6. **Success:** `pending/` moves to `applied/<version>/`,
     `last-result.json` records `applied`, and the reloaded page
     shows "Updated to version X."
  7. **Any failure:** the backup is restored byte-for-byte (files the
     new build added are removed, files it replaced or deleted are
     put back, the previous installed-manifest is restored), the
     service restarts, its health is confirmed, the package moves to
     `rejected/`, and the result records `rolled_back` with the
     reason. The page that rode out the restart shows "An update
     didn't take and was rolled back — you're still on the previous
     version. Nothing was changed."

Service control modes (helper): `windows` (default on Windows —
`sc` against `PTZ_SERVICE_NAME`, default `PTZControlApp`), `command`
(`PTZ_SVC_MODE=command` with `PTZ_SVC_STOP_CMD` / `PTZ_SVC_START_CMD`
— how the sandbox battery drives a stand-in service), `none` (dev:
file swap only, no service control).

## Safety invariants

- Hash verification happens **twice**: at detection and again inside
  the helper before any file moves.
- Replacement is manifest-driven only; directories are never
  wholesale-deleted; files in no manifest (the preset DB above all)
  are untouchable, and the validator refuses manifests that name
  data files at all.
- No archives, no path traversal, no symlinks, no extra files.
- No update while the camera is being driven.
- Automatic rollback, verified by a health check on the restored
  build, with the outcome persisted and surfaced in the UI.
- The stream gateway (MediaMTX) is a separate service and is never
  part of an app update — the video path doesn't blink.

## UI

Identical on phone and desktop, dark theme, plain language:

- A slim banner appears **only** when there is something to say:
  `available` ("An update is ready — version X. It has already
  passed its safety check." + **Update now** / **Later**; Later
  dismisses for the session), `invalid` (the quiet ignored-package
  notice), and the persisted outcome after an apply (applied /
  rolled back / failed, dismissible).
- During an apply, the overlay ("Updating… the app will reconnect on
  its own") polls the status endpoint; on `applied` the page reloads
  itself onto the new build; on `rolled_back`/`failed` it lifts the
  overlay and shows the outcome banner.
- **Advanced settings → Updates** carries the same facts in a
  predictable place: current version, staged state in words, and
  the same Update button.
- This build also bumps the PWA shell cache (`web/sw.js`,
  `ptz-shell-v2`) so updated web files actually reach installed
  clients; future releases that change `web/` should bump it again.

## Bootstrap note

The build currently on the office PC predates the updater (no
VERSION file, no `.installed-manifest.json`). Its replacement goes
out by the existing manual PC deploy. From that build on, updates
flow through this system; the first staged apply treats the previous
manifest as empty (replaces only what the new manifest lists,
deletes nothing) — the safe default.

## Tests

`server/tests_update.py` (57 checks, all against mock/local layouts):

- Validation: valid package; one flipped byte (file named); extra
  unlisted file; a lone `.zip`; `../` and backslash manifest paths;
  a manifest naming the preset DB; a missing payload file; a
  symlink in the payload.
- Packaging tool on a temp git repo: code trees packaged, VERSION
  written and listed, tests + data excluded, output passes the app
  validator, dirty tree refused, `--allow-dirty` override.
- Full apply against a fake install root (real subprocess service,
  command-mode stop/start): apply-while-driving → 409; new version
  live; payload swapped byte-for-byte; backup holds the old version;
  **the preset DB is byte-identical and its presets survive**; the
  service really restarted.
- Corrupted after authorization: byte flipped in the window between
  the apply call and the helper's start → the **helper's**
  re-verification fails it, the service never restarts, the install
  tree is untouched, the package is rejected, the failure surfaces.
- Bad new version (fingerprints honest, code broken): automatic
  rollback restores the install tree byte-for-byte, no
  installed-manifest is left behind, the old service is healthy,
  and the outcome reads `rolled_back` with the reason.
- UI over CDP with canned states: available banner (mobile), the
  Advanced → Updates row (desktop), the updating overlay
  (simulated), and the rolled-back outcome banner (mobile).
  Previews: `ui-preview-{mobile,desktop}-update.png`,
  `ui-preview-desktop-updating.png`,
  `ui-preview-mobile-update-rolledback.png`.
