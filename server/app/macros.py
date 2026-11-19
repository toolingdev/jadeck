"""Dashboard macros — chained WING actions, built and run from the
dashboard (authoring contract: MACROS.md at the repo root).

A macro is a name plus an ordered list of steps. Two step types in
v1, both aimed at the WING bridge:

  {"type": "wing_mute", "channel": 13, "muted": true}
  {"type": "wing_mute", "channelName": "MAIN", "muted": false}
  {"type": "delay", "ms": 500}

`channelName` names one of the 8 favorite slots (config.WING_*); it
resolves to that slot's console channel when the macro is saved, and
the resolved number is stored alongside the name — what runs is
always a number, the name is kept so the intent stays readable.

SAFETY, because these steps move live audio: a run is sequential;
every WING step is set through the bridge and then READ BACK from
the console, and the step only counts if the read-back matches.
Any failure — bridge error, read-back mismatch, per-step timeout,
the whole-run deadline — STOPS the macro at that step and the run
report says which step failed and why. Nothing is queued, nothing
is retried silently, and the engine returns to idle after every
run, success or failure.

Macros live in their own SQLite file (macros.db beside presets.db):
the preset DB is the owner's camera data and stays untouched, and the
macro store rides the same live-data exclusion from update
packages (server/data/** is never packaged).

On a fresh store two seed macros are written — the PC ⇄ Mic swap
The owner asked for (Nov 9, 2026), as a toggle pair. MAIN is channel 1,
a verified favorite. The PC channel (13) is a PLACEHOLDER: the owner
recalled it as "channel 13 or something", unverified — the seed
descriptions say so, and the number gets confirmed by name at the
first live session.
"""
from __future__ import annotations

import asyncio
import json
import os
import sqlite3
import threading
import time


MAX_NAME_LEN = 60
MAX_DESC_LEN = 300
MAX_STEPS = 64
MAX_DELAY_MS = 60_000
CHANNEL_MIN, CHANNEL_MAX = 1, 48

STEP_TIMEOUT_S = 10.0        # one WING step may never hang a run
RUN_DEADLINE_S = 180.0       # ...and neither may a whole macro

_MACRO_FIELDS = {"name", "description", "steps"}
_WING_STEP_FIELDS = {"type", "channel", "channelName", "muted"}
_DELAY_STEP_FIELDS = {"type", "ms"}


class MacroError(Exception):
    """Base for macro errors; main.py maps the subclasses."""


class MacroValidationError(MacroError):
    """The macro document is not valid (→ HTTP 400)."""


class MacroConflictError(MacroError):
    """Duplicate name, or a run already in flight (→ HTTP 409)."""


class MacroNotFoundError(MacroError):
    """No macro with that id (→ HTTP 404)."""


# ------------------------------------------------------------ favorites

def favorites_from_config(cfg) -> list[dict]:
    """The 8 WING favorite slots as [{name, channel}] — channel is
    None for an unassigned (placeholder) slot."""
    return [{"name": name, "channel": channel}
            for name, channel in zip(cfg.wing_names, cfg.wing_channels)]


def _resolve_favorite(name: str, favorites: list[dict]) -> dict:
    for fav in favorites:
        if fav["name"].lower() == name.strip().lower():
            return fav
    names = ", ".join(f["name"] for f in favorites)
    raise MacroValidationError(
        f"unknown favorite channel name {name!r} — the favorites are: "
        f"{names}")


# ------------------------------------------------------------ validation

def _is_int(v) -> bool:
    return isinstance(v, int) and not isinstance(v, bool)


def validate_macro(data, favorites: list[dict]) -> dict:
    """Validate one macro document (MACROS.md) and return its
    normalized form: trimmed name, defaulted description, steps
    with favorite names resolved to channel numbers. Raises
    MacroValidationError with a precise, step-numbered message."""
    if not isinstance(data, dict):
        raise MacroValidationError("a macro must be a JSON object")
    unknown = set(data) - _MACRO_FIELDS
    if unknown:
        raise MacroValidationError(
            f"unknown macro field(s): {', '.join(sorted(unknown))} "
            f"(allowed: {', '.join(sorted(_MACRO_FIELDS))})")
    name = data.get("name")
    if not isinstance(name, str) or not name.strip():
        raise MacroValidationError("name is required (a non-empty string)")
    name = name.strip()
    if len(name) > MAX_NAME_LEN:
        raise MacroValidationError(
            f"name is {len(name)} characters; the limit is {MAX_NAME_LEN}")
    description = data.get("description", "")
    if not isinstance(description, str):
        raise MacroValidationError("description must be a string")
    if len(description) > MAX_DESC_LEN:
        raise MacroValidationError(
            f"description is {len(description)} characters; the limit "
            f"is {MAX_DESC_LEN}")
    steps = data.get("steps")
    if not isinstance(steps, list) or not steps:
        raise MacroValidationError(
            "steps is required (a non-empty list of steps)")
    if len(steps) > MAX_STEPS:
        raise MacroValidationError(
            f"{len(steps)} steps; the limit is {MAX_STEPS}")
    return {"name": name, "description": description,
            "steps": [_validate_step(i + 1, s, favorites)
                      for i, s in enumerate(steps)]}


