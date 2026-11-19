#!/usr/bin/env python3
"""Macros battery: the validator and store unit-tested, the
engine's semantics (ordering, delays, read-back stop-on-failure,
state reset, one-run-at-a-time) tested against stub and real
WING services, the macro endpoints end-to-end against the
mock-mode service, and the builder UI over CDP at phone and
desktop sizes — cards, a seeded run landing green, building a
macro in the editor, importing JSON, no phone-width overflow.

Run:  .venv/bin/python server/tests_macros.py
Spawns its own mock + service on test ports; touches nothing real.
"""
from __future__ import annotations

import asyncio
import base64
import json
import os
import sqlite3
import subprocess
import sys
import tempfile
import time
import urllib.request
import urllib.error
import urllib.parse

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)
sys.path.insert(0, HERE)

from app import macros, wing  # noqa: E402
from app.config import WING_DEFAULT_CHANNELS, WING_DEFAULT_NAMES  # noqa: E402

SVC = "http://127.0.0.1:18097"
CHROME = os.path.expanduser(
    "~/.cache/ms-playwright/chromium-1248/chrome-linux64/chrome")

PASS = []
FAIL = []


def check(name, cond, detail=""):
    (PASS if cond else FAIL).append(name)
    print(f"  {'ok  ' if cond else 'FAIL'} {name} {detail}")


