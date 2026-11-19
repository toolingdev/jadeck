"""Service configuration, all overridable by environment variables."""
from __future__ import annotations

import os
from dataclasses import dataclass


# The WING favorite slots (DASHBOARD.md): 8 mute toggles. The owner
# named the channels on his console and picked this display order
# on Nov 8, 2026 — slot order IS display order. The env overrides
# replace the whole list (PTZ_WING_CHANNELS / PTZ_WING_NAMES,
# 8 entries each); a blanked entry is an unassigned slot.
WING_SLOT_COUNT = 8
WING_DEFAULT_CHANNELS = (1, 2, 3, 7, 8, 10, 12, 6)
WING_DEFAULT_NAMES = ("MAIN", "WIRELESS GRAY", "WIFI MIC 2",
                      "VOX BK 1", "VOX BK 2", "ZGTAR", "MONTAGE",
                      "AUDIENCE")


def _parse_wing_channels(raw: str) -> tuple:
    """PTZ_WING_CHANNELS="1,2,,7,..." -> the console channel behind
    each of the dashboard's 8 favorite slots (None-padded to 8).
    Empty or 0 = not assigned (the slot shows as a placeholder)."""
    out: list = []
    for part in (raw or "").split(","):
        part = part.strip()
        try:
            n = int(part) if part else 0
        except ValueError:
            n = 0
        out.append(n if 1 <= n <= 48 else None)
    return tuple((out + [None] * WING_SLOT_COUNT)[:WING_SLOT_COUNT])


def _parse_wing_names(raw: str) -> tuple:
    """PTZ_WING_NAMES="MAIN|WIRELESS GRAY|..." -> slot labels.
    Anything missing falls back to "Fav N" (the mockup's labels)."""
    parts = [p.strip() for p in (raw or "").split("|")]
    return tuple((parts[i] if i < len(parts) and parts[i]
                  else f"Fav {i + 1}") for i in range(WING_SLOT_COUNT))


@dataclass(frozen=True)
class Config:
    camera_host: str = "10.1.10.110"
    camera_port: int = 5678            # VISCA over TCP
    camera_http: str = "http://10.1.10.110"
    camera_user: str = ""              # HTTP CGI auth (digest on 6.3.45+)
    camera_pass: str = ""
    bind_host: str = "0.0.0.0"
    bind_port: int = 8080
    db_path: str = ""                  # filled in __post_init__ style below
    macros_path: str = ""              # macro store DB (own file —
                                       # app/macros.py; never presets.db)
    mock: bool = False
    install_root: str = ""             # folder holding server/ and web/
    updates_dir: str = ""              # staged-update folder (sibling)
    # Dashboard bridges (DASHBOARD.md). Every bridge is optional: an
    # unconfigured one reports itself honestly in its status instead
    # of taking the app down.
    vmix_host: str = "127.0.0.1"       # vMix runs on the same PC
    vmix_port: int = 8088              # vMix Web Controller port
    midi_port: str = ""                # virtual MIDI port name match
    midi_channel: int = 1
    midi_note_prev: int = 60
    midi_note_next: int = 62           # the mockup's flashed default
    midi_note_black: int = 64
    midi_note_logo: int = 65
    midi_note_clear: int = 66
    wing_host: str = ""                # console IP; "" = not set up
    wing_port: int = 2223              # WING OSC (UDP)
    wing_channels: tuple = WING_DEFAULT_CHANNELS
    wing_names: tuple = WING_DEFAULT_NAMES

    @staticmethod
    def from_env() -> "Config":
        mock = os.environ.get("PTZ_MOCK", "") == "1"
        if mock:
            host = os.environ.get("PTZ_CAMERA_HOST", "127.0.0.1")
            port = int(os.environ.get("PTZ_CAMERA_PORT", "15678"))
            http = os.environ.get("PTZ_CAMERA_HTTP", "http://127.0.0.1:18081")
        else:
            host = os.environ.get("PTZ_CAMERA_HOST", "10.1.10.110")
            port = int(os.environ.get("PTZ_CAMERA_PORT", "5678"))
            http = os.environ.get("PTZ_CAMERA_HTTP", f"http://{host}")
        server_dir = os.path.dirname(os.path.dirname(
            os.path.abspath(__file__)))
        default_db = os.path.join(server_dir, "data", "presets.db")
        db_path = os.environ.get("PTZ_DB", default_db)
        macros_path = os.environ.get(
            "PTZ_MACROS_DB",
            os.path.join(os.path.dirname(db_path), "macros.db"))
        install_root = os.path.dirname(server_dir)
        updates_dir = os.environ.get("PTZ_UPDATES_DIR") or os.path.normpath(
            os.path.join(install_root, "..", "updates"))
        def _int(name: str, default: int) -> int:
            try:
                return int(os.environ.get(name, "") or default)
            except ValueError:
                return default

        # The WING favorites ship assigned — the owner's 8 picks — in
        # mock and real mode alike; PTZ_WING_CHANNELS/_NAMES, when
        # set at all, replace the whole list (a blank entry leaves
        # that slot unassigned).
        wing_channels = (_parse_wing_channels(
            os.environ["PTZ_WING_CHANNELS"])
            if "PTZ_WING_CHANNELS" in os.environ
            else WING_DEFAULT_CHANNELS)
        wing_names = (_parse_wing_names(os.environ["PTZ_WING_NAMES"])
                      if "PTZ_WING_NAMES" in os.environ
                      else WING_DEFAULT_NAMES)
        return Config(
            camera_host=host,
            camera_port=port,
            camera_http=http,
            camera_user=os.environ.get("PTZ_CAMERA_USER", ""),
            camera_pass=os.environ.get("PTZ_CAMERA_PASS", ""),
            bind_host=os.environ.get("PTZ_BIND", "0.0.0.0"),
            bind_port=int(os.environ.get("PTZ_PORT", "8080")),
            db_path=db_path,
            macros_path=macros_path,
            mock=mock,
            install_root=install_root,
            updates_dir=updates_dir,
            vmix_host=os.environ.get("PTZ_VMIX_HOST", "127.0.0.1"),
            vmix_port=_int("PTZ_VMIX_PORT", 8088),
            midi_port=os.environ.get("PTZ_MIDI_PORT", ""),
            midi_channel=min(16, max(1, _int("PTZ_MIDI_CHANNEL", 1))),
            midi_note_prev=_int("PTZ_MIDI_NOTE_PREV", 60),
            midi_note_next=_int("PTZ_MIDI_NOTE_NEXT", 62),
            midi_note_black=_int("PTZ_MIDI_NOTE_BLACK", 64),
            midi_note_logo=_int("PTZ_MIDI_NOTE_LOGO", 65),
            midi_note_clear=_int("PTZ_MIDI_NOTE_CLEAR", 66),
            wing_host=os.environ.get("PTZ_WING_HOST", ""),
            wing_port=_int("PTZ_WING_PORT", 2223),
            wing_channels=wing_channels,
            wing_names=wing_names,
        )