def _validate_step(n: int, step, favorites: list[dict]) -> dict:
    if not isinstance(step, dict):
        raise MacroValidationError(f"step {n}: a step must be an object")
    stype = step.get("type")
    if stype == "wing_mute":
        unknown = set(step) - _WING_STEP_FIELDS
        if unknown:
            raise MacroValidationError(
                f"step {n}: unknown field(s) for a wing_mute step: "
                f"{', '.join(sorted(unknown))}")
        if "muted" not in step:
            raise MacroValidationError(
                f"step {n}: wing_mute needs \"muted\" (true or false)")
        muted = step["muted"]
        if not isinstance(muted, bool):
            raise MacroValidationError(
                f"step {n}: \"muted\" must be true or false")
        channel = step.get("channel")
        chan_name = step.get("channelName")
        if channel is not None and not _is_int(channel):
            raise MacroValidationError(
                f"step {n}: \"channel\" must be an integer")
        if channel is not None and not CHANNEL_MIN <= channel <= CHANNEL_MAX:
            raise MacroValidationError(
                f"step {n}: channel {channel} is outside "
                f"{CHANNEL_MIN}..{CHANNEL_MAX}")
        if chan_name is not None and not isinstance(chan_name, str):
            raise MacroValidationError(
                f"step {n}: \"channelName\" must be a string")
        if channel is None and chan_name is None:
            raise MacroValidationError(
                f"step {n}: wing_mute needs a \"channel\" number or a "
                f"\"channelName\" favorite")
        out = {"type": "wing_mute", "muted": muted}
        if chan_name is not None:
            fav = _resolve_favorite(chan_name, favorites)
            if fav["channel"] is None:
                raise MacroValidationError(
                    f"step {n}: favorite {fav['name']!r} is unassigned "
                    f"(it has no console channel yet)")
            if channel is not None and channel != fav["channel"]:
                raise MacroValidationError(
                    f"step {n}: channel {channel} disagrees with "
                    f"channelName {chan_name!r}, which is channel "
                    f"{fav['channel']}")
            out["channel"] = fav["channel"]
            out["channelName"] = fav["name"]
        else:
            out["channel"] = channel
        return out
    if stype == "delay":
        unknown = set(step) - _DELAY_STEP_FIELDS
        if unknown:
            raise MacroValidationError(
                f"step {n}: unknown field(s) for a delay step: "
                f"{', '.join(sorted(unknown))}")
        ms = step.get("ms")
        if not _is_int(ms):
            raise MacroValidationError(
                f"step {n}: delay needs \"ms\" (an integer number of "
                f"milliseconds)")
        if not 0 <= ms <= MAX_DELAY_MS:
            raise MacroValidationError(
                f"step {n}: delay {ms} ms is outside 0..{MAX_DELAY_MS}")
        return {"type": "delay", "ms": ms}
    raise MacroValidationError(
        f"step {n}: unknown step type {stype!r} "
        f"(v1 types: wing_mute, delay)")


def extract_import(payload) -> tuple[list, bool]:
    """The import shapes (MACROS.md): one macro object, a bare list
    of them, or {"macros": [...]}; the wrapper may also carry
    "validateOnly": true. Returns (raw macro list, validate_only)."""
    if isinstance(payload, list):
        return payload, False
    if isinstance(payload, dict):
        if "macros" in payload:
            items = payload["macros"]
            if not isinstance(items, list):
                raise MacroValidationError(
                    "\"macros\" must be a list of macro objects")
            return items, bool(payload.get("validateOnly", False))
        if "steps" in payload or "name" in payload:
            return [payload], bool(payload.get("validateOnly", False))
    raise MacroValidationError(
        "import body must be a macro object, a list of macro "
        "objects, or {\"macros\": [...]}")


def describe_step(step: dict, favorites: list[dict]) -> str:
    """One plain-words line for a step — run logs and the builder's
    summary line both use it, so they can never disagree."""
    if step["type"] == "delay":
        ms = step["ms"]
        if ms == 0:
            return "Wait 0 ms"
        return f"Wait {ms / 1000:g} s" if ms >= 1000 \
            else f"Wait {ms} ms"
    name = step.get("channelName")
    if not name:
        for fav in favorites:
            if fav["channel"] == step["channel"]:
                name = fav["name"]
                break
    verb = "Mute" if step["muted"] else "Unmute"
    where = f"{name} · Ch {step['channel']}" if name \
        else f"Ch {step['channel']}"
    return f"{verb} {where}"


