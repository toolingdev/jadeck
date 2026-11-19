#!/usr/bin/env python3
"""Dashboard battery: the three bridges unit- and wire-tested
against fakes, the dashboard endpoints end-to-end against the
mock-mode service (fake bridges + mock camera), and the dashboard
UI over CDP at iPad, desktop and phone sizes — view switcher,
no-signal panes, vMix preview/CUT tally flow, the Proclaim MIDI
readout, a preset recall reaching the mock camera, the WING mute
flipping both ways, and no phone-width overflow.

One environment note: this sandbox blocks UDP outright (even
loopback), so the WING wire checks run the real client and codec
over whichever datagram transport exists — a real UDP socket when
permitted, an in-memory pipe with identical semantics when not.
The battery prints which one it used.

Run:  .venv/bin/python server/tests_dashboard.py
Spawns its own mock + service on test ports; touches nothing real.
"""
from __future__ import annotations

import asyncio
import base64
import json
import os
import socket
import subprocess
import sys
import threading
import time
import urllib.request
import urllib.error
import urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)
sys.path.insert(0, HERE)

from app import proclaim, vmix, wing  # noqa: E402
from app.config import (  # noqa: E402
    Config, WING_DEFAULT_CHANNELS, WING_DEFAULT_NAMES,
    _parse_wing_channels, _parse_wing_names)

SVC = "http://127.0.0.1:18095"
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


# ------------------------------------------------------------- units