def req(method, url, body=None):
    data = json.dumps(body).encode() if body is not None else None
    r = urllib.request.Request(url, data=data, method=method,
                               headers={"Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(r, timeout=30) as resp:
            raw = resp.read()
            try:
                return resp.status, json.loads(raw or b"{}")
            except ValueError:
                return resp.status, {"__text": raw.decode("utf-8", "replace")}
    except urllib.error.HTTPError as e:
        try:
            return e.code, json.loads(e.read() or b"{}")
        except Exception:
            return e.code, {}
    except urllib.error.URLError:
        return 0, {}


FAVS = [{"name": n, "channel": c}
        for n, c in zip(WING_DEFAULT_NAMES, WING_DEFAULT_CHANNELS)]


# ------------------------------------------------------------- units

def expect_invalid(name, doc, *needles):
    try:
        macros.validate_macro(doc, FAVS)
        check(name, False, "(validated when it should not have)")
    except macros.MacroValidationError as e:
        check(name, all(n in str(e) for n in needles), f"({e})")


def unit_checks():
    print("unit: macro validation")
    m = macros.validate_macro(
        {"name": "  Test  ", "steps": [
            {"type": "wing_mute", "channelName": "main", "muted": True},
            {"type": "delay", "ms": 250}]}, FAVS)
    check("validate: name trimmed, favorite resolved case-insensitively",
          m["name"] == "Test" and m["description"] == ""
          and m["steps"][0] == {"type": "wing_mute", "muted": True,
                                "channel": 1, "channelName": "MAIN"})
    m = macros.validate_macro(
        {"name": "Num", "steps": [
            {"type": "wing_mute", "channel": 13, "muted": False}]}, FAVS)
    check("validate: a bare channel number passes through",
          m["steps"][0] == {"type": "wing_mute", "muted": False,
                            "channel": 13})
    m = macros.validate_macro(
        {"name": "Both", "steps": [
            {"type": "wing_mute", "channel": 1, "channelName": "MAIN",
             "muted": True}]}, FAVS)
    check("validate: channel + agreeing channelName is fine",
          m["steps"][0]["channel"] == 1
          and m["steps"][0]["channelName"] == "MAIN")

    expect_invalid("validate: missing name refused",
                   {"steps": [{"type": "delay", "ms": 1}]}, "name")
    expect_invalid("validate: name over 60 chars refused",
                   {"name": "x" * 61,
                    "steps": [{"type": "delay", "ms": 1}]}, "60")
    expect_invalid("validate: empty steps refused",
                   {"name": "x", "steps": []}, "steps")
    expect_invalid("validate: 65 steps refused",
                   {"name": "x",
                    "steps": [{"type": "delay", "ms": 0}] * 65}, "64")
    expect_invalid("validate: unknown macro field refused",
                   {"name": "x", "bogus": 1,
                    "steps": [{"type": "delay", "ms": 1}]}, "bogus")
    expect_invalid("validate: unknown step type refused, step numbered",
                   {"name": "x", "steps": [{"type": "delay", "ms": 1},
                                           {"type": "explode"}]},
                   "step 2", "explode")
    expect_invalid("validate: unknown step field refused",
                   {"name": "x", "steps": [
                       {"type": "delay", "ms": 1, "fast": True}]},
                   "step 1", "fast")
    expect_invalid("validate: wing_mute without muted refused",
                   {"name": "x", "steps": [
                       {"type": "wing_mute", "channel": 3}]}, "muted")
    expect_invalid("validate: wing_mute muted=1 refused (not a bool)",
                   {"name": "x", "steps": [
                       {"type": "wing_mute", "channel": 3, "muted": 1}]},
                   "true or false")
    expect_invalid("validate: wing_mute with no channel at all refused",
                   {"name": "x", "steps": [
                       {"type": "wing_mute", "muted": True}]}, "channel")
    expect_invalid("validate: channel 0 refused",
                   {"name": "x", "steps": [
                       {"type": "wing_mute", "channel": 0,
                        "muted": True}]}, "1..48")
    expect_invalid("validate: channel 49 refused",
                   {"name": "x", "steps": [
                       {"type": "wing_mute", "channel": 49,
                        "muted": True}]}, "1..48")
    expect_invalid("validate: disagreeing channel + channelName refused",
                   {"name": "x", "steps": [
                       {"type": "wing_mute", "channel": 5,
                        "channelName": "MAIN", "muted": True}]},
                   "disagrees")
    expect_invalid("validate: unknown favorite refused, names listed",
                   {"name": "x", "steps": [
                       {"type": "wing_mute", "channelName": "PC",
                        "muted": True}]}, "PC", "MAIN")
    favs_un = [dict(f) for f in FAVS]
    favs_un[0] = {"name": "MAIN", "channel": None}
    try:
        macros.validate_macro(
            {"name": "x", "steps": [
                {"type": "wing_mute", "channelName": "MAIN",
                 "muted": True}]}, favs_un)
        check("validate: unassigned favorite refused", False)
    except macros.MacroValidationError as e:
        check("validate: unassigned favorite refused",
              "unassigned" in str(e), f"({e})")
    expect_invalid("validate: delay -1 refused",
                   {"name": "x", "steps": [{"type": "delay", "ms": -1}]},
                   "0..60000")
    expect_invalid("validate: delay 60001 refused",
                   {"name": "x", "steps": [
                       {"type": "delay", "ms": 60001}]}, "0..60000")
    expect_invalid("validate: delay as a string refused",
                   {"name": "x", "steps": [
                       {"type": "delay", "ms": "500"}]}, "ms")

    check("labels: mute by name, unmute by number, waits",
          macros.describe_step(
              {"type": "wing_mute", "channel": 1,
               "channelName": "MAIN", "muted": True}, FAVS)
          == "Mute MAIN · Ch 1"
          and macros.describe_step(
              {"type": "wing_mute", "channel": 13, "muted": False},
              FAVS) == "Unmute Ch 13"
          and macros.describe_step({"type": "delay", "ms": 1500}, FAVS)
          == "Wait 1.5 s"
          and macros.describe_step({"type": "delay", "ms": 250}, FAVS)
          == "Wait 250 ms")

    items, dry = macros.extract_import({"macros": [], "validateOnly": 1})
    check("import shapes: wrapper + validateOnly flag",
          items == [] and dry is True)
    items, dry = macros.extract_import([{"name": "a"}])
    check("import shapes: a bare list", len(items) == 1 and dry is False)
    items, dry = macros.extract_import({"name": "a", "steps": []})
    check("import shapes: a single macro object",
          len(items) == 1 and dry is False)
    try:
        macros.extract_import({"what": "is this"})
        check("import shapes: junk refused", False)
    except macros.MacroValidationError:
        check("import shapes: junk refused", True)


def store_checks(tmp):
    print("unit: macro store (own file, seeds, CRUD)")
    path = os.path.join(tmp, "macros.db")
    store = macros.MacroStore(path, FAVS)
    seeded = store.list()
    check("store: fresh store seeds exactly the PC ⇄ Mic pair",
          [m["name"] for m in seeded]
          == ["PC ⇄ Mic: PC live", "PC ⇄ Mic: Mic live"])
    pc_live, mic_live = seeded
    check("store: seed PC steps use channel 13, mic resolves to MAIN/1",
          pc_live["steps"][0]["channel"] == 13
          and pc_live["steps"][1]["channelName"] == "MAIN"
          and pc_live["steps"][1]["channel"] == 1
          and mic_live["steps"][0]["channel"] == 1
          and mic_live["steps"][1]["channel"] == 13)
    check("store: seed descriptions carry the PC verification note",
          all("PLACEHOLDER" in m["description"]
              and "unverified" in m["description"] for m in seeded))
    check("store: seeds have no last run yet",
          all(m["lastRun"] is None for m in seeded))

    doc = macros.validate_macro(
        {"name": "Store test", "steps": [{"type": "delay", "ms": 5}]},
        FAVS)
    made = store.create(doc)
    check("store: create + get round-trips",
          made["name"] == "Store test"
          and store.get(made["id"])["steps"] == doc["steps"])
    try:
        store.create(macros.validate_macro(
            {"name": "store TEST",
             "steps": [{"type": "delay", "ms": 5}]}, FAVS))
        check("store: duplicate name (any case) refused", False)
    except macros.MacroConflictError:
        check("store: duplicate name (any case) refused", True)
    upd = store.update(made["id"], macros.validate_macro(
        {"name": "Store test 2", "description": "d",
         "steps": [{"type": "delay", "ms": 9}]}, FAVS))
    check("store: update replaces name/steps",
          upd["name"] == "Store test 2"
          and upd["steps"][0]["ms"] == 9)
    report = {"macroId": made["id"], "ok": True, "steps": []}
    store.record_run(made["id"], report)
    check("store: a run report is recorded on the macro",
          store.get(made["id"])["lastRun"]["ok"] is True)

    again = macros.MacroStore(path, FAVS)
    check("store: reopening does not re-seed or lose rows",
          [m["name"] for m in again.list()]
          == ["PC ⇄ Mic: PC live", "PC ⇄ Mic: Mic live",
              "Store test 2"])
    check("store: delete works, then get is None",
          store.delete(made["id"]) is True
          and store.get(made["id"]) is None
          and store.delete(made["id"]) is False)


# ------------------------------------------------------------- engine

class CfgStub:
    mock = True
    wing_host = ""
    wing_port = 2223
    wing_channels = WING_DEFAULT_CHANNELS
    wing_names = WING_DEFAULT_NAMES


class StubWingSvc:
    """Engine-level stand-in for WingService: records calls, can
    refuse per channel, can hang (for the timeout check)."""

    def __init__(self, fail=(), hang=False):
        self.calls = []
        self.state = {}
        self.fail = set(fail)
        self.hang = hang

    async def set_mute_by_channel(self, channel, muted):
        self.calls.append((channel, muted))
        if self.hang:
            await asyncio.sleep(30)
        if channel in self.fail:
            raise wing.WingError("stub: the console refused")
        self.state[channel] = muted
        return {"channel": channel, "muted": muted}


def engine_checks(tmp):
    print("unit: macro engine semantics")
    path = os.path.join(tmp, "engine-macros.db")
    if os.path.exists(path):
        os.remove(path)
    store = macros.MacroStore(path, FAVS)

    def mk(name, steps):
        return store.create(macros.validate_macro(
            {"name": name, "steps": steps}, FAVS))

    # Ordering + delay really elapses.
    stub = StubWingSvc()
    eng = macros.MacroEngine(store, stub, FAVS)
    m = mk("Order", [{"type": "wing_mute", "channel": 1, "muted": True},
                     {"type": "delay", "ms": 150},
                     {"type": "wing_mute", "channel": 2,
                      "muted": False}])
    t0 = time.time()
    rep = asyncio.run(eng.run(m["id"]))
    elapsed = time.time() - t0
    check("engine: steps run in order, delay elapses, report is green",
          rep["ok"] and stub.calls == [(1, True), (2, False)]
          and elapsed >= 0.15 and len(rep["steps"]) == 3
          and all(s["ok"] for s in rep["steps"]),
          f"({elapsed:.2f}s)")
    check("engine: the run was recorded on the macro",
          store.get(m["id"])["lastRun"]["ok"] is True)
    check("engine: idle after a clean run", eng.running is False)

    # Stop on failure: step 2 refuses; step 3 must never run.
    stub2 = StubWingSvc(fail={2})
    eng2 = macros.MacroEngine(store, stub2, FAVS)
    m2 = mk("Fragile", [
        {"type": "wing_mute", "channel": 1, "muted": True},
        {"type": "wing_mute", "channel": 2, "muted": True},
        {"type": "wing_mute", "channel": 3, "muted": True}])
    rep = asyncio.run(eng2.run(m2["id"]))
    check("engine: a failed step stops the macro at that step",
          rep["ok"] is False and rep["failedStep"] == 2
          and "step 2" in rep["error"]
          and stub2.calls == [(1, True), (2, True)]
          and len(rep["steps"]) == 2
          and rep["steps"][1]["ok"] is False)
    check("engine: state resets after a failed run",
          eng2.running is False)
    m2b = mk("Recovery", [
        {"type": "wing_mute", "channel": 3, "muted": True}])
    rep = asyncio.run(eng2.run(m2b["id"]))
    check("engine: the next run after a failure works normally",
          rep["ok"] is True)

    # Per-step timeout: a hanging console cannot hang the macro.
    eng3 = macros.MacroEngine(store, StubWingSvc(hang=True), FAVS,
                              step_timeout_s=0.25)
    t0 = time.time()
    rep = asyncio.run(eng3.run(m2["id"]))
    check("engine: a hung step times out and stops the run",
          rep["ok"] is False and rep["failedStep"] == 1
          and "did not answer" in rep["error"]
          and time.time() - t0 < 5 and eng3.running is False)

    # Run deadline: a long delay cannot pass it.
    eng4 = macros.MacroEngine(store, StubWingSvc(), FAVS,
                              run_deadline_s=0.25)
    m4 = mk("Slow", [{"type": "delay", "ms": 5000}])
    rep = asyncio.run(eng4.run(m4["id"]))
    check("engine: the run deadline stops an over-long macro",
          rep["ok"] is False and "deadline" in rep["error"]
          and eng4.running is False)

    # One at a time.
    eng5 = macros.MacroEngine(store, StubWingSvc(), FAVS)
    m5 = mk("Long", [{"type": "delay", "ms": 700}])

    async def concurrent():
        task = asyncio.create_task(eng5.run(m5["id"]))
        await asyncio.sleep(0.1)
        try:
            await eng5.run(m5["id"])
            return False, await task
        except macros.MacroConflictError:
            return True, await task

    refused, rep = asyncio.run(concurrent())
    check("engine: a second run while one is in flight is refused",
          refused and rep["ok"] is True and eng5.running is False)

    try:
        asyncio.run(eng.run(99999))
        check("engine: unknown macro id raises NotFound", False)
    except macros.MacroNotFoundError:
        check("engine: unknown macro id raises NotFound", True)

    # The real WingService, mock-backed: read-back verification is
    # the service's own, and the seeded pair flips real state.
    svc = wing.WingService(CfgStub())
    eng6 = macros.MacroEngine(store, svc, FAVS)
    seeds = {m["name"]: m for m in store.list()}
    rep = asyncio.run(eng6.run(seeds["PC ⇄ Mic: Mic live"]["id"]))
    check("engine: seeded Mic live runs green on the mock console",
          rep["ok"] and svc._impl.get_mute(1) is False
          and svc._impl.get_mute(13) is True)
    rep = asyncio.run(eng6.run(seeds["PC ⇄ Mic: PC live"]["id"]))
    check("engine: seeded PC live reverses it, also green",
          rep["ok"] and svc._impl.get_mute(1) is True
          and svc._impl.get_mute(13) is False)

    # The service's verification itself: a console whose sets never
    # take must produce a WingError, not a quiet pass.
    class LyingDesk:
        def set_mute(self, channel, muted):
            pass

        def get_mute(self, channel):
            return False

    svc._impl = LyingDesk()
    try:
        asyncio.run(svc.set_mute_by_channel(5, True))
        check("wing service: read-back mismatch raises", False)
    except wing.WingError as e:
        check("wing service: read-back mismatch raises",
              "read back" in str(e), f"({e})")
    try:
        asyncio.run(svc.set_mute_by_channel(49, True))
        check("wing service: channel 49 refused before any traffic",
              False)
    except wing.WingError:
        check("wing service: channel 49 refused before any traffic",
              True)


# ------------------------------------------------------------- API

def api_checks():
    print("api: macro endpoints against the mock-mode service")

    code, lst = req("GET", SVC + "/api/macros")
    check("macros list: the seeded pair, no last runs yet",
          code == 200 and len(lst) == 2
          and lst[0]["name"] == "PC ⇄ Mic: PC live"
          and all(m["lastRun"] is None for m in lst))

    code, eng = req("GET", SVC + "/api/macros/engine")
    check("engine status: idle", code == 200 and eng["running"] is False)

    doc = {"name": "API macro", "description": "from the battery",
           "steps": [{"type": "wing_mute", "channelName": "main",
                      "muted": True},
                     {"type": "delay", "ms": 300},
                     {"type": "wing_mute", "channel": 13,
                      "muted": False}]}
    code, made = req("POST", SVC + "/api/macros", doc)
    check("create: 201, favorite resolved to MAIN channel 1",
          code == 201 and made["steps"][0]["channel"] == 1
          and made["steps"][0]["channelName"] == "MAIN")
    mid = made["id"]

    code, err = req("POST", SVC + "/api/macros",
                    {"name": "Bad", "steps": [
                        {"type": "wing_mute", "channel": 99,
                         "muted": True}]})
    check("create invalid: 400 naming the problem",
          code == 400 and "1..48" in err.get("detail", ""))
    code, err = req("POST", SVC + "/api/macros", doc)
    check("create duplicate name: 409",
          code == 409 and "already exists" in err.get("detail", ""))

    t0 = time.time()
    code, rep = req("POST", SVC + f"/api/macros/{mid}/run")
    elapsed = time.time() - t0
    check("run: green report, per-step results, delay honored",
          code == 200 and rep["ok"] is True and elapsed >= 0.3
          and [s["ok"] for s in rep["steps"]] == [True, True, True]
          and rep["steps"][0]["label"] == "Mute MAIN · Ch 1")
    code, one = req("GET", SVC + f"/api/macros/{mid}")
    check("run: the report is persisted as the macro's last run",
          code == 200 and one["lastRun"]["ok"] is True
          and len(one["lastRun"]["steps"]) == 3)
    code, wst = req("GET", SVC + "/api/wing/status")
    by_label = {c["label"]: c for c in wst["channels"]}
    check("run: the console state really changed (MAIN muted)",
          by_label["MAIN"]["muted"] is True)

    code, rep = req("POST", SVC + "/api/macros/99999/run")
    check("run unknown macro: 404", code == 404)

    code, upd = req("PATCH", SVC + f"/api/macros/{mid}",
                    {"name": "API macro renamed",
                     "steps": [{"type": "delay", "ms": 10}]})
    check("patch: name + steps replaced",
          code == 200 and upd["name"] == "API macro renamed"
          and upd["steps"] == [{"type": "delay", "ms": 10}])
    code, _ = req("PATCH", SVC + "/api/macros/99999",
                  {"name": "ghost"})
    check("patch unknown macro: 404", code == 404)

    # Import: single object, then a wrapper list.
    code, res = req("POST", SVC + "/api/macros/import",
                    {"name": "Imported one",
                     "steps": [{"type": "delay", "ms": 1}]})
    check("import: a single macro object lands (201)",
          code == 201 and len(res["imported"]) == 1
          and res["imported"][0]["name"] == "Imported one")
    code, res = req("POST", SVC + "/api/macros/import",
                    {"macros": [
                        {"name": "Imported two",
                         "steps": [{"type": "delay", "ms": 1}]},
                        {"name": "Imported three",
                         "steps": [{"type": "wing_mute", "channel": 6,
                                    "muted": True}]}]})
    check("import: a {macros: [...]} wrapper lands both",
          code == 201 and len(res["imported"]) == 2)

    # Import is all-or-nothing, with per-macro errors.
    code, before = req("GET", SVC + "/api/macros")
    code, res = req("POST", SVC + "/api/macros/import",
                    {"macros": [
                        {"name": "Should not land",
                         "steps": [{"type": "delay", "ms": 1}]},
                        {"name": "Broken",
                         "steps": [{"type": "wing_mute",
                                    "channelName": "NOPE",
                                    "muted": True}]}]})
    check("import: one bad macro refuses the whole batch (400)",
          code == 400 and len(res.get("errors", [])) == 1
          and res["errors"][0]["index"] == 1
          and "NOPE" in res["errors"][0]["error"])
    code, after = req("GET", SVC + "/api/macros")
    check("import: nothing from a refused batch was written",
          len(after) == len(before)
          and all(m["name"] != "Should not land" for m in after))

    code, res = req("POST", SVC + "/api/macros/import?validateOnly=true",
                    {"name": "Dry run",
                     "steps": [{"type": "delay", "ms": 1}]})
    check("import validateOnly: valid, and nothing written",
          code == 200 and res.get("valid") is True)
    code, lst = req("GET", SVC + "/api/macros")
    check("import validateOnly: the macro really is absent",
          all(m["name"] != "Dry run" for m in lst))

    code, res = req("POST", SVC + "/api/macros/import",
                    {"name": "Imported one",
                     "steps": [{"type": "delay", "ms": 1}]})
    check("import: a name that already exists is a precise error",
          code == 400 and "already exists"
          in res["errors"][0]["error"])

    code, r = req("DELETE", SVC + f"/api/macros/{mid}")
    check("delete: ok, then the macro is gone",
          code == 200 and r.get("ok") is True)
    code, _ = req("GET", SVC + f"/api/macros/{mid}")
    check("delete: get after delete is 404", code == 404)

    # The macro store is its own file; the preset DB is untouched.
    pdb = sqlite3.connect("/tmp/ptz-macros-test.db")
    ptables = {r[0] for r in pdb.execute(
        "SELECT name FROM sqlite_master WHERE type='table'")}
    mdb = sqlite3.connect("/tmp/ptz-macros-test-macros.db")
    mtables = {r[0] for r in mdb.execute(
        "SELECT name FROM sqlite_master WHERE type='table'")}
    check("stores: presets.db holds only presets, macros.db only macros",
          ptables == {"presets", "sqlite_sequence"}
          and "macros" in mtables)
    code, presets = req("GET", SVC + "/api/presets")
    check("presets: still the camera's eight, untouched by macros",
          code == 200 and len(presets) == 8)


# ---------------------------------------------------------------- UI

async def cdp_checks():
    print("ui: macro cards, run, builder, import (CDP)")
    port = 19231
    proc = subprocess.Popen(
        [CHROME, "--headless=new", "--no-sandbox", "--disable-gpu",
         f"--remote-debugging-port={port}", "about:blank"],
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    try:
        ws_url = None
        for _ in range(50):
            try:
                rq = urllib.request.Request(
                    f"http://127.0.0.1:{port}/json/new?url="
                    + urllib.parse.quote(SVC + "/dashboard"),
                    method="PUT")
                with urllib.request.urlopen(rq, timeout=5) as r:
                    ws_url = json.loads(r.read())["webSocketDebuggerUrl"]
                break
            except Exception:
                time.sleep(0.3)
        if not ws_url:
            check("chromium CDP up", False)
            return
        import websockets
        async with websockets.connect(ws_url,
                                      max_size=32 * 1024 * 1024) as ws:
            seq = 0

            async def cmd(method, **params):
                nonlocal seq
                seq += 1
                await ws.send(json.dumps(
                    {"id": seq, "method": method, "params": params}))
                while True:
                    msg = json.loads(await ws.recv())
                    if msg.get("id") == seq:
                        return msg.get("result", {})

            async def ev(expr):
                r = await cmd("Runtime.evaluate", expression=expr,
                              returnByValue=True, awaitPromise=True)
                return r.get("result", {}).get("value")

            async def nav(url, width, height, mobile=True):
                await cmd("Emulation.setDeviceMetricsOverride",
                          width=width, height=height,
                          deviceScaleFactor=2 if mobile else 1,
                          mobile=mobile)
                await cmd("Page.navigate", url=url)
                await asyncio.sleep(3)

            async def cap(path):
                r = await cmd("Page.captureScreenshot", format="png")
                with open(path, "wb") as f:
                    f.write(base64.b64decode(r["data"]))

            async def wait_for(expr, timeout=12.0):
                t0 = time.time()
                while time.time() - t0 < timeout:
                    if await ev(expr):
                        return True
                    await asyncio.sleep(0.3)
                return False

            await cmd("Page.enable")
            await cmd("Runtime.enable")

            # ---------------- phone: the parity size ----------------
            await nav(SVC + "/dashboard", 390, 844)
            check("macros: the seeded pair renders as cards on phone",
                  await wait_for("""(() => {
                    const c = document.querySelectorAll(
                      '#macroList .macro-card');
                    return c.length >= 2 && [...c].some((x) =>
                      x.textContent.includes('PC ⇄ Mic: Mic live')); })()"""))
            check("macros: the PC seed shows its summary with Ch 13",
                  await ev("""(() => {
                    const c = [...document.querySelectorAll(
                      '#macroList .macro-card')].find((x) =>
                      x.textContent.includes('PC ⇄ Mic: PC live'));
                    return !!c && c.textContent.includes('Ch 13')
                      && c.textContent.includes('PLACEHOLDER'); })()"""))
            check("macros: Run on the Mic-live seed lands all-green",
                  await ev("""(() => {
                    const c = [...document.querySelectorAll(
                      '#macroList .macro-card')].find((x) =>
                      x.textContent.includes('PC ⇄ Mic: Mic live'));
                    if (!c) return false;
                    c.querySelector('.macro-run').click();
                    return true; })()""")
                  and await wait_for("""(() => {
                    const c = [...document.querySelectorAll(
                      '#macroList .macro-card')].find((x) =>
                      x.textContent.includes('PC ⇄ Mic: Mic live'));
                    return !!c && c.textContent.includes(
                      'all steps landed')
                      && c.querySelectorAll('.step-chip.pass')
                           .length === 2; })()"""))
            check("macros: no horizontal overflow on the phone",
                  await ev("""(() =>
                    document.documentElement.scrollWidth
                      <= window.innerWidth + 1)()"""))

            # Builder: new macro, one mute step + one delay step.
            await ev("document.getElementById('macroNew').click()")
            await asyncio.sleep(0.4)
            await ev("""(() => {
              document.getElementById('macroName').value =
                'CDP built macro';
              document.getElementById('macroAddDelay').click();
              return true; })()""")
            await asyncio.sleep(0.4)
            await ev("document.getElementById('macroSave').click()")
            check("builder: a macro built in the editor appears as a card",
                  await wait_for("""(() =>
                    [...document.querySelectorAll('#macroList .macro-card')]
                      .some((x) => x.textContent.includes(
                        'CDP built macro')))()"""))

            # Import: paste JSON, land a card.
            await ev("document.getElementById('macroImportToggle').click()")
            await asyncio.sleep(0.3)
            await ev("""(() => {
              document.getElementById('macroImportText').value =
                JSON.stringify({name: 'CDP imported macro', steps: [
                  {type: 'wing_mute', channelName: 'AUDIENCE',
                   muted: true}]});
              document.getElementById('macroImportBtn').click();
              return true; })()""")
            check("import: pasted JSON lands as a card",
                  await wait_for("""(() =>
                    [...document.querySelectorAll('#macroList .macro-card')]
                      .some((x) => x.textContent.includes(
                        'CDP imported macro')))()"""))
            await cap(os.path.join(ROOT, "ui-preview-dashboard-"
                                         "macros-mobile.png"))

            # ---------------- desktop ----------------
            await nav(SVC + "/dashboard", 1400, 900, mobile=False)
            check("macros: cards render at desktop width too",
                  await wait_for("""(() =>
                    document.querySelectorAll('#macroList .macro-card')
                      .length >= 4)()"""))
            check("macros: no horizontal overflow on desktop",
                  await ev("""(() =>
                    document.documentElement.scrollWidth
                      <= window.innerWidth + 1)()"""))
            await ev("""(() => document.querySelector('.macros')
                      .scrollIntoView())()""")
            await asyncio.sleep(0.6)
            await cap(os.path.join(ROOT, "ui-preview-dashboard-"
                                         "macros-desktop.png"))
            check("preview screenshots written", True)
    finally:
        proc.terminate()


def main():
    tmp = tempfile.mkdtemp(prefix="ptz-macros-test-")
    unit_checks()
    store_checks(tmp)
    engine_checks(tmp)
    for f in ("/tmp/ptz-macros-test.db", "/tmp/ptz-macros-test-macros.db"):
        if os.path.exists(f):
            os.remove(f)
    env = dict(os.environ, PTZ_PORT="18097",
               PTZ_DB="/tmp/ptz-macros-test.db",
               PTZ_MACROS_DB="/tmp/ptz-macros-test-macros.db",
               PTZ_UPDATES_DIR="/tmp/ptz-macros-updates")
    svc = subprocess.Popen(
        [sys.executable, os.path.join(HERE, "run.py"), "--mock"],
        env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    try:
        for _ in range(60):
            code, _ = req("GET", SVC + "/api/status")
            if code == 200:
                break
            time.sleep(0.5)
        api_checks()
        asyncio.run(cdp_checks())
    finally:
        svc.terminate()
    print(f"\n{len(PASS)} passed, {len(FAIL)} failed"
          + (f" — FAILED: {FAIL}" if FAIL else ""))
    sys.exit(1 if FAIL else 0)


if __name__ == "__main__":
    main()
