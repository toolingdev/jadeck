"""Camera connection manager: the single owner of the VISCA TCP connection.

Smooth-control design (the owner's requirement — no jitter):

* ONE persistent TCP connection, opened once and kept alive with automatic
  reconnect. Commands never reconnect per call.
* Byte frames are written atomically under a single lock, so frames from
  concurrent tasks can never interleave on the wire.
* Drive (press-and-hold) is a *state*, not a stream of requests. Clients
  send their desired drive vector whenever it changes; a 25 Hz control
  loop compares desired vs. applied and emits a VISCA drive command only
  when something actually changed — pointer jitter and chatty clients are
  coalesced away before they can reach the camera.
* Speed ramps in over ~200 ms at drive start instead of slamming to full
  speed, which is what makes starts/stops feel smooth rather than jerky.
* Dead-man's switch: if the driving client goes silent (closed tab, lost
  Wi-Fi) for > 1.2 s, the camera is stopped automatically.
* Discrete commands (preset recall, home, focus) force-stop any active
  drive first, then go through the same serialized path.
"""
from __future__ import annotations

import asyncio
import logging
import re
import time
import urllib.request
from dataclasses import dataclass, field

from . import visca
from .config import Config

log = logging.getLogger("ptz.camera")

TICK = 0.04            # 25 Hz control loop
DEADMAN_S = 1.2        # stop if the driving client goes silent
RAMP_STEP = 9          # speed units (of 100) gained per tick at drive start

# Pick-a-white-point flow (see wb_pick): moderate absolute-move speeds,
# settle polling, and how close is close enough. One Push WB averages a
# center region, so the aim tolerance is deliberately loose; the return
# tolerance is tighter because "back where it was" is the promise.
WB_PAN_SPEED = 18
WB_TILT_SPEED = 16
WB_SETTLE_TIMEOUT_S = 3.0
WB_SETTLE_POLL_S = 0.2
WB_TRIGGER_SETTLE_S = 1.5
WB_AIM_TOLERANCE = 24        # position units (~1.7 deg)
WB_RETURN_TOLERANCE = 8      # position units (~0.6 deg)


class WbPickError(Exception):
    """The pick-a-white-point flow could not complete."""


class WbBusyError(WbPickError):
    """Another camera operation is already running."""


@dataclass
class DriveVector:
    pan: int = 0       # -1/0/+1
    tilt: int = 0
    zoom: int = 0
    speed: int = 40    # 5..100 from the UI slider

    def active(self) -> bool:
        return bool(self.pan or self.tilt or self.zoom)

    def key(self) -> tuple:
        return (self.pan, self.tilt, self.zoom)


@dataclass
class CameraStatus:
    connected: bool = False
    driving: bool = False
    power: bool | None = None
    pan: int | None = None
    tilt: int | None = None
    zoom: int | None = None
    focus_auto: bool | None = None
    device: dict = field(default_factory=dict)
    last_error: str = ""