def unit_checks():
    print("unit: OSC codec, vMix XML, MIDI packing, slot config")

    # OSC: the exact bytes of a mute set for channel 14.
    pkt = wing.osc_encode("/ch/14/mute", [1])
    check("OSC encode: address padded, typetag ,i, big-endian int",
          pkt == b"/ch/14/mute\x00" + b",i\x00\x00"
                 + (1).to_bytes(4, "big", signed=True),
          f"({pkt.hex()})")
    addr, values = wing.osc_decode(pkt)
    check("OSC decode round-trips a set",
          addr == "/ch/14/mute" and values == [1])
    addr, values = wing.osc_decode(wing.osc_encode("/?"))
    check("OSC bare address decodes with no values",
          addr == "/?" and values == [])
    addr, values = wing.osc_decode(
        wing.osc_encode("/?", ["WING,10.0.0.9,PGM,ngc-full,S1,1.0"]))
    check("OSC string arg round-trips (probe reply shape)",
          addr == "/?" and values[0].startswith("WING,"))
    try:
        wing.osc_decode(b"\x01\x02\x03")
        check("OSC decode rejects garbage", False)
    except wing.WingError:
        check("OSC decode rejects garbage", True)

    # vMix XML: canned Web Controller state.
    xml = b"""<?xml version="1.0" encoding="UTF-8"?>
<vmix><version>29.0.0.49</version><edition>Max</edition>
<inputs>
<input key="aaaa" number="1" type="XAML" title="Cam Wide" state="Running">Cam Wide</input>
<input key="bbbb" number="2" type="NDI" title="Slides" state="Running">Slides</input>
<input key="cccc" number="3" type="Video" title="Clip" state="Paused">Clip</input>
</inputs>
<preview>2</preview><active>1</active>
<fadeToBlack>False</fadeToBlack><streaming>True</streaming>
<recording>False</recording></vmix>"""
    st = vmix.parse_status_xml(xml)
    check("vMix XML: inputs parsed with numbers/titles/types",
          [i["number"] for i in st["inputs"]] == [1, 2, 3]
          and st["inputs"][1]["title"] == "Slides"
          and st["inputs"][2]["state"] == "Paused")
    check("vMix XML: preview/active/streaming/recording parsed",
          st["preview"] == 2 and st["active"] == 1
          and st["streaming"] is True and st["recording"] is False
          and st["version"] == "29.0.0.49")
    try:
        vmix.parse_status_xml(b"<html>not vmix</html>")
        check("vMix XML: foreign XML rejected", False)
    except vmix.VmixError:
        check("vMix XML: foreign XML rejected", True)

    # MIDI: the DWORDs winmm would receive for the mockup's note.
    on, off = proclaim.note_dwords(62, channel=1)
    check("MIDI: note-on DWORD for Ch 1 Note 62 vel 100",
          on == 0x90 | (62 << 8) | (100 << 16), f"({on:#x})")
    check("MIDI: note-off DWORD pairs it",
          off == 0x80 | (62 << 8))
    on3, _ = proclaim.note_dwords(60, channel=3)
    check("MIDI: channel lands in the status nibble",
          on3 & 0xFF == 0x92)

    # Slot config parsing (8 slots; partial lists None-pad).
    check("WING channels: '14,2,,9' -> (14, 2, None, 9, None x4)",
          _parse_wing_channels("14,2,,9")
          == (14, 2, None, 9, None, None, None, None))
    check("WING channels: empty -> all placeholders",
          _parse_wing_channels("") == (None,) * 8)
    check("WING channels: junk and out-of-range -> placeholders",
          _parse_wing_channels("x,0,49,5")
          == (None, None, None, 5, None, None, None, None))
    check("WING names: pipe list with Fav fallbacks",
          _parse_wing_names("Speaker||Piano")
          == ("Speaker", "Fav 2", "Piano", "Fav 4", "Fav 5",
              "Fav 6", "Fav 7", "Fav 8"))
    check("WING channels: the full 8-entry override parses in order",
          _parse_wing_channels("1,2,3,7,8,10,12,6")
          == (1, 2, 3, 7, 8, 10, 12, 6))
    check("WING names: the full 8-entry override parses in order",
          _parse_wing_names("MAIN|WIRELESS GRAY|WIFI MIC 2|VOX BK 1|"
                            "VOX BK 2|ZGTAR|MONTAGE|AUDIENCE")
          == WING_DEFAULT_NAMES)

    # Shipped defaults: the owner's picks (Nov 8, 2026), mock and real.
    check("WING defaults are the owner's 8 picks, in his order",
          WING_DEFAULT_CHANNELS == (1, 2, 3, 7, 8, 10, 12, 6)
          and WING_DEFAULT_NAMES == (
              "MAIN", "WIRELESS GRAY", "WIFI MIC 2", "VOX BK 1",
              "VOX BK 2", "ZGTAR", "MONTAGE", "AUDIENCE")
          and Config().wing_channels == WING_DEFAULT_CHANNELS
          and Config().wing_names == WING_DEFAULT_NAMES)
    saved = {k: os.environ.get(k) for k in
             ("PTZ_WING_CHANNELS", "PTZ_WING_NAMES", "PTZ_MOCK")}
    try:
        for k in saved:
            os.environ.pop(k, None)
        cfg = Config.from_env()
        check("WING from_env: no overrides -> the shipped picks",
              cfg.wing_channels == WING_DEFAULT_CHANNELS
              and cfg.wing_names == WING_DEFAULT_NAMES)
        os.environ["PTZ_WING_CHANNELS"] = "5,6"
        os.environ["PTZ_WING_NAMES"] = "A|B"
        cfg = Config.from_env()
        check("WING from_env: an override replaces the whole list",
              cfg.wing_channels
              == (5, 6, None, None, None, None, None, None)
              and cfg.wing_names[:2] == ("A", "B"))
    finally:
        for k, v in saved.items():
            if v is None:
                os.environ.pop(k, None)
            else:
                os.environ[k] = v


# -------------------------------------------- wire-level fake bridges

class FakeWingDesk:
    """The console's half of the protocol as a packet handler:
    /? probe, bare-address read, set with an int. Transport-free,
    so the same desk can sit behind a real UDP socket or an
    in-memory datagram pipe (this sandbox blocks UDP outright —
    even loopback — so the battery uses whichever exists)."""

    def __init__(self):
        self.muted = {7: False}
        self.received = []

    def handle(self, data: bytes) -> bytes | None:
        addr, values = wing.osc_decode(data)
        self.received.append((addr, values))
        if addr == "/?":
            return wing.osc_encode(
                "/?", ["WING,127.0.0.1,PGM,ngc-full,FAKE,1.0"])
        if addr == "/ch/7/mute" and not values:
            return wing.osc_encode(
                "/ch/7/mute", [1 if self.muted[7] else 0])
        if addr == "/ch/7/mute":
            self.muted[7] = values[0] != 0
        return None


class MemorySocket:
    """A datagram-shaped transport with no kernel socket: sendto
    hands the packet to the fake desk synchronously; recvfrom
    returns its reply, or raises socket.timeout when the desk
    stays silent — the exact surface WingClient codes against."""

    def __init__(self, desk):
        self.desk = desk
        self._inbox = []

    def settimeout(self, t):
        pass

    def sendto(self, pkt, peer):
        reply = self.desk.handle(pkt)
        if reply is not None:
            self._inbox.append(reply)

    def recvfrom(self, n):
        if not self._inbox:
            raise socket.timeout()
        return self._inbox.pop(0), ("127.0.0.1", 2223)

    def close(self):
        pass


