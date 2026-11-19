"""Camera image configuration: the get_image_conf / post_image_conf surface.

The camera exposes its image settings as a param.cgi conf — the same
key = "value" text family as get_device_conf (RECON.md §2). This module
is the single mapping table between that raw conf and the typed JSON
the web UI speaks:

* Reads: parse the conf, convert the keys we model to typed values.
* Writes: read-modify-write. The service fetches the camera's current
  conf, merges ONLY the fields the user changed, and posts the whole
  conf back — keys we do not model (e.g. ldc) ride along untouched,
  byte-identical, exactly as the camera's own web-UI form behaves.

Field/range sources (Nov 7, 2026):
* bright/saturation/contrast/sharpness/hue/flip/mirror/ldc keys and the
  live values they were seeded from: the real camera's param.cgi
  snapshot, ~/workspace/venue-av/ptzoptics-archive/camera-snapshot-2026-11-07/config.txt
* bright..hue range 0-14, flip/mirror 0/1, aemode and wbmode enums:
  PTZOptics "HTTP-CGI Control Sheet" Rev 1.4 (JDV 2021-10-15), the G2
  CGI reference RECON.md cross-checks against.
* rgain/bgain 0-255 (Manual WB), RG/BG tuning -10..+10 (Auto/Indoor/
  Outdoor), AWB sens Low/Middle/High, iris F-steps, shutter steps,
  bright 0-17, expcomp -7..7, gainlimit 0-15, DRC 0-8, AF sense
  Low/Normal/High, AF zone Top/Center/Bottom, NR2D Off/Auto/1-5,
  NR3D Off/1-8: PTZOptics G2 user manual (PT12X-SDI-G2, Rev 1.2) OSD
  menus + VISCA inquiry tables — the same platform family as our
  PT30X-NDI-G2 (devtype V63C).

LIVE RECONCILIATION (Nov 7, 2026, real camera on SOC v6.3.45, digest
auth read of get_image_conf via the office PC): the conf contains
EXACTLY eight keys — bright, saturation, contrast, sharpness, hue,
ldc, flip, mirror. Every modeled image-basics key below is confirmed
verbatim against the real device, and ldc rides along unmodeled as
designed. The WB / exposure / focus / NR keys are NOT present in
get_image_conf at all (true on 6.3.12's snapshot too), so those groups
remain unverified reconstructions: to_typed() simply omits them, the
Advanced panel serves the confirmed basics, and the other groups stay
dormant until their real surface (a different conf, or per-value
post_image_value calls from the camera's own web UI) is identified.
Still unpinned for the same reason: the VAR color-temperature range
(2800-6500 per PTZOptics' published color docs), the Manual-mode gain
range (0-15), and how shutter/iris are encoded (we store the
documented step labels). Changing any of these is a one-line edit in
FIELDS below — by design.
"""
from __future__ import annotations

import re
from collections import OrderedDict

SHUTTER_STEPS = ["1/30", "1/60", "1/90", "1/100", "1/125", "1/180",
                 "1/250", "1/350", "1/500", "1/725", "1/1000", "1/1500",
                 "1/2000", "1/3000", "1/4000", "1/6000", "1/10000"]
IRIS_STEPS = ["F1.8", "F2.0", "F2.4", "F2.8", "F3.4", "F4.0", "F4.8",
              "F5.6", "F6.8", "F8.0", "F9.6", "F11.0", "Close"]