# ------------------------------------------------------------ seed

PC_CHANNEL_PLACEHOLDER = 13   # the owner: "channel 13 or something" —
                              # UNVERIFIED, see the seed descriptions.


def _seed_macros(favorites: list[dict]) -> list[dict]:
    """The PC ⇄ Mic swap as a toggle pair. Unmute-first in both
    directions, so the thing that should be live comes up before
    the other goes down (no dead air, no doubled audio for long)."""
    mic = {"type": "wing_mute", "channelName": "MAIN"}
    try:
        _resolve_favorite("MAIN", favorites)
    except MacroValidationError:
        mic = {"type": "wing_mute", "channel": 1}
    note = ("Seeded swap, one of a pair. The PC channel "
            f"({PC_CHANNEL_PLACEHOLDER}) is a PLACEHOLDER — it is "
            "unverified; verify the PC channel by name at the first "
            "live session, then edit this macro. MAIN is channel 1, "
            "a verified favorite.")
    raw = [
        {"name": "PC ⇄ Mic: PC live", "description": note,
         "steps": [{"type": "wing_mute",
                    "channel": PC_CHANNEL_PLACEHOLDER, "muted": False},
                   {**mic, "muted": True}]},
        {"name": "PC ⇄ Mic: Mic live", "description": note,
         "steps": [{**mic, "muted": False},
                   {"type": "wing_mute",
                    "channel": PC_CHANNEL_PLACEHOLDER, "muted": True}]},
    ]
    return [validate_macro(m, favorites) for m in raw]


# ------------------------------------------------------------ store

class MacroStore:
    """Macros in their own SQLite file — never the preset DB."""

    def __init__(self, path: str, favorites: list[dict]):
        self._favorites = favorites
        os.makedirs(os.path.dirname(path), exist_ok=True)
        self._db = sqlite3.connect(path, check_same_thread=False)
        self._db.row_factory = sqlite3.Row
        self._lock = threading.Lock()
        with self._lock, self._db:
            self._db.execute("""
                CREATE TABLE IF NOT EXISTS macros (
                    id          INTEGER PRIMARY KEY AUTOINCREMENT,
                    name        TEXT NOT NULL,
                    description TEXT NOT NULL DEFAULT '',
                    steps       TEXT NOT NULL,
                    sort        INTEGER NOT NULL,
                    last_run    TEXT,
                    created_at  REAL NOT NULL,
                    updated_at  REAL NOT NULL
                )""")
            n = self._db.execute(
                "SELECT COUNT(*) FROM macros").fetchone()[0]
            if n == 0:
                now = time.time()
                for i, macro in enumerate(
                        _seed_macros(favorites)):
                    self._db.execute(
                        "INSERT INTO macros (name, description, steps,"
                        " sort, created_at, updated_at)"
                        " VALUES (?,?,?,?,?,?)",
                        (macro["name"], macro["description"],
                         json.dumps(macro["steps"]), i, now, now))

    @staticmethod
    def _row(r: sqlite3.Row) -> dict:
        return {
            "id": r["id"], "name": r["name"],
            "description": r["description"],
            "steps": json.loads(r["steps"]), "sort": r["sort"],
            "lastRun": json.loads(r["last_run"]) if r["last_run"]
                       else None,
            "createdAt": r["created_at"], "updatedAt": r["updated_at"],
        }

    def list(self) -> list[dict]:
        with self._lock:
            rows = self._db.execute(
                "SELECT * FROM macros ORDER BY sort, id").fetchall()
        return [self._row(r) for r in rows]

    def get(self, mid: int) -> dict | None:
        with self._lock:
            r = self._db.execute(
                "SELECT * FROM macros WHERE id=?", (mid,)).fetchone()
        return self._row(r) if r else None

    def by_name(self, name: str) -> dict | None:
        for m in self.list():
            if m["name"].lower() == name.strip().lower():
                return m
        return None

    def create(self, macro: dict) -> dict:
        if self.by_name(macro["name"]) is not None:
            raise MacroConflictError(
                f"a macro named {macro['name']!r} already exists")
        now = time.time()
        with self._lock, self._db:
            mx = self._db.execute(
                "SELECT COALESCE(MAX(sort), -1) FROM macros"
            ).fetchone()[0]
            cur = self._db.execute(
                "INSERT INTO macros (name, description, steps, sort,"
                " created_at, updated_at) VALUES (?,?,?,?,?,?)",
                (macro["name"], macro["description"],
                 json.dumps(macro["steps"]), mx + 1, now, now))
            mid = cur.lastrowid
        return self.get(mid)

    def update(self, mid: int, macro: dict) -> dict | None:
        clash = self.by_name(macro["name"])
        if clash is not None and clash["id"] != mid:
            raise MacroConflictError(
                f"a macro named {macro['name']!r} already exists")
        with self._lock, self._db:
            cur = self._db.execute(
                "UPDATE macros SET name=?, description=?, steps=?,"
                " updated_at=? WHERE id=?",
                (macro["name"], macro["description"],
                 json.dumps(macro["steps"]), time.time(), mid))
            if cur.rowcount == 0:
                return None
        return self.get(mid)

    def delete(self, mid: int) -> bool:
        with self._lock, self._db:
            cur = self._db.execute(
                "DELETE FROM macros WHERE id=?", (mid,))
            return cur.rowcount > 0

    def record_run(self, mid: int, report: dict) -> None:
        with self._lock, self._db:
            self._db.execute(
                "UPDATE macros SET last_run=? WHERE id=?",
                (json.dumps(report), mid))


