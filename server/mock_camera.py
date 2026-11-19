"""A tiny mock VISCA camera for developing/testing without the real one.

- TCP VISCA responder on 127.0.0.1:15678: ACKs + completes commands,
  answers inquiries, and *integrates drive commands into simulated
  pan/tilt/zoom positions* so the UI's position readout actually moves
  while you hold the pad. Absolute-position moves animate toward their
  target; CAM_WB mode/trigger frames are recorded; every command frame
  lands in a log the tests read back via /__test__/visca (with
  /__test__/reset and /__test__/dropabs?n= for failure injection).
- HTTP on 127.0.0.1:18081: get_device_conf + snapshot.jpg, same shapes
  the real camera returns.

Run:  python3 server/mock_camera.py     (then PTZ_MOCK=1 python3 server/run.py)
Logs every VISCA frame received to stdout — watch command counts to prove
the service's coalescing (a 1 s hold should produce a handful of frames,
not dozens).
"""
from __future__ import annotations

import asyncio
import os
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from app import visca  # noqa: E402

TCP_PORT = 15678
HTTP_PORT = 18081
ASSET = os.path.join(os.path.dirname(os.path.abspath(__file__)),
                     "assets", "mock-snapshot.jpg")

state = {
    "pan": 0, "tilt": 0, "zoom": 1000,
    "pan_v": 0, "tilt_v": 0, "zoom_v": 0,
    "presets": {},   # slot -> (pan, tilt, zoom)
    "focus_auto": True,
    "frames": 0,
    # Pick-a-white-point support: absolute-move target the integrator
    # animates toward, WB mode/trigger bookkeeping, a log of every
    # command frame (inquiries excluded) for sequence assertions, and
    # a failure-injection counter: when drop_abs_at > 0, the Nth
    # absolute move from now is ACKed but silently not applied.
    "abs_target": None,
    "wb_mode_byte": None,
    "wb_visca_triggers": 0,
    "cmd_log": [],
    "drop_abs_at": 0,
    "abs_seen": 0,
}


def nibbles(value: int, count: int) -> list[int]:
    return [(value >> (4 * (count - 1 - i))) & 0xF for i in range(count)]


def reply_inquiry(frame: bytes) -> bytes | None:
    if frame == visca.INQ_POWER:
        return bytes([0x90, 0x50, 0x02, 0xFF])
    if frame == visca.INQ_PANTILT_POS:
        p = state["pan"] & 0xFFFF
        t = state["tilt"] & 0xFFFF
        return bytes([0x90, 0x50, *nibbles(p, 4), *nibbles(t, 4), 0xFF])
    if frame == visca.INQ_ZOOM_POS:
        return bytes([0x90, 0x50, *nibbles(state["zoom"] & 0xFFFF, 4), 0xFF])
    if frame == visca.INQ_FOCUS_MODE:
        return bytes([0x90, 0x50, 0x02 if state["focus_auto"] else 0x03, 0xFF])
    if frame == visca.INQ_FOCUS_POS:
        return bytes([0x90, 0x50, *nibbles(0x1000, 4), 0xFF])
    return None


def _signed16(v: int) -> int:
    return v - 0x10000 if v & 0x8000 else v


def _nibble_value(frame: bytes, start: int, count: int) -> int:
    v = 0
    for i in range(count):
        v = (v << 4) | (frame[start + i] & 0x0F)
    return v


