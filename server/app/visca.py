"""VISCA command builders and reply parsers for the PTZOptics G2 (V63C).

Raw VISCA over TCP (port 5678), camera address 1 (0x81) — see RECON.md.
Pure functions only; no I/O. Byte templates cross-checked against the
legacy app's inline templates found in recon/visca-findings.txt.
"""
from __future__ import annotations

import math

CAM = 0x81  # camera address 1
TERM = 0xFF

# ------------------------------------------------------- position model
# Absolute pan/tilt positions are signed 16-bit values in the camera's
# own step units. The spans below are the camera's mechanical ranges
# (pan +/-170 deg, tilt -30..+90 deg), the same spans the mock camera
# integrates against: 2448/170 = 1296/90 = 432/30 = 14.4, so both axes
# share one scale — 14.4 position units per degree (0.0694 deg/unit).
PAN_MIN, PAN_MAX = -2448, 2448          # -170..+170 deg
TILT_MIN, TILT_MAX = -432, 1296         # -30..+90 deg
UNITS_PER_DEGREE = 14.4

# Zoom position: 0x0000 = full wide, 0x4000 = full optical tele (30x).
# Horizontal FOV endpoints for the PT30X (vendor spec): ~60.7 deg wide,
# ~3.4 deg tele. FOV narrows roughly geometrically with zoom position,
# so interpolate on a log curve between the endpoints.
ZOOM_OPTICAL_MAX = 0x4000
HFOV_WIDE_DEG = 60.7
HFOV_TELE_DEG = 3.4


def _cmd(*body: int) -> bytes:
    return bytes([CAM, *body, TERM])


# ---------------------------------------------------------------- drive ---

def pantilt_drive(pan: int, tilt: int, pan_speed: int, tilt_speed: int) -> bytes:
    """Continuous pan/tilt drive.

    pan:  -1 left, 0 stop, +1 right
    tilt: -1 down, 0 stop, +1 up
    speeds: pan 1..24, tilt 1..20 (VISCA limits)
    """
    pan_speed = max(1, min(24, int(pan_speed)))
    tilt_speed = max(1, min(20, int(tilt_speed)))
    x = { -1: 0x01, 0: 0x03, 1: 0x02 }[pan]
    y = { 1: 0x01, 0: 0x03, -1: 0x02 }[tilt]
    return _cmd(0x01, 0x06, 0x01, pan_speed, tilt_speed, x, y)


def pantilt_stop(pan_speed: int = 0x10, tilt_speed: int = 0x10) -> bytes:
    return _cmd(0x01, 0x06, 0x01,
                max(0, min(24, pan_speed)), max(0, min(20, tilt_speed)),
                0x03, 0x03)


def home() -> bytes:
    return _cmd(0x01, 0x06, 0x04)


def _pos_nibbles(value: int) -> list[int]:
    """Signed 16-bit position as 4 nibble bytes (0Y form)."""
    u = int(value) & 0xFFFF
    return [(u >> 12) & 0xF, (u >> 8) & 0xF, (u >> 4) & 0xF, u & 0xF]


def pantilt_absolute(pan: int, tilt: int,
                     pan_speed: int = 18, tilt_speed: int = 16) -> bytes:
    """Absolute position move (CAM_PanTilt Absolute Position).

    81 01 06 02 VV WW 0Y0Y0Y0Y 0Z0Z0Z0Z FF — VV/WW are the pan/tilt
    speeds (pan 1..24, tilt 1..20); positions clamp to the mechanical
    ranges above. Zoom is not part of the command and stays untouched.
    """
    pan = max(PAN_MIN, min(PAN_MAX, int(pan)))
    tilt = max(TILT_MIN, min(TILT_MAX, int(tilt)))
    return _cmd(0x01, 0x06, 0x02,
                max(1, min(24, int(pan_speed))),
                max(1, min(20, int(tilt_speed))),
                *_pos_nibbles(pan), *_pos_nibbles(tilt))


# --------------------------------------------------------- white balance

# CAM_WB mode values (81 01 04 35 pq FF), VISCA standard table. The
# wb-pick flow uses only "onepush" plus the trigger below.
WB_MODES = {"auto": 0x00, "indoor": 0x01, "outdoor": 0x02,
            "onepush": 0x03, "atw": 0x04, "manual": 0x05}


def wb_mode(mode: str) -> bytes:
    """Set the white-balance mode over VISCA (CAM_WB).

    VISCA is the WB source of truth on this camera: firmware 6.3.45's
    get_image_conf exposes no WB keys at all (verified live, Nov 7,
    2026), so the CGI WB group in imageconf.py stays dormant.
    """
    return _cmd(0x01, 0x04, 0x35, WB_MODES[mode])


def wb_onepush_trigger() -> bytes:
    """Fire one-push white balance: balance off whatever is at the
    center of the frame right now. Frame per the PTZOptics G2 command
    set (81 01 04 35 05 FF) — to be byte-confirmed against the real
    camera in the first live motion test window, like the preset slot
    byte was; nothing in this module has touched the real camera."""
    return _cmd(0x01, 0x04, 0x35, 0x05)


# ----------------------------------------------------- wb-pick geometry
# "Pick a white point": the user taps a point (x, y — normalized 0..1,
# origin top-left) that should be white. One Push WB reads the CENTER
# of the frame, so the camera must aim that point at the center: pan by
# dx * horizontal FOV, tilt by -dy * vertical FOV (screen y grows down,
# tilt position grows up). Assumes flip/mirror off (the camera's
# standing baseline); near-center is enough — the camera averages a
# center region, so a degree or two of model error is harmless.