class UdpDeskServer(threading.Thread):
    """The fake desk behind a real UDP socket, where the sandbox
    permits UDP at all."""

    def __init__(self, desk):
        super().__init__(daemon=True)
        self.desk = desk
        self.sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        self.sock.bind(("127.0.0.1", 0))
        self.port = self.sock.getsockname()[1]

    def run(self):
        while True:
            try:
                data, peer = self.sock.recvfrom(8192)
            except OSError:
                return
            reply = self.desk.handle(data)
            if reply is not None:
                self.sock.sendto(reply, peer)


def _udp_available() -> bool:
    try:
        s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        s.bind(("127.0.0.1", 0))
        s.sendto(b"x", ("127.0.0.1", 9))
        s.close()
        return True
    except OSError:
        return False


class FakeVmixServer:
    """An HTTP stand-in for the Web Controller: canned state XML,
    function calls recorded and applied to preview/active."""

    XML = """<?xml version="1.0" encoding="UTF-8"?>
<vmix><version>29.0.0.49</version><edition>Max</edition>
<inputs>
<input key="k1" number="1" type="XAML" title="Wide" state="Running">Wide</input>
<input key="k2" number="2" type="NDI" title="Slides" state="Running">Slides</input>
</inputs>
<preview>{preview}</preview><active>{active}</active>
<fadeToBlack>False</fadeToBlack><streaming>False</streaming>
<recording>True</recording></vmix>"""

    def __init__(self):
        self.preview = 2
        self.active = 1
        self.calls = []
        outer = self

        class H(BaseHTTPRequestHandler):
            def log_message(self, *a):
                pass

            def do_GET(self):
                q = urllib.parse.parse_qs(
                    urllib.parse.urlparse(self.path).query)
                fn = q.get("Function", [None])[0]
                if fn:
                    outer.calls.append((fn, q))
                    if fn == "PreviewInput":
                        outer.preview = int(q["Input"][0])
                    elif fn in ("Cut", "Fade"):
                        outer.active = outer.preview
                body = FakeVmixServer.XML.format(
                    preview=outer.preview,
                    active=outer.active).encode()
                self.send_response(200)
                self.send_header("Content-Type", "text/xml")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

        self.httpd = ThreadingHTTPServer(("127.0.0.1", 0), H)
        self.port = self.httpd.server_address[1]
        threading.Thread(target=self.httpd.serve_forever,
                         daemon=True).start()


def wire_checks():
    print("wire: real clients against fake WING + fake vMix (HTTP)")

    desk = FakeWingDesk()
    if _udp_available():
        srv = UdpDeskServer(desk)
        srv.start()
        client = wing.WingClient("127.0.0.1", srv.port, timeout=2.0)
        transport = "real UDP socket"
    else:
        srv = None
        client = wing.WingClient(
            "127.0.0.1", 2223, timeout=2.0,
            sock_factory=lambda: MemorySocket(desk))
        transport = "in-memory datagrams (sandbox blocks UDP)"
    print(f"       WING transport: {transport}")
    check("WING probe returns the console string",
          client.probe().startswith("WING,127.0.0.1"))
    check("WING read: channel 7 starts live",
          client.get_mute(7) is False)
    client.set_mute(7, True)
    check("WING set then read-back: muted",
          client.get_mute(7) is True)
    check("WING toggle flips it back live",
          client.toggle_mute(7) is False
          and desk.muted[7] is False)

    class SilentDesk:
        def handle(self, data):
            return None

    quiet = wing.WingClient(
        "127.0.0.1", 2223, timeout=0.2,
        sock_factory=lambda: MemorySocket(SilentDesk()))
    try:
        quiet.get_mute(7)
        check("WING silence raises WingError, never a guess", False)
    except wing.WingError:
        check("WING silence raises WingError, never a guess", True)
    if srv is not None:
        srv.sock.close()

    fake = FakeVmixServer()
    vc = vmix.VmixClient("127.0.0.1", fake.port, timeout=2.0)
    st = vc.status()
    check("vMix client: status over HTTP parses",
          st["active"] == 1 and st["preview"] == 2
          and st["recording"] is True and len(st["inputs"]) == 2)
    vc.preview(1)
    vc.cut()
    fnames = [c[0] for c in fake.calls]
    check("vMix client: PreviewInput then Cut were called",
          fnames == ["PreviewInput", "Cut"], f"({fnames})")
    check("vMix client: the fake state followed (active=1)",
          vc.status()["active"] == 1)
    vc.fade(1000)
    check("vMix client: Fade carries Duration=1000",
          fake.calls[-1][0] == "Fade"
          and fake.calls[-1][1].get("Duration") == ["1000"])
    fake.httpd.shutdown()