# ------------------------------------------------------------ engine

class MacroEngine:
    """Runs macros against the WING service, one at a time.

    The engine's whole state is `running` + the current run's
    position; a finally block clears both after EVERY run —
    success, stop-on-failure, or exception — so a failed macro can
    never wedge the next one."""

    def __init__(self, store: MacroStore, wing_svc,
                 favorites: list[dict],
                 step_timeout_s: float = STEP_TIMEOUT_S,
                 run_deadline_s: float = RUN_DEADLINE_S):
        self._store = store
        self._wing = wing_svc
        self._favorites = favorites
        self._step_timeout_s = step_timeout_s
        self._run_deadline_s = run_deadline_s
        self._running = False
        self._current: dict | None = None

    @property
    def running(self) -> bool:
        return self._running

    def status(self) -> dict:
        return {"running": self._running, "current": self._current}

    async def run(self, mid: int) -> dict:
        macro = self._store.get(mid)
        if macro is None:
            raise MacroNotFoundError(f"no macro with id {mid}")
        if self._running:
            raise MacroConflictError(
                "another macro is already running — wait for it to "
                "finish")
        self._running = True
        started = time.time()
        self._current = {"macroId": mid, "name": macro["name"],
                         "step": 0, "totalSteps": len(macro["steps"]),
                         "startedAt": started}
        results: list[dict] = []
        failed_step = None
        error = None
        try:
            deadline = started + self._run_deadline_s
            for i, step in enumerate(macro["steps"]):
                self._current["step"] = i + 1
                entry = {"step": i + 1, "type": step["type"],
                         "label": describe_step(step, self._favorites)}
                t0 = time.time()
                try:
                    entry["detail"] = await self._run_step(
                        step, deadline)
                    entry["ok"] = True
                except Exception as e:  # noqa: BLE001 — a step
                    # failing IS the report; the run stops here.
                    entry["ok"] = False
                    entry["error"] = str(e) or type(e).__name__
                    failed_step = i + 1
                    error = (f"step {i + 1} failed: {entry['error']}")
                entry["ms"] = round((time.time() - t0) * 1000)
                results.append(entry)
                if failed_step is not None:
                    break
            report = {"macroId": mid, "name": macro["name"],
                      "ok": failed_step is None,
                      "failedStep": failed_step, "error": error,
                      "startedAt": started, "finishedAt": time.time(),
                      "steps": results}
            self._store.record_run(mid, report)
            return report
        finally:
            self._running = False
            self._current = None

    async def _run_step(self, step: dict, deadline: float) -> str:
        remaining = deadline - time.time()
        if remaining <= 0:
            raise MacroError(
                f"the run passed its {self._run_deadline_s:g} s "
                "deadline — stopped before this step")
        if step["type"] == "delay":
            ms = step["ms"]
            if ms / 1000 > remaining:
                raise MacroError(
                    f"a {ms} ms delay would pass the run deadline")
            await asyncio.wait_for(asyncio.sleep(ms / 1000),
                                   timeout=remaining)
            return f"waited {ms} ms"
        if step["type"] == "wing_mute":
            try:
                back = await asyncio.wait_for(
                    self._wing.set_mute_by_channel(
                        step["channel"], step["muted"]),
                    timeout=min(self._step_timeout_s, remaining))
            except asyncio.TimeoutError:
                raise MacroError(
                    f"the console did not answer within "
                    f"{self._step_timeout_s:g} s") from None
            return (f"channel {back['channel']} read back "
                    f"{'muted' if back['muted'] else 'live'}")
        raise MacroError(f"cannot run step type {step['type']!r}")
