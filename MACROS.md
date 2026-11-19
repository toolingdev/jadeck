# Macros — the authoring contract

Built Nov 9, 2026, to the owner's ask: *software-side macros, so we
can chain functions, add delays etc.* A macro is a named, ordered
list of steps that runs against the Behringer WING from the
dashboard. This file is the complete contract for authoring one —
a person, a script, or an AI with nothing but this document and
HTTP access to the app can create a valid macro, import it, and
run it. The dashboard's builder UI writes through the exact same
endpoints and validator described here; there is one set of rules.

Base URL: the app itself — `http://<office-pc>:8080` on the venue
LAN (in mock mode, wherever `server/run.py --mock` is serving).

## The macro document

```json
{
  "name": "Sermon time",
  "description": "Optional notes, up to 300 characters.",
  "steps": [ /* 1..64 steps, run in order */ ]
}
```

| Field         | Type   | Rules |
|---------------|--------|-------|
| `name`        | string | Required. 1–60 characters after trimming. Unique across the store, compared case-insensitively. |
| `description` | string | Optional, default `""`. At most 300 characters. Free text — say what the macro is for, and flag any placeholder values in it. |
| `steps`       | array  | Required. 1–64 steps. |

No other fields are allowed — an unknown field anywhere (macro,
step, anywhere) is a validation error, never silently ignored.
Types are strict JSON types: a boolean is `true`/`false`, never
`1`; a number that must be an integer rejects `1.5` and `"3"`.

## Step types (v1)

### `wing_mute` — set a channel's mute on the console

```json
{"type": "wing_mute", "channelName": "MAIN", "muted": true}
{"type": "wing_mute", "channel": 13, "muted": false}
```

| Field         | Type    | Rules |
|---------------|---------|-------|
| `type`        | string  | Exactly `"wing_mute"`. |
| `channel`     | integer | Console channel, 1–48. One of `channel` / `channelName` is required. |
| `channelName` | string  | A favorite's name (table below), matched case-insensitively. If both are given they must agree. |
| `muted`       | boolean | Required. `true` = mute the channel, `false` = unmute (make it live). |

A `channelName` resolves to its channel **when the macro is
saved**; the stored step carries both the canonical name and the
resolved number, and the number is what runs. Naming a favorite
that is unassigned (no channel yet) is a validation error.

### `delay` — wait between steps

```json
{"type": "delay", "ms": 1500}
```

| Field  | Type    | Rules |
|--------|---------|-------|
| `type` | string  | Exactly `"delay"`. |
| `ms`   | integer | Milliseconds to wait, 0–60000. |

## The favorites (for `channelName`)

The owner's eight, as shipped in the app config — slot order is
display order. Any console channel 1–48 may also be addressed by
number; the favorites exist so macros can say what they mean.

| Name          | Channel |
|---------------|---------|
| MAIN          | 1       |
| WIRELESS GRAY | 2       |
| WIFI MIC 2    | 3       |
| VOX BK 1      | 7       |
| VOX BK 2      | 8       |
| ZGTAR         | 10      |
| MONTAGE       | 12      |
| AUDIENCE      | 6       |

**Never invent a channel number.** If a request names a sound
source that is not in this table and its channel is not otherwise
verified, stop and flag it — a guessed number mutes the wrong
thing on a live console. Known placeholder: the PC input is
seeded as channel **13**, which the owner recalled as "channel 13 or
something" — **unverified**. Macros using it must say so in their
`description` until it is confirmed by name at a live session.

## Safety semantics (what the engine guarantees)

These steps move live audio, so the engine is deliberately
stubborn:

- **Sequential.** Steps run one at a time, in order. Nothing is
  parallel, nothing is queued behind a run.
- **Read-back verification.** Every `wing_mute` step sets the
  value through the WING bridge and then reads the channel back
  from the console. The step counts only if the read-back matches
  what was set.
- **Stop on failure.** A bridge error, a read-back mismatch, a
  step that takes over **10 s**, or a whole run that passes
  **180 s** stops the macro at that step. Later steps do not run.
  There are no silent retries.
- **One run at a time.** Starting a run while another is in
  flight is refused (HTTP 409). The engine returns to idle after
  every run — success, failure, or exception.
- **Every run leaves a report** (below), stored as the macro's
  `lastRun` and shown on its dashboard card.

A run that stops on a failed step still answers **HTTP 200** —
the run happened; the report's `ok: false` is the failure.
HTTP errors are only for the request itself: unknown macro
(404), a second concurrent run (409), an invalid document (400).

### The run report

```json
{
  "macroId": 3, "name": "Sermon time", "ok": false,
  "failedStep": 2, "error": "step 2 failed: channel 8 read back live after being set muted — the console did not take the change",
  "startedAt": 1794268800.12, "finishedAt": 1794268801.03,
  "steps": [
    {"step": 1, "type": "wing_mute", "label": "Mute VOX BK 1 · Ch 7",
     "ok": true, "detail": "channel 7 read back muted", "ms": 14},
    {"step": 2, "type": "wing_mute", "label": "Mute VOX BK 2 · Ch 8",
     "ok": false, "error": "channel 8 read back live after being set muted — the console did not take the change", "ms": 12}
  ]
}
```