# name = JSON field, key = conf key, kind: int | enum | bool
FIELDS = [
    # --- image basics (keys + live values from the real camera snapshot)
    dict(name="brightness", key="bright", kind="int", min=0, max=14),
    dict(name="saturation", key="saturation", kind="int", min=0, max=14),
    dict(name="contrast", key="contrast", kind="int", min=0, max=14),
    dict(name="sharpness", key="sharpness", kind="int", min=0, max=14),
    dict(name="hue", key="hue", kind="int", min=0, max=14),
    dict(name="flip", key="flip", kind="bool"),
    dict(name="mirror", key="mirror", kind="bool"),
    # --- white balance (G2 CGI sheet enums; G2 manual ranges)
    dict(name="wbMode", key="wbmode", kind="enum",
         values=["auto", "indoor", "outdoor", "onepush", "manual", "var"]),
    dict(name="redGain", key="rgain", kind="int", min=0, max=255),
    dict(name="blueGain", key="bgain", kind="int", min=0, max=255),
    dict(name="redTuning", key="rgtuning", kind="int", min=-10, max=10),
    dict(name="blueTuning", key="bgtuning", kind="int", min=-10, max=10),
    dict(name="awbSensitivity", key="awbsens", kind="enum",
         values=["low", "normal", "high"]),
    dict(name="colorTemp", key="colortemp", kind="int",
         min=2800, max=6500, step=100),
    # --- exposure (G2 CGI sheet aemode enum; G2 manual parameters)
    dict(name="expMode", key="aemode", kind="enum",
         values=["auto", "manual", "shutter", "iris", "bright"]),
    dict(name="shutter", key="shutter", kind="enum", values=SHUTTER_STEPS),
    dict(name="iris", key="iris", kind="enum", values=IRIS_STEPS),
    dict(name="gain", key="gain", kind="int", min=0, max=15),
    dict(name="brightLevel", key="expbright", kind="int", min=0, max=17),
    dict(name="expCompOn", key="expcompmode", kind="bool"),
    dict(name="expComp", key="expcomp", kind="int", min=-7, max=7),
    dict(name="backlight", key="backlight", kind="bool"),
    dict(name="gainLimit", key="gainlimit", kind="int", min=0, max=15),
    dict(name="antiFlicker", key="flicker", kind="enum",
         values=["off", "50hz", "60hz"]),
    dict(name="drc", key="drc", kind="int", min=0, max=8),
    # --- focus extras (G2 manual VISCA inquiries: AF sensitivity / zone)
    dict(name="afSensitivity", key="afsens", kind="enum",
         values=["low", "normal", "high"]),
    dict(name="afZone", key="afzone", kind="enum",
         values=["top", "center", "bottom"]),
    # --- noise reduction (G2 manual OSD NR menu)
    dict(name="nr2d", key="nr2d", kind="enum",
         values=["off", "auto", "1", "2", "3", "4", "5"]),
    dict(name="nr3d", key="nr3d", kind="enum",
         values=["off", "1", "2", "3", "4", "5", "6", "7", "8"]),
]

BY_NAME = {f["name"]: f for f in FIELDS}

_LINE = re.compile(r'^\s*([A-Za-z0-9_]+)\s*=\s*(?:"([^"]*)"|(\S+))\s*$')


def parse_conf(text: str) -> "OrderedDict[str, str]":
    """Parse a param.cgi conf body into an ordered raw-string dict.
    Raw value strings are preserved verbatim so a later post-back of an
    untouched key is byte-identical to what the camera sent."""
    out: "OrderedDict[str, str]" = OrderedDict()
    for line in text.splitlines():
        m = _LINE.match(line)
        if m:
            out[m.group(1)] = m.group(2) if m.group(2) is not None \
                else m.group(3)
    return out


def _to_typed(spec: dict, raw: str):
    if spec["kind"] == "int":
        return int(raw)
    if spec["kind"] == "bool":
        return raw.strip() not in ("0", "", "off", "Off", "OFF")
    return raw


def _to_raw(spec: dict, value) -> str:
    if spec["kind"] == "bool":
        return "1" if value else "0"
    return str(value)


def validate(spec: dict, value):
    """Raise ValueError if a typed value is outside the documented
    range/enum for its field."""
    if spec["kind"] == "int":
        if not isinstance(value, int) or isinstance(value, bool):
            raise ValueError(f"{spec['name']}: expected an integer")
        if value < spec["min"] or value > spec["max"]:
            raise ValueError(
                f"{spec['name']}: {value} outside "
                f"{spec['min']}..{spec['max']}")
    elif spec["kind"] == "enum":
        if value not in spec["values"]:
            raise ValueError(
                f"{spec['name']}: {value!r} not one of {spec['values']}")
    elif spec["kind"] == "bool":
        if not isinstance(value, bool):
            raise ValueError(f"{spec['name']}: expected true/false")


def to_typed(conf: "OrderedDict[str, str]") -> dict:
    """The modeled subset of a raw conf, as typed JSON values. Keys the
    camera didn't return are simply absent (never invented)."""
    out = {}
    for spec in FIELDS:
        raw = conf.get(spec["key"])
        if raw is None:
            continue
        try:
            out[spec["name"]] = _to_typed(spec, raw)
        except ValueError:
            out[spec["name"]] = raw
    return out


def apply_update(conf: "OrderedDict[str, str]",
                 updates: dict) -> "OrderedDict[str, str]":
    """Merge typed updates into a copy of the raw conf. Every other key
    — modeled or not — keeps its exact original string."""
    new = OrderedDict(conf)
    for name, value in updates.items():
        spec = BY_NAME.get(name)
        if spec is None:
            raise ValueError(f"unknown image field: {name}")
        validate(spec, value)
        new[spec["key"]] = _to_raw(spec, value)
    return new