# ------------------------------------------------------- service API

def api_checks():
    print("api: dashboard endpoints against the mock-mode service")

    code, cfg = req("GET", SVC + "/api/config")
    check("/api/config carries program + confidence gateway URLs",
          code == 200 and cfg.get("programWhep", "").endswith("/program/whep")
          and cfg.get("confidenceHls", "").endswith("/confidence/index.m3u8"))

    code, st = req("GET", SVC + "/api/vmix/status")
    check("vmix status: simulated, 4 mockup inputs, PGM 1 PVW 2",
          code == 200 and st["connected"] and st["simulated"]
          and len(st["inputs"]) == 4 and st["active"] == 1
          and st["preview"] == 2)
    code, st = req("POST", SVC + "/api/vmix/preview", {"input": 3})
    check("vmix preview 3 lands", code == 200 and st["preview"] == 3)
    code, st = req("POST", SVC + "/api/vmix/cut")
    check("vmix CUT takes preview to program",
          code == 200 and st["active"] == 3)
    code, st = req("POST", SVC + "/api/vmix/preview", {"input": 2})
    code, st = req("POST", SVC + "/api/vmix/fade", {"durationMs": 1000})
    check("vmix FADE takes preview to program",
          code == 200 and st["active"] == 2)
    code, _ = req("POST", SVC + "/api/vmix/preview", {"input": 99})
    check("vmix preview of a missing input -> 502, not a silent ok",
          code == 502)

    code, st = req("GET", SVC + "/api/proclaim/status")
    check("proclaim status: mock port, defaults Ch 1 Next=62",
          code == 200 and st["simulated"] and st["channel"] == 1
          and st["notes"]["next"] == 62 and st["notes"]["prev"] == 60)
    code, r = req("POST", SVC + "/api/proclaim/next")
    check("proclaim next sends Note 62 on Ch 1",
          code == 200 and r["note"] == 62 and r["channel"] == 1)
    code, st = req("GET", SVC + "/api/proclaim/status")
    check("proclaim status remembers the last note sent",
          st["lastSent"]["action"] == "next")
    code, _ = req("POST", SVC + "/api/proclaim/bogus")
    check("proclaim unknown action -> 400", code == 400)

    # the owner's 8 picks (Nov 8, 2026), in display order.
    PICKS = [(1, "MAIN", 1), (2, "WIRELESS GRAY", 2),
             (3, "WIFI MIC 2", 3), (4, "VOX BK 1", 7),
             (5, "VOX BK 2", 8), (6, "ZGTAR", 10),
             (7, "MONTAGE", 12), (8, "AUDIENCE", 6)]
    code, st = req("GET", SVC + "/api/wing/status")
    check("wing status: linked fake console, the owner's 8 slots, "
          "all live, none 409-pending",
          code == 200 and st["connected"] and st["configured"]
          and [(c["slot"], c["label"], c["channel"])
               for c in st["channels"]] == PICKS
          and all(c["muted"] is False for c in st["channels"]))
    for slot, label, chan in PICKS:
        code, r = req("POST", SVC + "/api/wing/mute",
                      {"slot": slot, "muted": True})
        check(f"wing slot {slot} ({label}) mute set reads back "
              f"muted on Ch {chan}",
              code == 200 and r["muted"] is True
              and r["channel"] == chan and r["label"] == label)
        code, r = req("POST", SVC + "/api/wing/mute", {"slot": slot})
        check(f"wing slot {slot} ({label}) toggle reads back live",
              code == 200 and r["muted"] is False
              and r["channel"] == chan)
    code, _ = req("POST", SVC + "/api/wing/mute", {"slot": 9})
    check("wing slot 9 does not exist -> 422, not a guess",
          code == 422)

    # Unassigned-slot behaviour, in-process (the live service runs
    # with all eight assigned): the service must refuse with the
    # error the endpoint maps to 409.

    class CfgStub:
        mock = False
        wing_host = ""
        wing_port = 2223
        wing_channels = (None, 5, None, None, None, None, None, None)
        wing_names = ("Fav 1", "Fav 2", "Fav 3", "Fav 4",
                      "Fav 5", "Fav 6", "Fav 7", "Fav 8")

    svc = wing.WingService(CfgStub())
    st = asyncio.run(svc.status())
    check("wing unconfigured: status says so, slots are placeholders",
          st["configured"] is False
          and st["channels"][0]["muted"] is None
          and st["channels"][1]["channel"] == 5)
    try:
        asyncio.run(svc.set_mute(1, True))
        check("wing unassigned slot refuses (WingNotAssigned)", False)
    except wing.WingNotAssigned:
        check("wing unassigned slot refuses (WingNotAssigned)", True)

    code, page = req("GET", SVC + "/dashboard")
    check("/dashboard serves the dashboard view",
          code == 200 and "Service Dashboard" in page.get("__text", ""))
    code, page = req("GET", SVC + "/")
    check("camera view intact, now with the view switcher",
          code == 200 and "PTZOptics Camera" in page.get("__text", "")
          and 'href="/dashboard"' in page.get("__text", ""))


