"""Behringer WING bridge — the audio third of the dashboard.

The WING speaks OSC (Open Sound Control) over UDP on port 2223 and
replies to the caller's port (WING Remote Protocols, P.-G. Maillot):

  /?                      ->W  console info string comes back:
                               "WING,<ip>,PGM,<model>,<serial>,<fw>"
  /ch/<n>/mute            ->W  bare address (no args) READS the value;
                               the console replies with the same
                               address + the value
  /ch/<n>/mute ,i 1       ->W  SET: 1 = muted, 0 = live

Mute is the whole surface here on purpose — the owner asked for
mute/unmute on his 8 favorite channels, nothing more (DASHBOARD.md).
The favorite slots are app config (PTZ_WING_CHANNELS / _NAMES): the
console's channel numbers behind the 8 slots, shipped assigned to
The owner's picks (config.WING_DEFAULT_*). Unassigned slots are
reported as placeholders and refuse politely instead of guessing.

The OSC codec is hand-rolled stdlib (OSC 1.0: NUL-terminated,
4-byte-aligned address + typetag string + big-endian args) — the
app carries no dependency this small doesn't justify.

In mock mode a FakeWing stands in so the dashboard is exercisable
with no console anywhere near.
"""
from __future__ import annotations

import asyncio
import logging
import socket
import struct

from .config import WING_DEFAULT_CHANNELS

log = logging.getLogger("ptz.wing")


class WingError(Exception):
    pass


class WingNotAssigned(WingError):
    """The favorite slot has no console channel yet (→ HTTP 409)."""


# ------------------------------------------------------------- OSC codec

def _pad4(data: bytes) -> bytes:
    return data + b"\x00" * (-len(data) % 4)


def _osc_string(text: str) -> bytes:
    return _pad4(text.encode("ascii") + b"\x00")


def osc_encode(address: str, args: list | None = None) -> bytes:
    """args: plain values — int -> ,i ; float -> ,f ; str -> ,s."""
    args = args or []
    tags = ","
    body = b""
    for a in args:
        if isinstance(a, bool):            # bool is an int subclass;
            a = int(a)                     # mute is a plain 0/1 anyway
        if isinstance(a, int):
            tags += "i"
            body += struct.pack(">i", a)
        elif isinstance(a, float):
            tags += "f"
            body += struct.pack(">f", a)
        elif isinstance(a, str):
            tags += "s"
            body += _osc_string(a)
        else:
            raise WingError(f"cannot OSC-encode {type(a).__name__}")
    return _osc_string(address) + _osc_string(tags) + body


def osc_decode(data: bytes) -> tuple[str, list]:
    """One OSC message -> (address, [values]). Raises WingError on
    anything malformed — a garbled UDP reply must never be trusted."""
    def read_string(buf: bytes, off: int) -> tuple[str, int]:
        end = buf.find(b"\x00", off)
        if end < 0:
            raise WingError("unterminated OSC string")
        raw = buf[off:end]
        off += (len(raw) + 4) // 4 * 4
        return raw.decode("ascii", "replace"), off

    if not data or data[0:1] != b"/":
        raise WingError("not an OSC message")
    address, off = read_string(data, 0)
    if off >= len(data):
        return address, []
    tags, off = read_string(data, off)
    if not tags.startswith(","):
        raise WingError("OSC message without a typetag string")
    values = []
    for tag in tags[1:]:
        if tag == "i":
            values.append(struct.unpack(">i", data[off:off + 4])[0])
            off += 4
        elif tag == "f":
            values.append(struct.unpack(">f", data[off:off + 4])[0])
            off += 4
        elif tag == "s":
            s, off = read_string(data, off)
            values.append(s)
        else:
            raise WingError(f"unsupported OSC typetag {tag!r}")
    return address, values


# ---------------------------------------------------------------- client

class WingClient:
    """One UDP socket per exchange: the console replies to the
    caller's port, so a fresh ephemeral socket per query is both
    the simplest and the documented shape. Blocking; the service
    wraps calls in asyncio.to_thread."""

    def __init__(self, host: str, port: int = 2223,
                 timeout: float = 0.6, sock_factory=None):
        self.host = host
        self.port = port
        self.timeout = timeout
        # Test seam only: a factory returning a datagram-shaped
        # object (settimeout/sendto/recvfrom/close) for sandboxes
        # that block UDP outright. Production always uses real
        # sockets.
        self._sock_factory = sock_factory

    def _query(self, address: str, args: list | None = None,
               expect_reply: bool = True) -> tuple[str, list] | None:
        pkt = osc_encode(address, args)
        sock = self._sock_factory() if self._sock_factory \
            else socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        try:
            sock.settimeout(self.timeout)
            sock.sendto(pkt, (self.host, self.port))
            if not expect_reply:
                return None
            data, _ = sock.recvfrom(8192)
        except (socket.timeout, TimeoutError):
            raise WingError(
                f"WING at {self.host}:{self.port} did not answer")
        except OSError as e:
            raise WingError(f"WING unreachable: {e}")
        finally:
            sock.close()
        return osc_decode(data)

    def probe(self) -> str:
        """Console identity string ("WING,192.168...,PGM,...")."""
        addr, values = self._query("/?")
        if addr != "/?" or not values or not isinstance(values[0], str):
            raise WingError("unexpected reply to the WING probe")
        return values[0]

    def get_mute(self, channel: int) -> bool:
        addr, values = self._query(f"/ch/{channel}/mute")
        if addr != f"/ch/{channel}/mute" or not values:
            raise WingError(
                f"unexpected mute reply for channel {channel}")
        # Float parameters on the WING answer with value triplets;
        # mute is an int and answers plainly — either way the value
        # itself is first.
        return int(values[0]) != 0

    def set_mute(self, channel: int, muted: bool) -> None:
        # A set is fire-and-forget in OSC; correctness is proven by
        # the read-back the service does after every write.
        self._query(f"/ch/{channel}/mute", [1 if muted else 0],
                    expect_reply=False)

    def toggle_mute(self, channel: int) -> bool:
        new = not self.get_mute(channel)
        self.set_mute(channel, new)
        return new


