#!/usr/bin/env python3
"""Pick-a-white-point battery: unit checks on the VISCA builders and
geometry, then the full flow end-to-end against the mock camera
(frame order, exact return, center tap, corner clamp, busy rejection,
failure injection), then the loupe UI checks over CDP (a drag posts
the REFINED coordinates; preview screenshots).

Run:  .venv/bin/python server/tests_wb_pick.py
Spawns its own mock + service on test ports; touches nothing real.
"""
from __future__ import annotations

import asyncio
import json
import os
import subprocess
import sys
import threading
import time
import urllib.request
import urllib.error
import urllib.parse

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)
sys.path.insert(0, HERE)
from app import visca  # noqa: E402

SVC = "http://127.0.0.1:18099"
MOCK_HTTP = "http://127.0.0.1:18081"
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
            return resp.status, json.loads(resp.read() or b"{}")
    except urllib.error.HTTPError as e:
        try:
            return e.code, json.loads(e.read() or b"{}")
        except Exception:
            return e.code, {}


def mock_test(path):
    with urllib.request.urlopen(MOCK_HTTP + path, timeout=10) as r:
        body = r.read()
    try:
        return json.loads(body or b"{}")
    except ValueError:
        return {}


def reset(pan, tilt, zoom):
    mock_test(f"/__test__/reset?pan={pan}&tilt={tilt}&zoom={zoom}")
    time.sleep(0.8)   # let the service's poll loop see the new position


def abs_targets(log):
    """(pan, tilt) of every absolute-move frame in the mock's log."""
    out = []
    for hexs in log:
        f = bytes.fromhex(hexs)
        if len(f) >= 15 and f[1:4] == bytes([0x01, 0x06, 0x02]):
            def s16(start):
                v = 0
                for i in range(4):
                    v = (v << 4) | (f[start + i] & 0xF)
                return v - 0x10000 if v & 0x8000 else v
            out.append((s16(6), s16(10)))
    return out


def idx_of(log, hexs):
    return log.index(hexs) if hexs in log else -1


MODE = "81 01 04 35 03 ff"
TRIG = "81 01 04 35 05 ff"

# ------------------------------------------------------------- unit ---

def unit_checks():
    print("unit: visca builders + geometry")
    check("wb_mode(onepush) bytes", visca.wb_mode("onepush").hex(" ") == MODE)
    check("wb_onepush_trigger bytes",
          visca.wb_onepush_trigger().hex(" ") == TRIG)
    f = visca.pantilt_absolute(-100, 1296, 18, 16)
    check("absolute frame shape",
          f[1:4] == bytes([0x01, 0x06, 0x02]) and f[4] == 18 and f[5] == 16
          and len(f) == 15 and f[-1] == 0xFF)
    check("absolute clamps pan", abs_targets([visca.pantilt_absolute(
        99999, 0).hex(" ")]) == [(2448, 0)])
    check("hfov endpoints",
          abs(visca.hfov_degrees(0) - 60.7) < 0.01
          and abs(visca.hfov_degrees(0x4000) - 3.4) < 0.01)
    check("hfov monotonic narrowing",
          visca.hfov_degrees(0) > visca.hfov_degrees(0x2000)
          > visca.hfov_degrees(0x4000))
    check("center tap = zero delta",
          visca.wb_target(200, 100, 1000, 0.5, 0.5) == (200, 100))
    t = visca.wb_target(200, 100, 1000, 0.75, 0.25)
    check("off-center tap moves right+up", t[0] > 200 and t[1] > 100,
          f"-> {t}")
    check("corner tap clamps to pan limit",
          visca.wb_target(2400, 100, 0, 1.0, 0.5)[0] == 2448)


# -------------------------------------------------------- integration ---

