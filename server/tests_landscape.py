#!/usr/bin/env python3
"""Phone-landscape layout battery: the landscape arrangement (full-
height stage + internally scrolling control rail) at real phone
sizes over CDP, the WB loupe's viewport-fit placement at short
heights (including the beside-the-finger fallback), the update
banner in landscape, and portrait/desktop regression previews.

Run:  .venv/bin/python server/tests_landscape.py
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

SVC = "http://127.0.0.1:18097"
CHROME = os.path.expanduser(
    "~/.cache/ms-playwright/chromium-1248/chrome-linux64/chrome")
LAND_MQ = "(orientation: landscape) and (max-height: 540px)"

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
        return e.code, {}
    except urllib.error.URLError:
        return 0, {}


async def cdp_checks():
    print("ui: landscape layout, loupe placement, banner, regressions")
    port = 19224
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

            await cmd("Page.enable")
            await cmd("Runtime.enable")

            RECT = ("(e)=>{const r=e.getBoundingClientRect();"
                    "return {l:r.left,t:r.top,r:r.right,b:r.bottom,"
                    "w:r.width,h:r.height}}")

            # ---------------- landscape, buttons mode (844x390) --------
            await nav(SVC + "/", 844, 390)
            check("landscape media query matches at 844x390",
                  await ev(f"matchMedia('{LAND_MQ}').matches") is True)
            geo = await ev(f"""(() => {{
              const R = {RECT};
              return {{
                docH: document.documentElement.scrollHeight,
                vh: innerHeight,
                bodyOverflow: getComputedStyle(document.body).overflow,
                stage: R(document.querySelector('.stage')),
                wrap: R(document.querySelector('.video-wrap')),
                rail: R(document.querySelector('.rail')),
                railClientH: document.querySelector('.rail').clientHeight,
                railScrollH: document.querySelector('.rail').scrollHeight,
                padBtn: R(document.querySelector('#ptzPad button')),
                zoomIn: R(document.getElementById('zoomIn')),
                header: R(document.querySelector('header')),
              }}; }})()""")
            check("page itself does not scroll in landscape",
                  geo["docH"] <= geo["vh"] + 1
                  and geo["bodyOverflow"] == "hidden",
                  f"(docH {geo['docH']} vs vh {geo['vh']})")
            check("stage owns the left, rail the right, header slim",
                  geo["stage"]["r"] <= geo["rail"]["l"] + 1
                  and geo["rail"]["r"] <= 845
                  and geo["header"]["h"] <= 52,
                  f"(header {geo['header']['h']:.0f}px)")
            check("stream pane is full-height (not a stretched strip)",
                  geo["wrap"]["h"] >= 180,
                  f"(wrap {geo['wrap']['w']:.0f}x{geo['wrap']['h']:.0f})")
            check("rail scrolls internally (content taller than rail)",
                  geo["railScrollH"] > geo["railClientH"] + 40,
                  f"({geo['railScrollH']} > {geo['railClientH']})")
            check("drive pad + zoom reachable without scrolling",
                  0 <= geo["padBtn"]["t"] and geo["padBtn"]["b"] <= 391
                  and 0 <= geo["zoomIn"]["t"] and geo["zoomIn"]["b"] <= 391)
            await cap(os.path.join(
                ROOT, "ui-preview-landscape-buttons.png"))

            # ---------------- presets + advanced in the rail -----------
            await ev("""(() => {
              document.querySelector('.presets')
                .scrollIntoView({block: 'start'});
              return true; })()""")
            await asyncio.sleep(0.6)
            pre = await ev(f"""(() => {{
              const R = {RECT};
              const rail = R(document.querySelector('.rail'));
              const p = R(document.querySelector('.preset'));
              return {{rail, p,
                       n: document.querySelectorAll('.preset').length}};
            }})()""")
            check("presets scroll into view inside the rail",
                  pre["n"] >= 1 and pre["p"]["t"] >= pre["rail"]["t"] - 1
                  and pre["p"]["b"] <= pre["rail"]["b"] + 70,
                  f"({pre['n']} presets)")
            await cap(os.path.join(
                ROOT, "ui-preview-landscape-presets.png"))

            await ev("""(() => {
              if (document.getElementById('advBody').hidden)
                document.getElementById('advToggle').click();
              return true; })()""")
            await asyncio.sleep(1.2)
            adv = await ev(f"""(() => {{
              const R = {RECT};
              document.getElementById('wbPickBtn')
                .scrollIntoView({{block: 'center'}});
              const rail = R(document.querySelector('.rail'));
              const wb = R(document.getElementById('wbPickBtn'));
              document.getElementById('advUpdatesGroup')
                .scrollIntoView({{block: 'center'}});
              const upd = R(document.getElementById('updState'));
              return {{rail, wb, upd,
                       state: document.getElementById('updState')
                         .textContent}};
            }})()""")
            check("WB pick button scrolls into the rail's view",
                  adv["wb"]["t"] >= adv["rail"]["t"] - 1
                  and adv["wb"]["b"] <= adv["rail"]["b"] + 1,
                  f"(wb t{adv['wb']['t']:.0f} b{adv['wb']['b']:.0f} "
                  f"in rail t{adv['rail']['t']:.0f} "
                  f"b{adv['rail']['b']:.0f})")
            check("Updates row reports in landscape",
                  bool(adv["state"]), f"({adv['state']})")
            await cap(os.path.join(
                ROOT, "ui-preview-landscape-advanced.png"))

            # ---------------- joystick mode (740x360) ------------------
            await nav(SVC + "/", 740, 360)
            await ev("document.getElementById('modeJoystick').click()")
            await asyncio.sleep(0.8)
            joy = await ev(f"""(() => {{
              const R = {RECT};
              return {{base: R(document.getElementById('joyBase')),
                       home: R(document.getElementById('joyHomeBtn')),
                       homeHidden: document.getElementById('joyHomeBtn')
                         .hidden}};
            }})()""")
            check("joystick fully usable in landscape (base + HOME)",
                  joy["base"]["w"] >= 150 and joy["base"]["t"] >= 0
                  and joy["base"]["b"] <= 361 and not joy["homeHidden"]
                  and joy["home"]["b"] <= 361,
                  f"(base {joy['base']['w']:.0f}px)")
            await cap(os.path.join(
                ROOT, "ui-preview-landscape-joystick.png"))
            await ev("document.getElementById('modeButtons').click()")

            # ---------------- update banner in landscape ---------------
            await nav(SVC + "/?updateDemo=available", 740, 360)
            ban = await ev(f"""(() => {{
              const R = {RECT};
              const b = document.getElementById('updateBanner');
              const btn = R(document.getElementById('updateApplyBtn'));
              return {{hidden: b.hidden, btn,
                       txt: document.getElementById('updateBannerText')
                         .textContent}};
            }})()""")
            check("update banner visible + tappable in landscape",
                  ban["hidden"] is False
                  and "update is ready" in (ban["txt"] or "")
                  and ban["btn"]["t"] >= 0 and ban["btn"]["b"] <= 361
                  and ban["btn"]["r"] <= 741,
                  f"({ban['txt']})")
            await cap(os.path.join(
                ROOT, "ui-preview-landscape-update.png"))

            # ---------------- WB loupe in landscape --------------------
            await nav(SVC + "/?wbPickDemo=loupe", 844, 390)
            lou = await ev(f"""(() => {{
              const R = {RECT};
              const l = document.getElementById('wbLoupe');
              return {{hidden: l.hidden, rect: R(l),
                       vw: innerWidth, vh: innerHeight,
                       loupeZ: +getComputedStyle(l).zIndex,
                       barZ: +getComputedStyle(
                         document.getElementById('wbBar')).zIndex}};
            }})()""")
            check("loupe shows mid-drag in landscape, on-screen",
                  lou["hidden"] is False
                  and lou["rect"]["l"] >= -1 and lou["rect"]["t"] >= -1
                  and lou["rect"]["r"] <= lou["vw"] + 1
                  and lou["rect"]["b"] <= lou["vh"] + 1,
                  f"({lou['rect']})")
            check("loupe floats above the pick bar (never hidden)",
                  lou["loupeZ"] > lou["barZ"],
                  f"(loupe z{lou['loupeZ']} > bar z{lou['barZ']})")
            await cap(os.path.join(
                ROOT, "ui-preview-landscape-wbpick.png"))

            # Loupe placement stress at a very short height: the bubble
            # must stay in the viewport at the extremes, and beside the
            # finger where neither above nor below fits.
            await nav(SVC + "/", 700, 260)
            stress = await ev("""(async () => {
              const $ = (id) => document.getElementById(id);
              if ($("advBody").hidden) $("advToggle").click();
              $("wbPickBtn").click();
              await new Promise(r => setTimeout(r, 2600));
              const lay = () => $("wbPickLayer").getBoundingClientRect();
              const mk = (type, fx, fy) => new PointerEvent(type, {
                bubbles: true, pointerId: 9,
                clientX: lay().left + fx * lay().width,
                clientY: lay().top + fy * lay().height });
              const out = [];
              for (const [fx, fy] of [[0.5, 0.05], [0.5, 0.5],
                                      [0.5, 0.8], [0.06, 0.5],
                                      [0.94, 0.5]]) {
                const L = $("wbPickLayer");
                L.dispatchEvent(mk("pointerdown", fx, fy));
                L.dispatchEvent(mk("pointermove", fx, fy));
                await new Promise(r => setTimeout(r, 150));
                const lr = $("wbLoupe").getBoundingClientRect();
                const cx = lr.left + lr.width / 2,
                      cy = lr.top + lr.height / 2;
                const vx = lay().left + fx * lay().width,
                      vy = lay().top + fy * lay().height;
                out.push({fx, fy,
                  inside: lr.left >= -1 && lr.top >= -1 &&
                          lr.right <= innerWidth + 1 &&
                          lr.bottom <= innerHeight + 1,
                  place: Math.abs(cx - vx) > 40 ? "side"
                         : (cy < vy ? "above" : "below")});
                L.dispatchEvent(mk("pointerup", fx, fy));
              }
              $("wbCancel").click();
              return out;
            })()""")
            check("loupe stays in viewport at 700x260 extremes",
                  bool(stress) and all(p["inside"] for p in stress),
                  f"({stress})")
            mid = next((p for p in (stress or [])
                        if abs(p["fy"] - 0.8) < 0.01), None)
            check("loupe goes beside the finger when above/below "
                  "cannot fit", bool(mid) and mid["place"] == "side",
                  f"({mid})")

            # ---------------- portrait regression ----------------------
            await nav(SVC + "/", 390, 844)
            por = await ev(f"""(() => {{
              const R = {RECT};
              return {{mq: matchMedia('{LAND_MQ}').matches,
                       railDisplay: getComputedStyle(
                         document.querySelector('.rail')).display,
                       stage: R(document.querySelector('.stage')),
                       controls: R(document.querySelector('.controls')),
                       footer: getComputedStyle(
                         document.querySelector('footer')).display}};
            }})()""")
            check("portrait unchanged (stacked, rail contents, footer)",
                  por["mq"] is False and por["railDisplay"] == "contents"
                  and por["stage"]["b"] <= por["controls"]["t"] + 20
                  and por["footer"] != "none")
            await cap(os.path.join(ROOT, "ui-preview-mobile.png"))

            # ---------------- desktop regression -----------------------
            await nav(SVC + "/", 1280, 900, mobile=False)
            desk = await ev(f"""(() => {{
              const R = {RECT};
              return {{mq: matchMedia('{LAND_MQ}').matches,
                       railDisplay: getComputedStyle(
                         document.querySelector('.rail')).display,
                       stage: R(document.querySelector('.stage')),
                       controls: R(document.querySelector('.controls')),
                       presets: R(document.querySelector('.presets'))}};
            }})()""")
            check("desktop unchanged (side-by-side grid via contents)",
                  desk["mq"] is False
                  and desk["railDisplay"] == "contents"
                  and desk["controls"]["l"] > desk["stage"]["l"]
                  and desk["controls"]["t"] < desk["stage"]["b"]
                  and desk["presets"]["t"] >= desk["stage"]["b"] - 20)
            await cap(os.path.join(ROOT, "ui-preview-desktop.png"))
            check("preview screenshots written", True)
    finally:
        proc.terminate()


def main():
    env = dict(os.environ, PTZ_PORT="18097",
               PTZ_DB="/tmp/ptz-landscape-test.db",
               PTZ_UPDATES_DIR="/tmp/ptz-landscape-updates")
    if os.path.exists("/tmp/ptz-landscape-test.db"):
        os.remove("/tmp/ptz-landscape-test.db")
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
