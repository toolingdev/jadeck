#!/usr/bin/env python3
"""Fullscreen / immersive-video battery: the bottom-right fullscreen
button in portrait, landscape and desktop; tap-to-toggle the
on-video chrome; drag-is-not-a-tap; real Fullscreen API enter/exit
at phone portrait and landscape sizes (geometry + clean restore);
the ~3 s auto-hide in fullscreen and tap-to-bring-back; and
pick-a-white-point mode owning the pane (no button, no toggle).

Run:  .venv/bin/python server/tests_fullscreen.py
Spawns its own mock + service on test ports; touches nothing real.
"""
from __future__ import annotations

import asyncio
import base64
import json
import os
import subprocess
import sys
import time
import urllib.request
import urllib.error
import urllib.parse

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)

SVC = "http://127.0.0.1:18093"
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
    except urllib.error.HTTPError:
        return e.code, {}
    except urllib.error.URLError:
        return 0, {}


async def cdp_checks():
    print("ui: fullscreen button, tap chrome toggle, api enter/exit, "
          "auto-hide, pick-mode ownership")
    port = 19226
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
                    + urllib.parse.quote(SVC + "/"), method="PUT")
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
                await asyncio.sleep(4)

            async def cap(path):
                r = await cmd("Page.captureScreenshot", format="png")
                with open(path, "wb") as f:
                    f.write(base64.b64decode(r["data"]))

            async def mouse(kind, x, y, buttons=0):
                p = {"type": kind, "x": x, "y": y, "button": "left",
                     "clickCount": 1}
                if buttons:
                    p["buttons"] = buttons
                await cmd("Input.dispatchMouseEvent", **p)

            async def tap(x, y):
                await mouse("mousePressed", x, y)
                await asyncio.sleep(0.07)
                await mouse("mouseReleased", x, y)
                await asyncio.sleep(0.35)

            async def center_of(sel):
                return await ev(f"""(() => {{
                  const r = document.querySelector('{sel}')
                    .getBoundingClientRect();
                  return {{x: r.left + r.width / 2,
                           y: r.top + r.height / 2}}; }})()""")

            async def chrome_off():
                return await ev("document.getElementById('stage')"
                                ".classList.contains('chrome-off')")

            async def fs_on():
                return await ev("document.fullscreenElement ==="
                                " document.getElementById('stage')")

            async def wait_for(fn, timeout=8.0):
                t0 = time.time()
                while time.time() - t0 < timeout:
                    if await fn():
                        return True
                    await asyncio.sleep(0.25)
                return False

            await cmd("Page.enable")
            await cmd("Runtime.enable")

            RECT = ("(e)=>{const r=e.getBoundingClientRect();"
                    "return {l:r.left,t:r.top,r:r.right,b:r.bottom,"
                    "w:r.width,h:r.height}}")

            async def btn_geo():
                return await ev(f"""(() => {{
                  const R = {RECT};
                  const b = document.getElementById('fsBtn');
                  const cs = getComputedStyle(b);
                  return {{btn: R(b),
                           wrap: R(document.querySelector('.video-wrap')),
                           display: cs.display,
                           opacity: +cs.opacity,
                           iconIn: getComputedStyle(b.querySelector(
                             '.fs-ico-compress')).display,
                           iconOut: getComputedStyle(b.querySelector(
                             '.fs-ico-expand')).display}};
                }})()""")

            def btn_at_corner(g):
                return (g["display"] != "none" and g["opacity"] > 0.9
                        and g["btn"]["w"] >= 44 and g["btn"]["h"] >= 44
                        and 0 <= g["wrap"]["r"] - g["btn"]["r"] <= 24
                        and 0 <= g["wrap"]["b"] - g["btn"]["b"] <= 24
                        and g["btn"]["l"] >= g["wrap"]["l"]
                        and g["btn"]["t"] >= g["wrap"]["t"])

            # ---------------- portrait: button + tap toggle ----------
            await nav(SVC + "/", 390, 844)
            g = await btn_geo()
            check("fullscreen button bottom-right of the video "
                  "(portrait)", btn_at_corner(g), f"({g['btn']})")
            check("expand icon shown while windowed",
                  g["iconOut"] != "none" and g["iconIn"] == "none")
            await cap(os.path.join(ROOT, "ui-preview-mobile-fsbtn.png"))

            c = await center_of(".video-wrap")
            badge_op = "getComputedStyle(document.querySelector(" \
                       "'.video-badges')).opacity"
            check("chrome starts visible", await chrome_off() is False
                  and await ev(badge_op) == "1")
            await tap(c["x"], c["y"])
            check("tap on the video hides the chrome",
                  await chrome_off() is True
                  and await ev(badge_op) == "0")
            await tap(c["x"], c["y"])
            check("second tap brings the chrome back",
                  await chrome_off() is False
                  and await ev(badge_op) == "1")

            # A drag across the picture is not a tap.
            await mouse("mousePressed", c["x"] - 60, c["y"])
            await mouse("mouseMoved", c["x"], c["y"], buttons=1)
            await mouse("mouseMoved", c["x"] + 60, c["y"], buttons=1)
            await mouse("mouseReleased", c["x"] + 60, c["y"])
            await asyncio.sleep(0.35)
            check("drag across the video does NOT toggle the chrome",
                  await chrome_off() is False)

            # ---------------- portrait: real fullscreen --------------
            win_stage = await ev(f"""(() => {{ const R = {RECT};
              return R(document.getElementById('stage')); }})()""")
            b = await center_of("#fsBtn")
            await tap(b["x"], b["y"])
            check("fullscreen API engages on the stage (portrait)",
                  await wait_for(fs_on, 5))
            check("entering fullscreen did not also toggle chrome off",
                  await chrome_off() is False)
            geo = await ev(f"""(() => {{ const R = {RECT};
              return {{stage: R(document.getElementById('stage')),
                       iw: innerWidth, ih: innerHeight}}; }})()""")
            check("fullscreen stage fills the portrait viewport",
                  abs(geo["stage"]["w"] - geo["iw"]) <= 2
                  and abs(geo["stage"]["h"] - geo["ih"]) <= 2
                  and abs(geo["stage"]["l"]) <= 2
                  and abs(geo["stage"]["t"]) <= 2,
                  f"({geo['stage']} vs {geo['iw']}x{geo['ih']})")
            g = await btn_geo()
            check("compress icon + button bottom-right in fullscreen",
                  g["iconIn"] != "none" and g["iconOut"] == "none"
                  and btn_at_corner(g))
            await cap(os.path.join(
                ROOT, "ui-preview-fullscreen-portrait-on.png"))
            c = await center_of(".video-wrap")
            await tap(c["x"], c["y"])
            check("tap in fullscreen hides the chrome",
                  await chrome_off() is True)
            await cap(os.path.join(
                ROOT, "ui-preview-fullscreen-portrait-off.png"))
            await tap(c["x"], c["y"])
            check("tap in fullscreen brings the chrome back",
                  await chrome_off() is False)
            b = await center_of("#fsBtn")
            await tap(b["x"], b["y"])
            check("fullscreen exits from the button (portrait)",
                  await wait_for(lambda: ev(
                      "document.fullscreenElement === null"), 5))
            back = await ev(f"""(() => {{ const R = {RECT};
              return R(document.getElementById('stage')); }})()""")
            check("windowed layout restored after exit",
                  abs(back["w"] - win_stage["w"]) <= 2
                  and abs(back["h"] - win_stage["h"]) <= 2
                  and await chrome_off() is False,
                  f"({back} vs {win_stage})")

            # ---------------- auto-hide in fullscreen ----------------
            b = await center_of("#fsBtn")
            await tap(b["x"], b["y"])
            check("fullscreen re-engages for the auto-hide check",
                  await wait_for(fs_on, 5))
            check("chrome auto-hides after ~3 s untouched",
                  await wait_for(chrome_off, 8))
            c = await center_of(".video-wrap")
            await tap(c["x"], c["y"])
            check("tap wakes the chrome after auto-hide",
                  await chrome_off() is False)
            b = await center_of("#fsBtn")
            await tap(b["x"], b["y"])
            check("fullscreen exits after auto-hide check",
                  await wait_for(lambda: ev(
                      "document.fullscreenElement === null"), 5))

            # ---------------- landscape: button + fullscreen ---------
            await nav(SVC + "/", 844, 390)
            g = await btn_geo()
            check("fullscreen button bottom-right of the video "
                  "(landscape)", btn_at_corner(g), f"({g['btn']})")
            b = await center_of("#fsBtn")
            await tap(b["x"], b["y"])
            check("fullscreen API engages on the stage (landscape)",
                  await wait_for(fs_on, 5))
            geo = await ev(f"""(() => {{ const R = {RECT};
              return {{stage: R(document.getElementById('stage')),
                       iw: innerWidth, ih: innerHeight}}; }})()""")
            check("fullscreen stage fills the landscape viewport",
                  abs(geo["stage"]["w"] - geo["iw"]) <= 2
                  and abs(geo["stage"]["h"] - geo["ih"]) <= 2,
                  f"({geo['stage']} vs {geo['iw']}x{geo['ih']})")
            await cap(os.path.join(
                ROOT, "ui-preview-fullscreen-landscape-on.png"))
            b = await center_of("#fsBtn")
            await tap(b["x"], b["y"])
            check("fullscreen exits from the button (landscape)",
                  await wait_for(lambda: ev(
                      "document.fullscreenElement === null"), 5))

            # ---------------- desktop: button present ----------------
            await nav(SVC + "/", 1280, 900, mobile=False)
            g = await btn_geo()
            check("fullscreen button bottom-right of the video "
                  "(desktop)", btn_at_corner(g), f"({g['btn']})")

            # ---------------- pick mode owns the pane ----------------
            await nav(SVC + "/?wbPickDemo=pin", 390, 844)
            pick = await ev("""(() => {
              const b = document.getElementById('fsBtn');
              return {picking: document.querySelector('.video-wrap')
                        .classList.contains('picking'),
                      fsDisplay: getComputedStyle(b).display,
                      layerHidden: document.getElementById(
                        'wbPickLayer').hidden};
            })()""")
            check("pick mode active with the fullscreen button gone",
                  pick["picking"] is True
                  and pick["fsDisplay"] == "none"
                  and pick["layerHidden"] is False, f"({pick})")
            lc = await center_of("#wbPickLayer")
            await tap(lc["x"], lc["y"])
            check("tap during picking does NOT toggle the chrome",
                  await chrome_off() is False)
            check("preview screenshots written", True)
    finally:
        proc.terminate()


def main():
    env = dict(os.environ, PTZ_PORT="18093",
               PTZ_DB="/tmp/ptz-fullscreen-test.db",
               PTZ_UPDATES_DIR="/tmp/ptz-fullscreen-updates")
    if os.path.exists("/tmp/ptz-fullscreen-test.db"):
        os.remove("/tmp/ptz-fullscreen-test.db")
    svc = subprocess.Popen(
        [sys.executable, os.path.join(HERE, "run.py"), "--mock"],
        env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    try:
        for _ in range(60):
            code, _ = req("GET", SVC + "/api/status")
            if code == 200:
                break
            time.sleep(0.5)
        asyncio.run(cdp_checks())
    finally:
        svc.terminate()
    print(f"\n{len(PASS)} passed, {len(FAIL)} failed"
          + (f" — FAILED: {FAIL}" if FAIL else ""))
    sys.exit(1 if FAIL else 0)


if __name__ == "__main__":
    main()