def apply_command(frame: bytes):
    if len(frame) >= 15 and frame[1:4] == bytes([0x01, 0x06, 0x02]):
        # Absolute position move: animate toward the target in
        # integrate() so the service's settle polling sees the camera
        # actually travel, then hold still.
        state["abs_seen"] += 1
        if state["drop_abs_at"] and \
                state["abs_seen"] == state["drop_abs_at"]:
            print("[mock] DROPPING absolute move (failure injection)",
                  flush=True)
            state["drop_abs_at"] = 0
            return
        state["abs_target"] = (_signed16(_nibble_value(frame, 6, 4)),
                               _signed16(_nibble_value(frame, 10, 4)))
        state["pan_v"] = state["tilt_v"] = 0
    elif len(frame) >= 5 and frame[1:4] == bytes([0x01, 0x04, 0x35]):
        # CAM_WB: mode set, or the one-push trigger frame (0x05).
        state["wb_mode_byte"] = frame[4]
        if frame[4] == 0x05:
            state["wb_visca_triggers"] += 1
    elif len(frame) >= 4 and frame[1] == 0x01 and frame[2] == 0x06 \
            and frame[3] == 0x01:  # pan/tilt drive
        vv, ww, x, y = frame[4], frame[5], frame[6], frame[7]
        state["pan_v"] = 0 if x == 3 else (-vv if x == 1 else vv)
        state["tilt_v"] = 0 if y == 3 else (ww if y == 1 else -ww)
    elif len(frame) >= 4 and frame[1:4] == bytes([0x01, 0x04, 0x07]):  # zoom
        b = frame[4]
        if b == 0:
            state["zoom_v"] = 0
        elif b & 0xF0 == 0x20:
            state["zoom_v"] = (b & 0x0F) + 1
        elif b & 0xF0 == 0x30:
            state["zoom_v"] = -((b & 0x0F) + 1)
    elif len(frame) >= 5 and frame[1:4] == bytes([0x01, 0x04, 0x3F]):  # preset
        mode, slot = frame[4], frame[5]
        if mode == 1:
            state["presets"][slot] = (state["pan"], state["tilt"],
                                      state["zoom"])
        elif mode == 2 and slot in state["presets"]:
            state["pan"], state["tilt"], state["zoom"] = \
                state["presets"][slot]
        elif mode == 0:
            state["presets"].pop(slot, None)
    elif len(frame) >= 4 and frame[1:4] == bytes([0x01, 0x04, 0x38]):
        state["focus_auto"] = frame[4] == 0x02
    elif len(frame) >= 4 and frame[1:4] == bytes([0x01, 0x06, 0x04]):  # home
        state["pan"], state["tilt"] = 0, 0


async def handle(reader: asyncio.StreamReader, writer: asyncio.StreamWriter):
    print("[mock] client connected", flush=True)
    buf = b""
    try:
        while True:
            data = await reader.read(4096)
            if not data:
                break
            frames, buf = visca.split_frames(buf + data)
            for f in frames:
                state["frames"] += 1
                print(f"[mock] rx {f.hex(' ')}", flush=True)
                if len(f) >= 2 and f[1] == 0x01:  # command (not inquiry)
                    state["cmd_log"].append(f.hex(" "))
                if f[1] == 0x09:  # inquiry
                    r = reply_inquiry(f)
                    if r:
                        writer.write(r)
                        await writer.drain()
                else:
                    apply_command(f)
                    writer.write(bytes([0x90, 0x41, 0xFF]))   # ACK
                    writer.write(bytes([0x90, 0x51, 0xFF]))   # completion
                    await writer.drain()
    except ConnectionError:
        pass
    print("[mock] client disconnected", flush=True)


async def integrate():
    while True:
        await asyncio.sleep(0.05)
        if state["abs_target"] is not None:
            # Travel toward the absolute target at a finite rate
            # (~6000 units/s) so settle polling observes motion, then
            # an exact stop on the target.
            tp, tt = state["abs_target"]
            step = 300
            dp = max(-step, min(step, tp - state["pan"]))
            dt = max(-step, min(step, tt - state["tilt"]))
            state["pan"] += dp
            state["tilt"] += dt
            if dp == 0 and dt == 0:
                state["abs_target"] = None
        else:
            state["pan"] = max(-2448, min(2448,
                                          state["pan"] + state["pan_v"] * 2))
            state["tilt"] = max(-432, min(1296,
                                           state["tilt"] + state["tilt_v"] * 2))
        state["zoom"] = max(0, min(16384,
                                    state["zoom"] + state["zoom_v"] * 30))


DEVICE_CONF = """devname = "MockCam (PTZOptics sim)"
devtype = "V63C"
devmodel = "F16.HI"
serial = "mock00000001"
versioninfo = "SOC v6.3.45 - ARM v6.3.45THI (MOCK)"
"""

# Image conf state (param.cgi get_image_conf / post_image_conf).
# The image basics are the REAL camera's recorded values (Nov 7, 2026
# snapshot: bright 11, saturation 4, contrast 10, sharpness 3, hue 8,
# flip 0, mirror 0, ldc 0). WB/exposure/focus/NR keys were not in that
# snapshot, so they are seeded with platform defaults from the G2
# manual's menu examples — NOT read from the real camera. `ldc` is
# deliberately a key the service does not model: tests use it to prove
# read-modify-write posts it back byte-identical.
from collections import OrderedDict