class FakeWing:
    """Mock-mode console: honest state for the console's channels
    (the owner's 8 favorites pre-seeded; the desk has 48 channels and
    the mock knows them all, so macros can address any of them),
    a probe string in the real one's shape, and a call log for
    the battery."""

    def __init__(self, channels=None):
        if channels is None:
            channels = WING_DEFAULT_CHANNELS
        self._muted = {ch: False for ch in channels}
        self.calls: list = []

    def probe(self) -> str:
        return "WING,127.0.0.1,PGM,ngc-full,MOCK0001,1.07.2-mock"

    def _known(self, channel: int) -> bool:
        if not 1 <= channel <= 48:
            raise WingError(f"mock console has no channel {channel}")
        return self._muted.setdefault(channel, False)

    def get_mute(self, channel: int) -> bool:
        return self._known(channel)

    def set_mute(self, channel: int, muted: bool) -> None:
        self._known(channel)
        self.calls.append(("set_mute", channel, bool(muted)))
        self._muted[channel] = bool(muted)

    def toggle_mute(self, channel: int) -> bool:
        new = not self.get_mute(channel)
        self.set_mute(channel, new)
        return new


class WingService:
    """Async facade for main.py: slots (app config) over a client.

    Status never raises. A write is always followed by a read-back
    from the console, so what the dashboard shows is what the desk
    actually holds — never just what we hoped we sent."""

    def __init__(self, cfg):
        self.mock = cfg.mock
        self.host = cfg.wing_host
        self.slots = [{"slot": i + 1,
                       "label": (cfg.wing_names[i]
                                 if i < len(cfg.wing_names)
                                 else f"Fav {i + 1}"),
                       "channel": channel}
                      for i, channel in enumerate(cfg.wing_channels)]
        self._impl = (FakeWing([s["channel"] for s in self.slots
                                if s["channel"] is not None])
                      if cfg.mock else (
                          WingClient(cfg.wing_host, cfg.wing_port)
                          if cfg.wing_host else None))

    @property
    def configured(self) -> bool:
        return self._impl is not None

    def _slot(self, slot: int) -> dict:
        for s in self.slots:
            if s["slot"] == slot:
                return s
        raise WingNotAssigned(f"no favorite slot {slot}")

    async def status(self) -> dict:
        base = {"configured": self.configured,
                "simulated": self.mock, "console": None}
        if not self.configured:
            base["connected"] = False
            base["error"] = "console address not set up yet"
            base["channels"] = [{**s, "muted": None}
                                for s in self.slots]
            return base
        try:
            console = await asyncio.to_thread(self._impl.probe)
        except WingError as e:
            base.update(connected=False, error=str(e),
                        channels=[{**s, "muted": None}
                                  for s in self.slots])
            return base
        channels = []
        for s in self.slots:
            muted = None
            if s["channel"] is not None:
                try:
                    muted = await asyncio.to_thread(
                        self._impl.get_mute, s["channel"])
                except WingError:
                    muted = None
            channels.append({**s, "muted": muted})
        base.update(connected=True, console=console,
                    channels=channels)
        return base

    async def set_mute(self, slot: int, muted: bool | None) -> dict:
        """muted=None toggles. Returns the one channel, read back."""
        s = self._slot(slot)
        if s["channel"] is None:
            raise WingNotAssigned(
                f"{s['label']} has no console channel yet — "
                "it is a placeholder until one is assigned")
        if not self.configured:
            raise WingError("console address not set up yet")
        if muted is None:
            new = await asyncio.to_thread(
                self._impl.toggle_mute, s["channel"])
        else:
            await asyncio.to_thread(
                self._impl.set_mute, s["channel"], bool(muted))
            new = await asyncio.to_thread(
                self._impl.get_mute, s["channel"])
        return {**s, "muted": new}

    async def set_mute_by_channel(self, channel: int,
                                  muted: bool) -> dict:
        """The macro engine's door (app/macros.py): set a mute by
        console channel number — any channel, not just a favorite
        slot — and REQUIRE the read-back to match. A mismatch is a
        WingError, never a shrug: macro steps move live audio, and
        "we sent it" is not "it happened"."""
        if not 1 <= channel <= 48:
            raise WingError(f"channel {channel} is outside 1..48")
        if not self.configured:
            raise WingError("console address not set up yet")
        await asyncio.to_thread(
            self._impl.set_mute, channel, bool(muted))
        back = await asyncio.to_thread(self._impl.get_mute, channel)
        if back != bool(muted):
            raise WingError(
                f"channel {channel} read back "
                f"{'muted' if back else 'live'} after being set "
                f"{'muted' if muted else 'live'} — the console did "
                "not take the change")
        return {"channel": channel, "muted": back}