def hfov_degrees(zoom_pos: int) -> float:
    """Horizontal field of view (degrees) at a zoom position."""
    frac = max(0, min(ZOOM_OPTICAL_MAX, int(zoom_pos))) / ZOOM_OPTICAL_MAX
    return math.exp(math.log(HFOV_WIDE_DEG)
                    + frac * (math.log(HFOV_TELE_DEG)
                              - math.log(HFOV_WIDE_DEG)))


def vfov_degrees(hfov_deg: float) -> float:
    """Vertical FOV from horizontal for a 16:9 sensor."""
    return math.degrees(2 * math.atan(
        math.tan(math.radians(hfov_deg) / 2) * 9 / 16))


def wb_target(pan: int, tilt: int, zoom_pos: int,
              x: float, y: float) -> tuple[int, int]:
    """Absolute pan/tilt that brings the tapped point to frame center,
    clamped to the camera's mechanical limits. Zoom is untouched."""
    hfov = hfov_degrees(zoom_pos)
    vfov = vfov_degrees(hfov)
    d_pan = round((x - 0.5) * hfov * UNITS_PER_DEGREE)
    d_tilt = round(-(y - 0.5) * vfov * UNITS_PER_DEGREE)
    return (max(PAN_MIN, min(PAN_MAX, int(pan) + d_pan)),
            max(TILT_MIN, min(TILT_MAX, int(tilt) + d_tilt)))


def zoom_drive(direction: int, speed: int) -> bytes:
    """direction: +1 tele, -1 wide, 0 stop. speed: 0..7 (variable-speed form)."""
    if direction == 0:
        return _cmd(0x01, 0x04, 0x07, 0x00)
    p = max(0, min(7, int(speed)))
    return _cmd(0x01, 0x04, 0x07, (0x20 if direction > 0 else 0x30) | p)


def zoom_stop() -> bytes:
    return _cmd(0x01, 0x04, 0x07, 0x00)


# ---------------------------------------------------------------- focus ---

def focus_drive(direction: int) -> bytes:
    """+1 far, -1 near, 0 stop."""
    return _cmd(0x01, 0x04, 0x08, {1: 0x02, -1: 0x03, 0: 0x00}[direction])


def focus_mode(auto: bool) -> bytes:
    return _cmd(0x01, 0x04, 0x38, 0x02 if auto else 0x03)


def focus_onepush() -> bytes:
    return _cmd(0x01, 0x04, 0x18, 0x01)


# -------------------------------------------------------------- presets ---

def _preset(mode: int, slot: int) -> bytes:
    # mode: 0 clear, 1 set/store, 2 recall. Slot byte is the preset number
    # itself (1..127) — matches how the legacy app's settings dialog
    # addresses slots 1..127 (RECON.md). To be confirmed byte-for-byte in
    # the first live motion test window (Phase 3); inquiries cannot
    # distinguish a 0-based offset, presets are never recalled blind.
    slot = max(0, min(127, int(slot)))
    return _cmd(0x01, 0x04, 0x3F, mode, slot)


def preset_recall(slot: int) -> bytes:
    return _preset(2, slot)


def preset_set(slot: int) -> bytes:
    return _preset(1, slot)


def preset_clear(slot: int) -> bytes:
    return _preset(0, slot)


# ------------------------------------------------------------- inquiries ---

INQ_POWER = _cmd(0x09, 0x04, 0x00)
INQ_PANTILT_POS = _cmd(0x09, 0x06, 0x12)
INQ_ZOOM_POS = _cmd(0x09, 0x04, 0x47)
INQ_FOCUS_POS = _cmd(0x09, 0x04, 0x48)
INQ_FOCUS_MODE = _cmd(0x09, 0x04, 0x38)


# --------------------------------------------------------------- parsing ---

def split_frames(buffer: bytes) -> tuple[list[bytes], bytes]:
    """Split a byte stream into complete VISCA frames (0xFF-terminated)."""
    frames = []
    while True:
        i = buffer.find(bytes([TERM]))
        if i < 0:
            return frames, buffer
        frames.append(buffer[: i + 1])
        buffer = buffer[i + 1:]


def frame_kind(frame: bytes) -> str:
    """Classify a reply frame: ack / completion / reply / error / unknown."""
    if len(frame) < 3 or frame[0] & 0xF0 != 0x90:
        return "unknown"
    b = frame[1] & 0xF0
    if b == 0x40:
        return "ack"
    if b == 0x50:
        # A 3-byte 9x 5y FF frame is a completion; longer is an inquiry reply.
        return "completion" if len(frame) == 3 else "reply"
    if b == 0x60:
        return "error"
    return "unknown"


def _nibble_value(frame: bytes, start: int, count: int) -> int:
    v = 0
    for i in range(count):
        v = (v << 4) | (frame[start + i] & 0x0F)
    return v


def _signed16(v: int) -> int:
    return v - 0x10000 if v & 0x8000 else v


def parse_power(frame: bytes) -> bool | None:
    if frame_kind(frame) == "reply" and len(frame) >= 4:
        return frame[2] == 0x02
    return None


def parse_zoom_pos(frame: bytes) -> int | None:
    if frame_kind(frame) == "reply" and len(frame) >= 7:
        return _nibble_value(frame, 2, 4)
    return None


def parse_pantilt_pos(frame: bytes) -> tuple[int, int] | None:
    if frame_kind(frame) == "reply" and len(frame) >= 11:
        pan = _signed16(_nibble_value(frame, 2, 4))
        tilt = _signed16(_nibble_value(frame, 6, 4))
        return pan, tilt
    return None


def parse_focus_mode(frame: bytes) -> bool | None:
    if frame_kind(frame) == "reply" and len(frame) >= 4:
        return frame[2] == 0x02  # 02 = auto, 03 = manual
    return None