image_conf = OrderedDict([
    ("bright", "11"), ("saturation", "4"), ("contrast", "10"),
    ("sharpness", "3"), ("hue", "8"), ("flip", "0"), ("mirror", "0"),
    ("ldc", "0"),
    ("wbmode", "auto"), ("rgain", "128"), ("bgain", "128"),
    ("rgtuning", "0"), ("bgtuning", "0"), ("awbsens", "normal"),
    ("colortemp", "5000"),
    ("aemode", "auto"), ("shutter", "1/250"), ("iris", "F5.6"),
    ("gain", "0"), ("expbright", "7"), ("expcompmode", "0"),
    ("expcomp", "0"), ("backlight", "0"), ("gainlimit", "3"),
    ("flicker", "off"), ("drc", "0"),
    ("afsens", "high"), ("afzone", "center"),
    ("nr2d", "3"), ("nr3d", "3"),
])
image_test = {"last_post": None, "wb_triggers": 0}


def serialize_image_conf() -> str:
    return "".join(f'{k} = "{v}"\n' for k, v in image_conf.items())


class HttpHandler(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def _send(self, body: bytes, ctype="text/plain", code=200):
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path.startswith("/__test__/visca"):
            import json
            self._send(json.dumps({
                "log": state["cmd_log"],
                "pan": state["pan"], "tilt": state["tilt"],
                "zoom": state["zoom"],
                "wb_mode_byte": state["wb_mode_byte"],
                "wb_visca_triggers": state["wb_visca_triggers"],
                "abs_seen": state["abs_seen"],
            }).encode(), "application/json")
        elif self.path.startswith("/__test__/reset"):
            from urllib.parse import parse_qs, urlparse
            q = parse_qs(urlparse(self.path).query)
            state["pan"] = int(q.get("pan", [state["pan"]])[0])
            state["tilt"] = int(q.get("tilt", [state["tilt"]])[0])
            state["zoom"] = int(q.get("zoom", [state["zoom"]])[0])
            state["pan_v"] = state["tilt_v"] = state["zoom_v"] = 0
            state["abs_target"] = None
            state["cmd_log"] = []
            state["wb_mode_byte"] = None
            state["wb_visca_triggers"] = 0
            state["abs_seen"] = 0
            state["drop_abs_at"] = 0
            self._send(b"OK")
        elif self.path.startswith("/__test__/dropabs"):
            from urllib.parse import parse_qs, urlparse
            q = parse_qs(urlparse(self.path).query)
            state["abs_seen"] = 0
            state["drop_abs_at"] = int(q.get("n", ["0"])[0])
            self._send(b"OK")
        elif self.path.startswith("/__test__/image"):
            import json
            self._send(json.dumps({
                "conf": dict(image_conf),
                "last_post": image_test["last_post"],
                "wb_triggers": image_test["wb_triggers"],
            }).encode(), "application/json")
        elif self.path.startswith("/cgi-bin/param.cgi"):
            query = self.path.split("?", 1)[1] if "?" in self.path else ""
            tokens = query.split("&") if query else []
            cmd = tokens[0] if tokens else ""
            if cmd == "get_image_conf":
                self._send(serialize_image_conf().encode())
            elif cmd == "post_image_conf":
                pairs = {}
                for tok in tokens[1:]:
                    if "=" in tok:
                        k, v = tok.split("=", 1)
                        from urllib.parse import unquote
                        pairs[unquote(k)] = unquote(v)
                image_conf.update(pairs)
                image_test["last_post"] = query
                print(f"[mock] post_image_conf {len(pairs)} keys",
                      flush=True)
                self._send(b"OK")
            elif cmd == "post_image_value" and "wbmode" in tokens \
                    and "trigger" in tokens:
                image_test["wb_triggers"] += 1
                print("[mock] wb trigger", flush=True)
                self._send(b"OK")
            else:
                self._send(DEVICE_CONF.encode())
        elif self.path.startswith("/snapshot.jpg"):
            try:
                with open(ASSET, "rb") as f:
                    body = f.read()
            except OSError:
                body = b""
            self.send_response(200 if body else 404)
            self.send_header("Content-Type", "image/jpeg")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        else:
            self.send_response(404)
            self.end_headers()


async def main():
    srv = await asyncio.start_server(handle, "127.0.0.1", TCP_PORT)
    http = ThreadingHTTPServer(("127.0.0.1", HTTP_PORT), HttpHandler)
    threading.Thread(target=http.serve_forever, daemon=True).start()
    print(f"[mock] VISCA on 127.0.0.1:{TCP_PORT}, HTTP on "
          f"127.0.0.1:{HTTP_PORT}", flush=True)
    asyncio.create_task(integrate())
    async with srv:
        await srv.serve_forever()


if __name__ == "__main__":
    asyncio.run(main())