def integration_checks():
    print("integration: full flow against the mock")
    for _ in range(60):
        try:
            _, s = req("GET", SVC + "/api/status")
            if s.get("connected"):
                break
        except Exception:
            pass
        time.sleep(0.5)
    else:
        check("service connects to mock", False)
        return
    check("service connects to mock", True)

    _, presets = req("GET", SVC + "/api/presets")
    pid = presets[0]["id"]

    # --- off-center tap: full sequence + exact return
    reset(200, 100, 1000)
    code, res = req("POST", SVC + "/api/camera/wb-pick",
                    {"x": 0.75, "y": 0.25})
    want = visca.wb_target(200, 100, 1000, 0.75, 0.25)
    m = mock_test("/__test__/visca")
    tg = abs_targets(m["log"])
    check("off-center pick succeeds", code == 200, f"({code} {res})")
    check("aimedAt matches the FOV model",
          tuple(res.get("aimedAt", {}).values()) == want,
          f"({res.get('aimedAt')} vs {want})")
    check("frame order mode < aim < trigger < return",
          0 <= idx_of(m["log"], MODE) < (m["log"].index(
              next(h for h in m["log"] if "01 06 02" in h)))
          and idx_of(m["log"], TRIG) > idx_of(m["log"], MODE)
          and tg == [want, (200, 100)],
          f"(targets {tg})")
    check("trigger frame after aim frame",
          m["log"].index(TRIG) > next(
              i for i, h in enumerate(m["log"]) if "01 06 02" in h))
    check("return target == stored origin exactly", tg[-1] == (200, 100))
    check("mock ends exactly at origin, zoom untouched",
          (m["pan"], m["tilt"], m["zoom"]) == (200, 100, 1000))
    check("exactly one VISCA trigger fired", m["wb_visca_triggers"] == 1)

    # --- center tap: mode + trigger, no motion at all
    reset(200, 100, 1000)
    code, res = req("POST", SVC + "/api/camera/wb-pick",
                    {"x": 0.5, "y": 0.5})
    m = mock_test("/__test__/visca")
    check("center pick succeeds", code == 200)
    check("center tap sends no absolute moves", abs_targets(m["log"]) == [])
    check("center tap still sets mode + fires trigger",
          idx_of(m["log"], MODE) >= 0 and m["wb_visca_triggers"] == 1)
    check("center aimedAt == origin",
          tuple(res.get("aimedAt", {}).values()) == (200, 100))

    # --- corner tap at widest zoom: clamps, still returns
    reset(2400, 100, 0)
    code, res = req("POST", SVC + "/api/camera/wb-pick",
                    {"x": 1.0, "y": 0.5})
    m = mock_test("/__test__/visca")
    check("corner pick succeeds", code == 200)
    check("corner aimedAt clamped to pan limit",
          res.get("aimedAt", {}).get("pan") == 2448,
          f"({res.get('aimedAt')})")
    check("corner pick returns to origin",
          (m["pan"], m["tilt"]) == (2400, 100))

    # --- busy: discrete commands rejected while the flow runs
    reset(200, 100, 1000)
    holder = {}
    def run_pick():
        holder["r"] = req("POST", SVC + "/api/camera/wb-pick",
                          {"x": 0.8, "y": 0.3})
    th = threading.Thread(target=run_pick)
    th.start()
    time.sleep(0.6)
    code_home, _ = req("POST", SVC + "/api/ptz/home")
    code_rec, _ = req("POST", SVC + f"/api/presets/{pid}/recall")
    th.join(timeout=30)
    m = mock_test("/__test__/visca")
    check("home rejected 409 during flow", code_home == 409,
          f"({code_home})")
    check("preset recall rejected 409 during flow", code_rec == 409,
          f"({code_rec})")
    check("busy pick itself completes", holder["r"][0] == 200)
    check("no drive frames leaked into the flow",
          not any("01 06 01" in h for h in m["log"]))

    # --- failure injection: the return move is dropped by the camera
    reset(200, 100, 1000)
    mock_test("/__test__/dropabs?n=2")
    code, res = req("POST", SVC + "/api/camera/wb-pick",
                    {"x": 0.75, "y": 0.25})
    m = mock_test("/__test__/visca")
    tg = abs_targets(m["log"])
    check("dropped return -> error reported", code == 502, f"({code})")
    check("return move was still attempted", tg == [want, (200, 100)],
          f"(targets {tg})")
    check("camera left at aim point (honest state)",
          (m["pan"], m["tilt"]) == want)

    # --- validation
    code, _ = req("POST", SVC + "/api/camera/wb-pick", {"x": 1.5, "y": 0.5})
    check("out-of-range coordinates -> 422", code == 422, f"({code})")


# ----------------------------------------------------------------- UI ---

async def cdp_checks():
    print("ui: loupe drag posts refined coordinates + previews")
    port = 19222
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
                    + urllib.parse.quote(SVC + "/?wbPickTest=1"),
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
        async with websockets.connect(ws_url, max_size=32 * 1024 * 1024) as ws:
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
            await cmd("Page.enable")
            await cmd("Runtime.enable")
            await cmd("Page.navigate", url=SVC + "/?wbPickTest=1")
            result = None
            for _ in range(80):
                await asyncio.sleep(0.5)
                r = await cmd("Runtime.evaluate", expression=(
                    "document.getElementById('wbTestResult')"
                    "?.textContent || ''"), returnByValue=True)
                txt = r.get("result", {}).get("value", "")
                if txt and txt != "running":
                    result = json.loads(txt)
                    break
            check("drag posts the REFINED point, not the first tap",
                  bool(result and result.get("ok")), f"({result})")

            async def shot(url, width, height, path, mobile=False):
                await cmd("Emulation.setDeviceMetricsOverride",
                          width=width, height=height,
                          deviceScaleFactor=2 if mobile else 1,
                          mobile=mobile)
                await cmd("Page.navigate", url=url)
                await asyncio.sleep(4)
                r = await cmd("Page.captureScreenshot", format="png")
                import base64
                with open(path, "wb") as f:
                    f.write(base64.b64decode(r["data"]))
            await shot(SVC + "/?wbPickDemo=loupe", 390, 844,
                       os.path.join(ROOT, "ui-preview-mobile-wbpick-loupe.png"),
                       mobile=True)
            await shot(SVC + "/?wbPickDemo=pin", 1280, 900,
                       os.path.join(ROOT, "ui-preview-desktop-wbpick.png"))
            check("preview screenshots written", True)
    finally:
        proc.terminate()


def main():
    unit_checks()
    env = dict(os.environ, PTZ_PORT="18099",
               PTZ_DB="/tmp/ptz-wbpick-test.db")
    if os.path.exists("/tmp/ptz-wbpick-test.db"):
        os.remove("/tmp/ptz-wbpick-test.db")
    svc = subprocess.Popen(
        [sys.executable, os.path.join(HERE, "run.py"), "--mock"],
        env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    try:
        integration_checks()
        asyncio.run(cdp_checks())
    finally:
        svc.terminate()
    print(f"\n{len(PASS)} passed, {len(FAIL)} failed"
          + (f" — FAILED: {FAIL}" if FAIL else ""))
    sys.exit(1 if FAIL else 0)


if __name__ == "__main__":
    main()
