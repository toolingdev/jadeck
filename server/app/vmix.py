"""vMix bridge — the switcher third of the dashboard (DASHBOARD.md).

vMix's Web Controller is an HTTP API on the same PC (default
127.0.0.1:8088, Settings → Web Controller in vMix must be enabled):

  GET /api/                         state as XML: inputs, which input
                                    is in preview, which is live
                                    (program), streaming/recording
  GET /api/?Function=PreviewInput&Input=N
  GET /api/?Function=Cut            take preview to program, instantly
  GET /api/?Function=Fade&Duration=1000
                                    take preview to program over 1 s

Inputs are addressed by number (1-based), exactly as the Web
Controller numbers them. Function calls answer 200 on success, 500
on error. Everything here is stdlib-only, like the rest of the app.

In mock mode a FakeVmix stands in, seeded with the four inputs from
the approved dashboard mockup, so the whole dashboard is exercisable
with no vMix anywhere near.
"""
from __future__ import annotations

import asyncio
import logging
import urllib.error
import urllib.parse
import urllib.request
import xml.etree.ElementTree as ET

log = logging.getLogger("ptz.vmix")


class VmixError(Exception):
    pass


def _as_bool(text: str | None) -> bool:
    return (text or "").strip().lower() == "true"


def parse_status_xml(data: bytes) -> dict:
    """vMix /api/ XML -> the dashboard's status shape. Pure: the
    battery feeds it canned XML. Raises VmixError on junk."""
    try:
        root = ET.fromstring(data)
    except ET.ParseError as e:
        raise VmixError(f"vMix sent back unreadable XML: {e}")
    if root.tag != "vmix":
        raise VmixError(f"unexpected reply from vMix (root <{root.tag}>)")

    def text(tag: str) -> str:
        el = root.find(tag)
        return (el.text or "").strip() if el is not None else ""

    def number(tag: str) -> int:
        try:
            return int(text(tag))
        except ValueError:
            return 0

    inputs = []
    holder = root.find("inputs")
    if holder is not None:
        for el in holder.findall("input"):
            try:
                n = int(el.get("number") or 0)
            except ValueError:
                n = 0
            inputs.append({
                "number": n,
                "key": el.get("key") or "",
                # title attribute wins; the element text is the
                # input's short title, same thing in practice.
                "title": el.get("title") or (el.text or "").strip(),
                "type": el.get("type") or "",
                "state": el.get("state") or "",
            })
    inputs.sort(key=lambda i: i["number"])
    return {
        "version": text("version"),
        "edition": text("edition"),
        "inputs": inputs,
        "preview": number("preview"),
        "active": number("active"),
        "streaming": _as_bool(text("streaming")),
        "recording": _as_bool(text("recording")),
        "fadeToBlack": _as_bool(text("fadeToBlack")),
    }


class VmixClient:
    """Blocking HTTP client (urllib). The service wraps calls in
    asyncio.to_thread so the event loop never waits on vMix."""

    def __init__(self, host: str, port: int, timeout: float = 2.5):
        self.base = f"http://{host}:{port}/api/"
        self.timeout = timeout

    def _get(self, params: dict | None) -> bytes:
        url = self.base
        if params:
            url += "?" + urllib.parse.urlencode(params)
        try:
            with urllib.request.urlopen(url, timeout=self.timeout) as r:
                return r.read()
        except urllib.error.HTTPError as e:
            raise VmixError(f"vMix answered HTTP {e.code}")
        except (urllib.error.URLError, OSError) as e:
            raise VmixError(f"vMix not reachable: {e}")

    def status(self) -> dict:
        return parse_status_xml(self._get(None))

    def call(self, function: str, **params) -> None:
        self._get({"Function": function, **params})

    def preview(self, number: int) -> None:
        self.call("PreviewInput", Input=number)

    def cut(self) -> None:
        self.call("Cut")

    def fade(self, duration_ms: int = 1000) -> None:
        self.call("Fade", Duration=int(duration_ms))


class FakeVmix:
    """Mock-mode vMix: the mockup's four inputs, an honest little
    state machine, and a call log the battery asserts against."""

    def __init__(self):
        self._inputs = [
            {"number": 1, "key": "fake-1", "type": "XAML",
             "title": "PTZ Wide — Sanctuary", "state": "Running"},
            {"number": 2, "key": "fake-2", "type": "XAML",
             "title": "PTZ Pulpit — Close", "state": "Running"},
            {"number": 3, "key": "fake-3", "type": "NDI",
             "title": "Proclaim Slides", "state": "Running"},
            {"number": 4, "key": "fake-4", "type": "Video",
             "title": "Media / Announcements", "state": "Paused"},
        ]
        self._preview = 2
        self._active = 1
        self.calls: list = []

    def status(self) -> dict:
        return {"version": "29.0.0.49 (mock)", "edition": "Max",
                "inputs": [dict(i) for i in self._inputs],
                "preview": self._preview, "active": self._active,
                "streaming": True, "recording": False,
                "fadeToBlack": False}

    def _known(self, number: int) -> None:
        if number not in {i["number"] for i in self._inputs}:
            raise VmixError(f"vMix has no input {number}")

    def preview(self, number: int) -> None:
        self._known(number)
        self.calls.append(("preview", number))
        self._preview = number

    def cut(self) -> None:
        self.calls.append(("cut",))
        self._active = self._preview

    def fade(self, duration_ms: int = 1000) -> None:
        self.calls.append(("fade", int(duration_ms)))
        self._active = self._preview


class VmixService:
    """Async facade for main.py. Status never raises: a vMix that
    is closed or restarting reads as connected=False with the
    reason, so the dashboard poll stays calm. Actions raise
    VmixError, which the endpoint maps to 502."""

    def __init__(self, cfg):
        self.mock = cfg.mock
        self._impl = FakeVmix() if cfg.mock else VmixClient(
            cfg.vmix_host, cfg.vmix_port)

    async def status(self) -> dict:
        try:
            st = await asyncio.to_thread(self._impl.status)
            return {"connected": True, "simulated": self.mock, **st}
        except VmixError as e:
            return {"connected": False, "simulated": self.mock,
                    "error": str(e), "inputs": [], "preview": 0,
                    "active": 0, "streaming": False,
                    "recording": False, "fadeToBlack": False,
                    "version": "", "edition": ""}

    async def preview(self, number: int) -> dict:
        await asyncio.to_thread(self._impl.preview, number)
        return await self.status()

    async def cut(self) -> dict:
        await asyncio.to_thread(self._impl.cut)
        return await self.status()

    async def fade(self, duration_ms: int = 1000) -> dict:
        await asyncio.to_thread(self._impl.fade, duration_ms)
        return await self.status()
