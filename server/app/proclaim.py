"""Proclaim bridge — the slides third of the dashboard.

Proclaim has no HTTP API (verified Nov 7, 2026 — even the Companion
ecosystem has no Proclaim module for exactly this reason). Its one
control door besides its own app is **MIDI in**: Proclaim's
Settings → MIDI maps incoming notes to slide actions. So the
dashboard service plays the part of a tiny MIDI controller on the
PC: a button press here sends one note on a virtual MIDI port
(loopMIDI or similar, named at deploy time — PTZ_MIDI_PORT matches
by substring), and Proclaim follows the note it was taught.

Default note map (ours — Proclaim must be taught these exact notes;
see DASHBOARD.md for the two-minute setup):

    Prev slide   Ch 1 · Note 60
    Next slide   Ch 1 · Note 62     (the approved mockup's flash)
    Black        Ch 1 · Note 64
    Logo         Ch 1 · Note 65
    Clear text   Ch 1 · Note 66

Every note is overridable by env (PTZ_MIDI_NOTE_* / _CHANNEL).

The sender sits behind the MidiOut interface:
  * WinmmMidiOut     — real Windows MIDI out via ctypes/winmm,
                       the production path on the office PC;
  * RecordingMidiOut — records what would have been sent. It is
                       the mock-mode backend AND the honest
                       fallback anywhere MIDI can't open, so the
                       dashboard still answers truthfully
                       (status says the port is the recorder).
"""
from __future__ import annotations

import asyncio
import logging
import sys
import time

log = logging.getLogger("ptz.proclaim")

NOTE_VELOCITY = 100
NOTE_HOLD_S = 0.12          # a tap, not a drone: on, hold, off


def pack_short_msg(status: int, data1: int, data2: int) -> int:
    """winmm midiOutShortMsg packs a short message into a DWORD:
    status in the low byte, data1 << 8, data2 << 16."""
    return (status & 0xFF) | ((data1 & 0x7F) << 8) \
        | ((data2 & 0x7F) << 16)


def note_dwords(note: int, channel: int = 1,
                velocity: int = NOTE_VELOCITY) -> tuple[int, int]:
    """(note-on DWORD, note-off DWORD) for a 1-based MIDI channel."""
    ch = (channel - 1) & 0x0F
    return (pack_short_msg(0x90 | ch, note, velocity),
            pack_short_msg(0x80 | ch, note, 0))


class ProclaimError(Exception):
    pass


# ------------------------------------------------------------ MIDI outs

class MidiOut:
    """Interface: send one tapped note."""
    name = "none"
    real = False

    def send_note(self, note: int, channel: int = 1) -> None:
        raise NotImplementedError

    def close(self) -> None:
        pass


class RecordingMidiOut(MidiOut):
    """Mock + fallback: remembers every note, sends nothing."""

    def __init__(self, name: str):
        self.name = name
        self.sent: list[dict] = []

    def send_note(self, note: int, channel: int = 1) -> None:
        self.sent.append({"note": note, "channel": channel,
                          "at": time.time()})


class WinmmMidiOut(MidiOut):
    """Real MIDI out on Windows via winmm (ctypes, stdlib only).

    Enumerates output devices, picks the one whose name contains
    the configured substring (case-insensitive) — or the first
    device when no name is configured — and opens it."""

    real = True

    def __init__(self, port_name: str = ""):
        import ctypes
        from ctypes import wintypes

        class MIDIOUTCAPSW(ctypes.Structure):
            _fields_ = [("wMid", wintypes.WORD),
                        ("wPid", wintypes.WORD),
                        ("vDriverVersion", wintypes.DWORD),
                        ("szPname", wintypes.WCHAR * 32),
                        ("wTechnology", wintypes.WORD),
                        ("wVoices", wintypes.WORD),
                        ("wNotes", wintypes.WORD),
                        ("wChannelMask", wintypes.WORD),
                        ("dwSupport", wintypes.DWORD)]

        winmm = ctypes.WinDLL("winmm")
        count = winmm.midiOutGetNumDevs()
        if count == 0:
            raise ProclaimError("this PC has no MIDI output devices "
                                "(is the virtual port installed?)")
        chosen, chosen_name = None, ""
        caps = MIDIOUTCAPSW()
        for i in range(count):
            if winmm.midiOutGetDevCapsW(
                    i, ctypes.byref(caps), ctypes.sizeof(caps)) != 0:
                continue
            if not port_name or port_name.lower() in \
                    caps.szPname.lower():
                chosen, chosen_name = i, caps.szPname
                break
        if chosen is None:
            raise ProclaimError(
                f"no MIDI output matching {port_name!r} on this PC")
        handle = wintypes.HANDLE()
        if winmm.midiOutOpen(ctypes.byref(handle), chosen, 0, 0,
                             0) != 0:
            raise ProclaimError(
                f"could not open MIDI output {chosen_name!r}")
        self._winmm = winmm
        self._handle = handle
        self.name = chosen_name

    def send_note(self, note: int, channel: int = 1) -> None:
        on, off = note_dwords(note, channel)
        self._winmm.midiOutShortMsg(self._handle, on)
        time.sleep(NOTE_HOLD_S)
        self._winmm.midiOutShortMsg(self._handle, off)

    def close(self) -> None:
        try:
            self._winmm.midiOutClose(self._handle)
        except Exception:
            pass


def open_midi_out(cfg) -> MidiOut:
    """Platform/config factory. Never raises: the worst case is the
    recorder, and the status endpoint says so plainly."""
    if cfg.mock:
        return RecordingMidiOut("Mock MIDI — recorded, not sent")
    if sys.platform == "win32":
        try:
            return WinmmMidiOut(cfg.midi_port)
        except Exception as e:
            log.warning("MIDI out unavailable (%s); recording only", e)
            return RecordingMidiOut(f"Unavailable — {e}")
    return RecordingMidiOut("Recorded only (MIDI out is Windows-PC "
                            "only)")


# -------------------------------------------------------------- service

ACTIONS = ("prev", "next", "black", "logo", "clear")


class ProclaimService:
    """Async facade for main.py: one note per action, remembered."""

    def __init__(self, cfg):
        self.mock = cfg.mock
        self.channel = cfg.midi_channel
        self.notes = {"prev": cfg.midi_note_prev,
                      "next": cfg.midi_note_next,
                      "black": cfg.midi_note_black,
                      "logo": cfg.midi_note_logo,
                      "clear": cfg.midi_note_clear}
        self.out = open_midi_out(cfg)
        self.last_sent: dict | None = None

    def status(self) -> dict:
        return {"available": self.out.real,
                "simulated": self.mock,
                "port": self.out.name,
                "channel": self.channel,
                "notes": dict(self.notes),
                "lastSent": self.last_sent}

    async def send(self, action: str) -> dict:
        if action not in ACTIONS:
            raise ProclaimError(f"unknown slide action {action!r}")
        note = self.notes[action]
        # winmm's hold sleeps ~120 ms; keep it off the event loop.
        await asyncio.to_thread(self.out.send_note, note,
                                self.channel)
        self.last_sent = {"action": action, "note": note,
                          "channel": self.channel,
                          "port": self.out.name}
        return {"ok": True, **self.last_sent}