class CameraService:
    def __init__(self, cfg: Config):
        self.cfg = cfg
        # Camera HTTP (CGI + snapshot). Firmware 6.3.45+ requires digest
        # auth on param.cgi and snapshot.jpg; older firmware and the mock
        # answer without auth. One opener for every camera HTTP read:
        # digest handler installed only when credentials are configured.
        if cfg.camera_user:
            mgr = urllib.request.HTTPPasswordMgrWithDefaultRealm()
            mgr.add_password(None, cfg.camera_http,
                             cfg.camera_user, cfg.camera_pass)
            self._http = urllib.request.build_opener(
                urllib.request.HTTPDigestAuthHandler(mgr))
        else:
            self._http = urllib.request.build_opener()
        self.status = CameraStatus()
        self._reader: asyncio.StreamReader | None = None
        self._writer: asyncio.StreamWriter | None = None
        self._write_lock = asyncio.Lock()
        self._inquiry_lock = asyncio.Lock()
        self._reply_waiter: asyncio.Future | None = None
        self._completion = asyncio.Event()
        self._tasks: list[asyncio.Task] = []
        self._desired = DriveVector()
        self._applied_key = (0, 0, 0)
        self._applied_speed = 0.0       # ramped speed actually in effect
        self._last_drive_msg = 0.0
        self._drive_owner: str | None = None
        self._device_cache_at = 0.0
        self.on_change = None           # async callback(status) for WS fanout
        # Operation lock: held for the whole pick-a-white-point flow so
        # nothing else can move the camera mid-sequence. While held,
        # drive input is dropped (not queued) and the discrete REST
        # commands answer 409 — the simpler safe option, documented in
        # PHASE2.md.
        self._op_lock = asyncio.Lock()

    @property
    def operation_active(self) -> bool:
        return self._op_lock.locked()

    # ---------------------------------------------------------- lifecycle

    async def start(self):
        self._tasks = [
            asyncio.create_task(self._connection_loop(), name="visca-conn"),
            asyncio.create_task(self._control_loop(), name="drive-loop"),
            asyncio.create_task(self._poll_loop(), name="inquiry-poll"),
        ]

    async def stop(self):
        for t in self._tasks:
            t.cancel()
        await self._close()

    async def _connection_loop(self):
        while True:
            try:
                self._reader, self._writer = await asyncio.wait_for(
                    asyncio.open_connection(self.cfg.camera_host,
                                            self.cfg.camera_port),
                    timeout=5)
                self.status.connected = True
                self.status.last_error = ""
                log.info("VISCA connected to %s:%s",
                         self.cfg.camera_host, self.cfg.camera_port)
                await self._notify()
                await self._read_loop()
            except asyncio.CancelledError:
                raise
            except Exception as e:  # reconnect with backoff, forever
                self.status.last_error = f"connect: {e}"
            self.status.connected = False
            self._applied_key = (0, 0, 0)
            self._applied_speed = 0.0
            await self._notify()
            await asyncio.sleep(2)

    async def _close(self):
        if self._writer is not None:
            try:
                self._writer.close()
            except Exception:
                pass
            self._writer = None

    async def _read_loop(self):
        buf = b""
        assert self._reader is not None
        while True:
            data = await self._reader.read(4096)
            if not data:
                raise ConnectionError("camera closed the connection")
            frames, buf = visca.split_frames(buf + data)
            for f in frames:
                kind = visca.frame_kind(f)
                if kind == "reply" and self._reply_waiter is not None \
                        and not self._reply_waiter.done():
                    self._reply_waiter.set_result(f)
                elif kind == "completion":
                    self._completion.set()
                elif kind == "error":
                    self.status.last_error = f"VISCA error {f.hex(' ')}"
                    log.warning("camera error reply: %s", f.hex(" "))

    # ------------------------------------------------------------- sending

    async def _write(self, frame: bytes):
        if self._writer is None:
            raise ConnectionError("not connected")
        async with self._write_lock:
            self._writer.write(frame)
            await self._writer.drain()

    async def send_command(self, frame: bytes, wait: bool = True):
        """Discrete command: serialized, waits briefly for completion."""
        await self._write(frame)
        if wait and self.status.connected:
            self._completion.clear()
            try:
                await asyncio.wait_for(self._completion.wait(), timeout=2.0)
            except asyncio.TimeoutError:
                pass  # cameras don't always complete promptly; not fatal

    async def inquire(self, frame: bytes) -> bytes | None:
        """Send an inquiry and return its reply frame (or None on timeout)."""
        async with self._inquiry_lock:
            if not self.status.connected:
                return None
            loop = asyncio.get_running_loop()
            self._reply_waiter = loop.create_future()
            try:
                await self._write(frame)
                return await asyncio.wait_for(self._reply_waiter, timeout=1.5)
            except (asyncio.TimeoutError, ConnectionError):
                return None
            finally:
                self._reply_waiter = None

    # -------------------------------------------------------------- drive

    def set_drive(self, vec: DriveVector, owner: str):
        """Latest-wins drive state from a client. Coalescing happens here:
        no matter how often this is called, the control loop emits at most
        one command per actual change."""
        if self._op_lock.locked():
            # A sequenced operation (pick-a-white-point) owns the camera;
            # drive input is dropped, never queued into the sequence.
            return
        self._desired = vec
        self._last_drive_msg = time.monotonic()
        if vec.active():
            self._drive_owner = owner

    async def _control_loop(self):
        while True:
            await asyncio.sleep(TICK)
            try:
                await self._control_tick()
            except asyncio.CancelledError:
                raise
            except Exception as e:
                log.debug("control tick: %s", e)

    async def _control_tick(self):
        now = time.monotonic()
        desired = self._desired
        # Dead-man's switch
        if desired.active() and now - self._last_drive_msg > DEADMAN_S:
            log.warning("dead-man: driving client silent, stopping camera")
            desired = self._desired = DriveVector(speed=desired.speed)
        if not self.status.connected:
            return
        was_driving = self.status.driving
        if not desired.active():
            if self._applied_key != (0, 0, 0) or self._applied_speed > 0:
                await self._write(visca.pantilt_stop())
                await self._write(visca.zoom_stop())
                self._applied_key = (0, 0, 0)
                self._applied_speed = 0.0
            self.status.driving = False
        else:
            # Ramp the speed toward the target for smooth starts.
            target = float(max(5, min(100, desired.speed)))
            if self._applied_speed < target:
                self._applied_speed = min(target,
                                          self._applied_speed + RAMP_STEP)
            else:
                self._applied_speed = target
            eff = self._applied_speed
            pan_sp = max(1, round(24 * eff / 100))
            tilt_sp = max(1, round(20 * eff / 100))
            zoom_sp = max(1, round(7 * eff / 100))
            sig = (desired.key(), pan_sp, tilt_sp, zoom_sp)
            if sig != getattr(self, "_applied_sig", None):
                if desired.pan or desired.tilt:
                    await self._write(visca.pantilt_drive(
                        desired.pan, desired.tilt, pan_sp, tilt_sp))
                elif self._applied_key[0] or self._applied_key[1]:
                    await self._write(visca.pantilt_stop(pan_sp, tilt_sp))
                if desired.zoom:
                    await self._write(visca.zoom_drive(desired.zoom, zoom_sp))
                elif self._applied_key[2]:
                    await self._write(visca.zoom_stop())
                self._applied_sig = sig
            self._applied_key = desired.key()
            self.status.driving = True
        if was_driving != self.status.driving:
            await self._notify()

    async def force_stop(self):
        self._desired = DriveVector(speed=self._desired.speed)
        if self.status.connected and (self._applied_key != (0, 0, 0)
                                      or self._applied_speed > 0):
            try:
                await self._write(visca.pantilt_stop())
                await self._write(visca.zoom_stop())
            except ConnectionError:
                pass
            self._applied_key = (0, 0, 0)
            self._applied_speed = 0.0
            self._applied_sig = None
        self.status.driving = False

    # ------------------------------------------------------ discrete moves

    async def home(self):
        await self.force_stop()
        await self.send_command(visca.home())

    async def recall_preset(self, slot: int):
        await self.force_stop()
        await self.send_command(visca.preset_recall(slot))

    async def store_preset(self, slot: int):
        await self.force_stop()
        await self.send_command(visca.preset_set(slot))

    async def clear_preset(self, slot: int):
        await self.force_stop()
        await self.send_command(visca.preset_clear(slot))

    async def focus(self, action: str):
        if action == "auto":
            await self.send_command(visca.focus_mode(True))
            self.status.focus_auto = True
        elif action == "manual":
            await self.send_command(visca.focus_mode(False))
            self.status.focus_auto = False
        elif action == "onepush":
            await self.send_command(visca.focus_onepush())
        elif action in ("near", "far", "stop"):
            d = {"near": -1, "far": 1, "stop": 0}[action]
            await self.send_command(visca.focus_drive(d), wait=False)
        await self._notify()

    # ------------------------------------------- pick a white point
    # the owner's feature (Nov 7, 2026): the user taps a point in the
    # picture that should be pure white. One Push WB reads the CENTER
    # of the frame, so the flow is: remember exactly where the camera
    # is, aim the tapped point at the center, fire One Push, come back.
    # WB goes over VISCA, not CGI — get_image_conf on this camera has
    # no WB keys (verified live on 6.3.45). The whole flow holds the
    # operation lock; on any failure after motion began, the return
    # move is still attempted before the error is reported.

    async def _read_position(self) -> tuple[int, int, int] | None:
        """Fresh (pan, tilt, zoom) from inquiries, falling back to the
        continuously polled status values."""
        pan = tilt = zoom = None
        f = await self.inquire(visca.INQ_PANTILT_POS)
        if f:
            pos = visca.parse_pantilt_pos(f)
            if pos:
                pan, tilt = pos
        f = await self.inquire(visca.INQ_ZOOM_POS)
        if f:
            zoom = visca.parse_zoom_pos(f)
        if pan is None:
            pan, tilt = self.status.pan, self.status.tilt
        if zoom is None:
            zoom = self.status.zoom
        if pan is None or tilt is None or zoom is None:
            return None
        return pan, tilt, zoom

    async def _wait_settled(self, target: tuple[int, int],
                            tolerance: int, what: str) -> tuple[int, int]:
        """Poll position until two reads ~200 ms apart agree (settled),
        then require the result to be within `tolerance` of target."""
        deadline = time.monotonic() + WB_SETTLE_TIMEOUT_S
        prev: tuple[int, int] | None = None
        last: tuple[int, int] | None = None
        while True:
            f = await self.inquire(visca.INQ_PANTILT_POS)
            if f:
                pos = visca.parse_pantilt_pos(f)
                if pos:
                    last = pos
                    if prev is not None and pos == prev:
                        break
                    prev = pos
            if time.monotonic() >= deadline:
                break
            await asyncio.sleep(WB_SETTLE_POLL_S)
        if last is None:
            raise WbPickError(f"camera stopped answering during {what}")
        if abs(last[0] - target[0]) > tolerance \
                or abs(last[1] - target[1]) > tolerance:
            raise WbPickError(
                f"camera did not reach the {what} position "
                f"(at {last[0]},{last[1]}, wanted {target[0]},{target[1]})")
        return last

    async def wb_pick(self, x: float, y: float) -> dict:
        """Aim the tapped point (normalized 0..1, origin top-left) at
        the frame center, fire One Push WB over VISCA, and return the
        camera to exactly where it was. Zoom is never touched."""
        if not self.status.connected:
            raise WbPickError("camera not connected")
        if self._op_lock.locked():
            raise WbBusyError("another camera operation is in progress")
        async with self._op_lock:
            await self.force_stop()
            origin = await self._read_position()
            if origin is None:
                raise WbPickError("could not read the camera position")
            pan0, tilt0, zoom0 = origin
            target = visca.wb_target(pan0, tilt0, zoom0, x, y)
            moved = target != (pan0, tilt0)
            try:
                await self.send_command(visca.wb_mode("onepush"))
                if moved:
                    await self.send_command(visca.pantilt_absolute(
                        target[0], target[1], WB_PAN_SPEED, WB_TILT_SPEED))
                    await self._wait_settled(target, WB_AIM_TOLERANCE,
                                             "aim")
                await self.send_command(visca.wb_onepush_trigger())
                await asyncio.sleep(WB_TRIGGER_SETTLE_S)
            except Exception as e:
                # Whatever went wrong, try to put the camera back
                # before reporting anything.
                try:
                    if moved:
                        await self.send_command(visca.pantilt_absolute(
                            pan0, tilt0, WB_PAN_SPEED, WB_TILT_SPEED))
                        await self._wait_settled((pan0, tilt0),
                                                 WB_RETURN_TOLERANCE,
                                                 "return")
                except Exception:
                    pass
                if isinstance(e, WbPickError):
                    raise
                raise WbPickError(f"white-balance pick failed: {e}")
            final = (pan0, tilt0)
            if moved:
                await self.send_command(visca.pantilt_absolute(
                    pan0, tilt0, WB_PAN_SPEED, WB_TILT_SPEED))
                final = await self._wait_settled(
                    (pan0, tilt0), WB_RETURN_TOLERANCE, "return")
            await self._notify()
            return {
                "ok": True,
                "tapped": {"x": x, "y": y},
                "from": {"pan": pan0, "tilt": tilt0, "zoom": zoom0},
                "aimedAt": {"pan": target[0], "tilt": target[1]},
                "returnedTo": {"pan": final[0], "tilt": final[1]},
            }

    # -------------------------------------------------------------- status

    async def _poll_loop(self):
        while True:
            await asyncio.sleep(0.6)
            if not self.status.connected:
                continue
            try:
                changed = False
                f = await self.inquire(visca.INQ_PANTILT_POS)
                if f:
                    pos = visca.parse_pantilt_pos(f)
                    if pos and pos != (self.status.pan, self.status.tilt):
                        self.status.pan, self.status.tilt = pos
                        changed = True
                f = await self.inquire(visca.INQ_ZOOM_POS)
                if f:
                    z = visca.parse_zoom_pos(f)
                    if z is not None and z != self.status.zoom:
                        self.status.zoom = z
                        changed = True
                if changed:
                    await self._notify()
            except asyncio.CancelledError:
                raise
            except Exception:
                pass

    async def refresh_static(self):
        """Power + focus mode + device info (CGI), refreshed lazily."""
        if self.status.connected:
            f = await self.inquire(visca.INQ_POWER)
            if f:
                self.status.power = visca.parse_power(f)
            f = await self.inquire(visca.INQ_FOCUS_MODE)
            if f:
                self.status.focus_auto = visca.parse_focus_mode(f)
        if time.monotonic() - self._device_cache_at > 30 or \
                not self.status.device:
            self.status.device = await self._fetch_device_conf()
            self._device_cache_at = time.monotonic()

    async def _fetch_device_conf(self) -> dict:
        url = f"{self.cfg.camera_http}/cgi-bin/param.cgi?get_device_conf"
        def _get() -> str:
            with self._http.open(url, timeout=4) as r:
                return r.read().decode("utf-8", "replace")
        try:
            text = await asyncio.get_running_loop().run_in_executor(
                None, _get)
        except Exception as e:
            log.debug("device_conf fetch failed: %s", e)
            return self.status.device
        out = {}
        for k, v in re.findall(r'(\w+)\s*=\s*"([^"]*)"', text):
            out[k] = v
        return out

    # ------------------------------------------------- image config (CGI)
    # Read-modify-write over param.cgi: fetch the camera's whole image
    # conf, merge the changed keys, post the whole conf back — untouched
    # keys (including ones we don't model) return byte-identical, the
    # same behavior as the camera's own web-UI form.

    async def _http_get(self, url: str) -> str | None:
        def _get() -> str:
            with self._http.open(url, timeout=5) as r:
                return r.read().decode("utf-8", "replace")
        try:
            return await asyncio.get_running_loop().run_in_executor(
                None, _get)
        except Exception as e:
            log.debug("camera HTTP GET failed (%s): %s", url, e)
            return None

    async def fetch_image_conf(self):
        """The camera's raw image conf (ordered key -> raw string)."""
        from . import imageconf
        text = await self._http_get(
            f"{self.cfg.camera_http}/cgi-bin/param.cgi?get_image_conf")
        if text is None:
            return None
        return imageconf.parse_conf(text)

    async def post_image_conf(self, conf) -> bool:
        from urllib.parse import quote
        qs = "post_image_conf" + "".join(
            f"&{k}={quote(str(v), safe='')}" for k, v in conf.items())
        text = await self._http_get(
            f"{self.cfg.camera_http}/cgi-bin/param.cgi?{qs}")
        return text is not None

    async def wb_trigger(self) -> bool:
        """One-push white balance: point at something white, fire once.
        Documented G2 CGI form: post_image_value&wbmode&trigger."""
        text = await self._http_get(
            f"{self.cfg.camera_http}/cgi-bin/param.cgi"
            "?post_image_value&wbmode&trigger")
        return text is not None

    async def fetch_snapshot(self) -> bytes | None:
        url = f"{self.cfg.camera_http}/snapshot.jpg"
        def _get() -> bytes:
            with self._http.open(url, timeout=5) as r:
                return r.read()
        try:
            return await asyncio.get_running_loop().run_in_executor(None, _get)
        except Exception as e:
            log.debug("snapshot fetch failed: %s", e)
            return None

    def snapshot_status(self) -> dict:
        s = self.status
        return {
            "connected": s.connected,
            "driving": s.driving,
            "power": s.power,
            "pan": s.pan, "tilt": s.tilt, "zoom": s.zoom,
            "focusAuto": s.focus_auto,
            "device": s.device,
            "error": s.last_error,
        }

    async def _notify(self):
        if self.on_change is not None:
            try:
                await self.on_change(self.snapshot_status())
            except Exception:
                pass