# ---------------------------------------------------------------- UI

async def cdp_checks():
    print("ui: switcher, no-signal panes, decks, wing strip (CDP)")
    port = 19228
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
                    + urllib.parse.quote(SVC + "/dashboard"), method="PUT")
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
                await asyncio.sleep(3)

            async def cap(path):
                r = await cmd("Page.captureScreenshot", format="png")
                with open(path, "wb") as f:
                    f.write(base64.b64decode(r["data"]))

            async def wait_for(expr, timeout=10.0):
                t0 = time.time()
                while time.time() - t0 < timeout:
                    if await ev(expr):
                        return True
                    await asyncio.sleep(0.3)
                return False

            async def click(sel):
                await ev(f"document.querySelector('{sel}').click()")
                await asyncio.sleep(0.5)

            await cmd("Page.enable")
            await cmd("Runtime.enable")

            # ---------------- iPad: the booth size ----------------
            await nav(SVC + "/dashboard", 1180, 820)
            check("switcher present, Dashboard marked current",
                  await ev("""(() => {
                    const a = document.querySelector(
                      '.view-switch .vs-btn.active');
                    return a && a.textContent.trim() === 'Dashboard'
                      && !!document.querySelector(
                        '.view-switch a[href="/"]'); })()"""))
            check("both panes settle into their no-signal state "
                  "(no gateway in the sandbox)",
                  await wait_for("""(() =>
                    !document.getElementById('programNoSignal').hidden
                    && !document.getElementById(
                      'confidenceNoSignal').hidden)()""", 15))
            check("camera mini pane falls back to snapshots",
                  await wait_for("""(() =>
                    !document.getElementById('camSnap').hidden)()""",
                    15))
            # The API section already drove the fake (preview 2,
            # fade -> program 2), so assert the deck against the
            # live status rather than the seed state.
            _, live = req("GET", SVC + "/api/vmix/status")
            check("vMix deck lists the four inputs, tallies matching "
                  "the bridge's own state",
                  await wait_for(f"""(() => {{
                    const b = document.querySelectorAll(
                      '#vmixInputs .input-btn');
                    return b.length === 4
                      && b[{live['active']} - 1].classList.contains(
                           'is-program')
                      && b[{live['preview']} - 1].classList.contains(
                           'is-preview'); }})()"""))
            await click("#vmixInputs .input-btn:nth-child(3)")
            check("tapping Input 3 previews it",
                  await wait_for("""(() =>
                    document.querySelector(
                      '#vmixInputs .input-btn:nth-child(3)')
                      .classList.contains('is-preview'))()"""))
            await click("#vmixCut")
            check("CUT moves the program tally to Input 3",
                  await wait_for("""(() =>
                    document.querySelector(
                      '#vmixInputs .input-btn:nth-child(3)')
                      .classList.contains('is-program'))()"""))
            await click("#proNext")
            check("Proclaim Next flashes the mockup's readout: "
                  "Ch 1 · Note 62",
                  await wait_for("""(() =>
                    document.getElementById('midiFlash')
                      .textContent.includes('Note 62'))()"""))
            check("WING strip renders the owner's eight channels, "
                  "all live",
                  await wait_for("""(() => {
                    const c = document.querySelectorAll(
                      '#wingGrid .chan');
                    return c.length === 8 && ![...c].some(
                      (x) => x.classList.contains('muted'))
                      && c[1].textContent.includes('WIRELESS GRAY')
                      && c[7].textContent.includes('AUDIENCE'); })()"""))
            await click("#wingGrid .chan:nth-child(8) .mute-btn")
            check("slot 8 (AUDIENCE) mute flips the strip to MUTED",
                  await wait_for("""(() =>
                    document.querySelector(
                      '#wingGrid .chan:nth-child(8)')
                      .classList.contains('muted'))()"""))
            await click("#wingGrid .chan:nth-child(8) .mute-btn")
            check("slot 8 mute again flips it back LIVE",
                  await wait_for("""(() =>
                    !document.querySelector(
                      '#wingGrid .chan:nth-child(8)')
                      .classList.contains('muted'))()"""))

            def visca_frames():
                with urllib.request.urlopen(
                        MOCK_HTTP + "/__test__/visca",
                        timeout=10) as r:
                    # The mock logs frames as spaced hex.
                    return [f.replace(" ", "")
                            for f in json.loads(r.read())["log"]]

            before = len([f for f in visca_frames()
                          if f.startswith("8101043f02")])
            check("dashboard renders the camera presets",
                  await wait_for("""(() =>
                    document.querySelectorAll(
                      '#dashPresets .preset').length >= 8)()"""))
            await click("#dashPresets .preset")
            await asyncio.sleep(1.2)
            after = len([f for f in visca_frames()
                         if f.startswith("8101043f02")])
            check("a dashboard preset click reaches the mock camera "
                  "(VISCA recall frame)",
                  after > before, f"(recall frames {before} -> {after})")
            check("the action log recorded the evening's work",
                  await ev("""(() =>
                    document.getElementById('dashLog')
                      .textContent.includes('CUT'))()"""))
            await cap(os.path.join(ROOT, "ui-preview-dashboard-ipad.png"))

            # ---------------- desktop ----------------
            await nav(SVC + "/dashboard", 1440, 900, mobile=False)
            check("desktop: decks spread to three columns' content "
                  "without overflow",
                  await wait_for("""(() =>
                    document.querySelectorAll(
                      '#vmixInputs .input-btn').length === 4)()""")
                  and await ev("""(() =>
                    document.documentElement.scrollWidth
                      <= innerWidth + 1)()"""))
            await cap(os.path.join(ROOT,
                                   "ui-preview-dashboard-desktop.png"))

            # ---------------- phone ----------------
            await nav(SVC + "/dashboard", 390, 844)
            check("phone: single column, no horizontal overflow",
                  await ev("""(() =>
                    document.documentElement.scrollWidth
                      <= innerWidth + 1)()"""))
            check("phone: all decks and the WING strip still present "
                  "(parity, not a neutered view)",
                  await ev("""(() =>
                    !!document.getElementById('vmixCut')
                    && !!document.getElementById('proNext')
                    && document.querySelectorAll(
                         '#wingGrid .chan').length === 8
                    && document.querySelectorAll(
                         '#dashPresets .preset').length >= 8)()"""))
            await cap(os.path.join(ROOT,
                                   "ui-preview-dashboard-mobile.png"))

            # ---------------- camera view kept its switcher --------
            await nav(SVC + "/", 1180, 820)
            check("camera view: switcher present, Camera current, "
                  "page otherwise itself",
                  await ev("""(() => {
                    const a = document.querySelector(
                      '.view-switch .vs-btn.active');
                    return a && a.textContent.trim() === 'Camera'
                      && !!document.getElementById('presetGrid')
                      && !!document.getElementById('advToggle'); })()"""))
            check("preview screenshots written", True)
    finally:
        proc.terminate()


def main():
    unit_checks()
    wire_checks()
    env = dict(os.environ, PTZ_PORT="18095",
               PTZ_DB="/tmp/ptz-dashboard-test.db",
               PTZ_UPDATES_DIR="/tmp/ptz-dashboard-updates")
    if os.path.exists("/tmp/ptz-dashboard-test.db"):
        os.remove("/tmp/ptz-dashboard-test.db")
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