(`startedAt`/`finishedAt` are epoch seconds; `ms` per step is
wall-clock milliseconds. Steps after `failedStep` are absent —
they did not run.)

## Endpoints

| Endpoint | Does |
|----------|------|
| `GET /api/macros` | List all macros (with `lastRun`). |
| `GET /api/macros/{id}` | One macro. 404 if absent. |
| `POST /api/macros` | Create from a macro document. 201; 400 with the validation message; 409 on a duplicate name. |
| `PATCH /api/macros/{id}` | Replace any of `name` / `description` / `steps` (the merged document is validated whole). |
| `DELETE /api/macros/{id}` | Delete. |
| `POST /api/macros/{id}/run` | Run it; answers the run report. |
| `POST /api/macros/import` | Import one macro, a list, or `{"macros": [...]}` — see below. |
| `GET /api/macros/engine` | `{"running": bool, "current": {...} \| null}`. |

A macro as the API returns it:
`{id, name, description, steps (normalized), sort, lastRun,
createdAt, updatedAt}` — timestamps are epoch seconds.

### Import — the programmatic door

`POST /api/macros/import` accepts any of:

```json
{ "name": "…", "description": "…", "steps": [ … ] }
[ { "name": "…", "steps": [ … ] }, { "name": "…", "steps": [ … ] } ]
{ "macros": [ { "name": "…", "steps": [ … ] } ], "validateOnly": false }
```

- **All-or-nothing.** Every macro is validated, and name-checked
  against the store and its siblings in the same import, before
  any is written. One bad macro refuses the whole batch.
- **Failures are precise.** A refusal is HTTP 400 with
  `{"detail": "…", "errors": [{"index": 0, "name": "…", "error":
  "step 2: unknown favorite channel name 'PC' — the favorites
  are: MAIN, …"}]}` — the index of each bad macro and the exact
  rule it broke.
- **Validate without writing:** add `"validateOnly": true` to
  the wrapper object, or `?validateOnly=true` to the URL. A valid
  dry run answers `{"valid": true, "validateOnly": true,
  "macros": [ …the normalized documents… ]}` and stores nothing.
- Success answers 201 with `{"imported": [ …the stored macros… ]}`.

## Worked example 1 — the seeded PC ⇄ Mic swap

Ships in the store on first run; shown here exactly as authored
(the stored form resolves `MAIN` to `"channel": 1` alongside the
name). Two macros, one per direction, each unmuting what should
be live *before* muting the other — no dead air.

```json
{
  "macros": [
    {
      "name": "PC ⇄ Mic: PC live",
      "description": "Seeded swap, one of a pair. The PC channel (13) is a PLACEHOLDER — it is unverified; verify the PC channel by name at the first live session, then edit this macro. MAIN is channel 1, a verified favorite.",
      "steps": [
        {"type": "wing_mute", "channel": 13, "muted": false},
        {"type": "wing_mute", "channelName": "MAIN", "muted": true}
      ]
    },
    {
      "name": "PC ⇄ Mic: Mic live",
      "description": "Seeded swap, one of a pair. The PC channel (13) is a PLACEHOLDER — it is unverified; verify the PC channel by name at the first live session, then edit this macro. MAIN is channel 1, a verified favorite.",
      "steps": [
        {"type": "wing_mute", "channelName": "MAIN", "muted": false},
        {"type": "wing_mute", "channel": 13, "muted": true}
      ]
    }
  ]
}
```

## Worked example 2 — a multi-step macro with delays

"Sermon time": the band comes down in two moves with a breath
between them, then the speaker's mic and the translator's mic come
up. Authored entirely by favorite name — no raw numbers to get
wrong.

```json
{
  "name": "Sermon time",
  "description": "Band down, speaker and translator up, with a short settle between moves.",
  "steps": [
    {"type": "wing_mute", "channelName": "VOX BK 1", "muted": true},
    {"type": "wing_mute", "channelName": "VOX BK 2", "muted": true},
    {"type": "wing_mute", "channelName": "ZGTAR", "muted": true},
    {"type": "wing_mute", "channelName": "MONTAGE", "muted": true},
    {"type": "delay", "ms": 1000},
    {"type": "wing_mute", "channelName": "MAIN", "muted": false},
    {"type": "delay", "ms": 250},
    {"type": "wing_mute", "channelName": "WIRELESS GRAY", "muted": false}
  ]
}
```

Importing and running it from a shell:

```sh
curl -s -X POST http://<office-pc>:8080/api/macros/import \
  -H 'Content-Type: application/json' -d @sermon-time.json
curl -s -X POST http://<office-pc>:8080/api/macros/3/run
```

## Mock mode — rehearse here first

`server/run.py --mock` stands in a fake console that knows all
48 channels, so the full flow — author, validate, import, run,
read the per-step report — works with no hardware anywhere near.
The dashboard battery (`server/tests_macros.py`) exercises the
same paths. A macro that has not run green in mock mode has no
business running on the live console.
