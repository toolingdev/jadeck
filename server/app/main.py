"""PTZOptics web controller — FastAPI service.

One service owns the camera connection; every browser (PC, tablet, phone)
is a thin client over REST + one WebSocket. Serves the web/ UI itself.
"""
from __future__ import annotations

import asyncio
import json
import logging
import os
import time
from contextlib import asynccontextmanager

from fastapi import FastAPI, HTTPException, Request, WebSocket, \
    WebSocketDisconnect
from fastapi.responses import FileResponse, JSONResponse, Response
from fastapi.staticfiles import StaticFiles
from pydantic import BaseModel, Field

from typing import Literal

from . import imageconf, macros, proclaim, updates, vmix, wing
from .camera import CameraService, DriveVector, WbBusyError, WbPickError
from .config import Config, WING_SLOT_COUNT
from .store import PresetStore

logging.basicConfig(level=logging.INFO,
                    format="%(asctime)s %(name)s %(levelname)s %(message)s")
log = logging.getLogger("ptz.main")

CFG = Config.from_env()
WEB_DIR = os.path.join(os.path.dirname(os.path.dirname(
    os.path.dirname(os.path.abspath(__file__)))), "web")

camera = CameraService(CFG)
store = PresetStore(CFG.db_path)
# Dashboard bridges (DASHBOARD.md): vMix over its HTTP API, Proclaim
# over MIDI out, the WING over OSC. Each is optional and reports
# itself honestly when its hardware isn't there.
vmix_svc = vmix.VmixService(CFG)
proclaim_svc = proclaim.ProclaimService(CFG)
wing_svc = wing.WingService(CFG)
# Dashboard macros (app/macros.py, contract in MACROS.md): chained
# WING actions in their own store beside — never inside — the
# preset DB. The engine runs them against the same WING service
# the strip uses, with read-back verification per step.
MACRO_FAVORITES = macros.favorites_from_config(CFG)
macro_store = macros.MacroStore(CFG.macros_path, MACRO_FAVORITES)
macro_engine = macros.MacroEngine(macro_store, wing_svc,
                                  MACRO_FAVORITES)
ws_clients: set[WebSocket] = set()


async def _broadcast(status: dict):
    msg = json.dumps({"t": "status", **status})
    dead = []
    for ws in list(ws_clients):
        try:
            await ws.send_text(msg)
        except Exception:
            dead.append(ws)
    for ws in dead:
        ws_clients.discard(ws)


camera.on_change = _broadcast


@asynccontextmanager
async def lifespan(app: FastAPI):
    log.info("starting (mock=%s) camera=%s:%s db=%s",
             CFG.mock, CFG.camera_host, CFG.camera_port, CFG.db_path)
    await camera.start()
    await camera.refresh_static()
    yield
    await camera.stop()


app = FastAPI(title="PTZOptics Web Controller", lifespan=lifespan)


# ------------------------------------------------------------------- API

@app.get("/api/config")
async def api_config(request: Request):
    """Stream gateway endpoints, derived from the host the client used."""
    host = request.url.hostname or "localhost"
    return {
        "whep": f"http://{host}:8889/cam/whep",
        "hls": f"http://{host}:8888/cam/index.m3u8",
        "whepSd": f"http://{host}:8889/cam_sd/whep",
        "hlsSd": f"http://{host}:8888/cam_sd/index.m3u8",
        "snapshot": "/api/snapshot",
        # Dashboard panes (DASHBOARD.md): the gateway's program
        # (vMix) and confidence (Proclaim) paths. Whether anything
        # is actually flowing on them is the pane's no-signal state
        # to show — see the NDI note in stream/mediamtx.yml.
        "programWhep": f"http://{host}:8889/program/whep",
        "programHls": f"http://{host}:8888/program/index.m3u8",
        "confidenceWhep": f"http://{host}:8889/confidence/whep",
        "confidenceHls": f"http://{host}:8888/confidence/index.m3u8",
    }


@app.get("/api/status")
async def api_status():
    await camera.refresh_static()
    return camera.snapshot_status()


@app.get("/api/snapshot")
async def api_snapshot():
    data = await camera.fetch_snapshot()
    if data is None:
        raise HTTPException(502, "snapshot unavailable")
    return Response(content=data, media_type="image/jpeg",
                    headers={"Cache-Control": "no-store"})


@app.get("/api/presets")
async def list_presets():
    return store.list()


class PresetCreate(BaseModel):
    name: str = Field(min_length=1, max_length=60)
    slot: int | None = Field(default=None, ge=1, le=127)


class PresetPatch(BaseModel):
    name: str | None = Field(default=None, min_length=1, max_length=60)
    exposure: dict | None = None
    setExposure: bool = False


class Reorder(BaseModel):
    ids: list[int]


@app.post("/api/presets", status_code=201)
async def create_preset(body: PresetCreate):
    try:
        return store.create(body.name.strip(), body.slot)
    except Exception as e:
        raise HTTPException(400, str(e))


@app.patch("/api/presets/{pid}")
async def patch_preset(pid: int, body: PresetPatch):
    p = store.update(pid, name=body.name.strip() if body.name else None,
                     exposure=body.exposure, set_exposure=body.setExposure)
    if p is None:
        raise HTTPException(404, "preset not found")
    return p


@app.delete("/api/presets/{pid}")
async def delete_preset(pid: int):
    if not store.delete(pid):
        raise HTTPException(404, "preset not found")
    return {"ok": True}


@app.post("/api/presets/reorder")
async def reorder_presets(body: Reorder):
    return store.reorder(body.ids)


def _reject_if_busy():
    """While a sequenced operation (pick-a-white-point) owns the
    camera, discrete motion commands are refused with 409 rather than
    queued — the sequence must never be interleaved."""
    if camera.operation_active:
        raise HTTPException(
            409, "camera is busy (white-balance pick in progress)")


@app.post("/api/presets/{pid}/recall")
async def recall_preset(pid: int):
    p = store.get(pid)
    if p is None:
        raise HTTPException(404, "preset not found")
    if not camera.status.connected:
        raise HTTPException(503, "camera not connected")
    _reject_if_busy()
    await camera.recall_preset(p["slot"])
    return {"ok": True, "slot": p["slot"]}


@app.post("/api/presets/{pid}/store")
async def store_preset(pid: int):
    p = store.get(pid)
    if p is None:
        raise HTTPException(404, "preset not found")
    if not camera.status.connected:
        raise HTTPException(503, "camera not connected")
    _reject_if_busy()
    await camera.store_preset(p["slot"])
    return {"ok": True, "slot": p["slot"]}


@app.post("/api/ptz/home")
async def ptz_home():
    if not camera.status.connected:
        raise HTTPException(503, "camera not connected")
    _reject_if_busy()
    await camera.home()
    return {"ok": True}


@app.post("/api/ptz/stop")
async def ptz_stop():
    await camera.force_stop()
    return {"ok": True}


class FocusBody(BaseModel):
    action: str  # auto | manual | onepush | near | far | stop


@app.post("/api/focus")
async def focus(body: FocusBody):
    if body.action not in ("auto", "manual", "onepush", "near", "far", "stop"):
        raise HTTPException(400, "unknown focus action")
    if not camera.status.connected:
        raise HTTPException(503, "camera not connected")
    _reject_if_busy()
    await camera.focus(body.action)
    return {"ok": True}


# --------------------------------------------- pick a white point (WB)
# The working white-balance path on this camera: the user taps a point
# that should be pure white; the service aims it at the frame center,
# fires One Push WB over VISCA, and returns to the exact prior position
# (see CameraService.wb_pick). Takes ~5 s; the camera moves.

class WbPickBody(BaseModel):
    x: float = Field(ge=0.0, le=1.0)
    y: float = Field(ge=0.0, le=1.0)


@app.post("/api/camera/wb-pick")
async def camera_wb_pick(body: WbPickBody):
    if not camera.status.connected:
        raise HTTPException(503, "camera not connected")
    try:
        return await camera.wb_pick(body.x, body.y)
    except WbBusyError as e:
        raise HTTPException(409, str(e))
    except WbPickError as e:
        raise HTTPException(502, str(e))


# ---------------------------------------------------------- updates
# Staged, hash-verified updates (design: UPDATE.md). Packages are
# copied into the updates folder beside the install; detection here
# verifies EVERY file's SHA-256 against the package manifest before
# anything is offered, and the apply itself runs in a detached helper
# (server/updater.py) with backup + automatic rollback. The app never
# reaches out to any server at update time.

@app.get("/api/update/status")
async def api_update_status():
    return updates.update_status(CFG.install_root, CFG.updates_dir)


@app.post("/api/update/apply")
async def api_update_apply():
    if camera.status.driving:
        raise HTTPException(
            409, "the camera is being driven right now — update once "
                 "it stops")
    if camera.operation_active:
        raise HTTPException(
            409, "a camera operation is in progress — try again in a "
                 "few seconds")
    manifest = None
    st = updates.update_status(CFG.install_root, CFG.updates_dir)
    if st["state"] != "available":
        raise HTTPException(409, "no verified update is staged")
    try:
        manifest = updates.launch_apply(
            CFG.install_root, CFG.updates_dir,
            f"http://127.0.0.1:{CFG.bind_port}/api/status")
    except updates.UpdateError as e:
        raise HTTPException(409, str(e))
    return {"ok": True, "state": "applying",
            "version": manifest["version"]}


# ------------------------------------------------------- image config
# Advanced settings. Field map, ranges and sources: app/imageconf.py.
# Writes are read-modify-write against the camera's own conf, so fields
# the user didn't touch go back byte-identical.

class ImagePatch(BaseModel):
    brightness: int | None = Field(default=None, ge=0, le=14)
    saturation: int | None = Field(default=None, ge=0, le=14)
    contrast: int | None = Field(default=None, ge=0, le=14)
    sharpness: int | None = Field(default=None, ge=0, le=14)
    hue: int | None = Field(default=None, ge=0, le=14)
    flip: bool | None = None
    mirror: bool | None = None
    wbMode: Literal["auto", "indoor", "outdoor", "onepush", "manual",
                    "var"] | None = None
    redGain: int | None = Field(default=None, ge=0, le=255)
    blueGain: int | None = Field(default=None, ge=0, le=255)
    redTuning: int | None = Field(default=None, ge=-10, le=10)
    blueTuning: int | None = Field(default=None, ge=-10, le=10)
    awbSensitivity: Literal["low", "normal", "high"] | None = None
    colorTemp: int | None = Field(default=None, ge=2800, le=6500)
    expMode: Literal["auto", "manual", "shutter", "iris",
                     "bright"] | None = None
    shutter: str | None = None   # step label; validated in imageconf
    iris: str | None = None      # step label; validated in imageconf
    gain: int | None = Field(default=None, ge=0, le=15)
    brightLevel: int | None = Field(default=None, ge=0, le=17)
    expCompOn: bool | None = None
    expComp: int | None = Field(default=None, ge=-7, le=7)
    backlight: bool | None = None
    gainLimit: int | None = Field(default=None, ge=0, le=15)
    antiFlicker: Literal["off", "50hz", "60hz"] | None = None
    drc: int | None = Field(default=None, ge=0, le=8)
    afSensitivity: Literal["low", "normal", "high"] | None = None
    afZone: Literal["top", "center", "bottom"] | None = None
    nr2d: Literal["off", "auto", "1", "2", "3", "4", "5"] | None = None
    nr3d: Literal["off", "1", "2", "3", "4", "5", "6", "7",
                  "8"] | None = None


@app.get("/api/camera/image")
async def get_camera_image():
    conf = await camera.fetch_image_conf()
    if conf is None:
        raise HTTPException(502, "camera image config unavailable")
    return imageconf.to_typed(conf)


@app.post("/api/camera/image")
async def post_camera_image(body: ImagePatch):
    updates = {k: v for k, v in body.model_dump().items()
               if v is not None}
    if not updates:
        raise HTTPException(400, "no image fields supplied")
    conf = await camera.fetch_image_conf()
    if conf is None:
        raise HTTPException(502, "camera image config unavailable")
    try:
        new_conf = imageconf.apply_update(conf, updates)
    except ValueError as e:
        raise HTTPException(400, str(e))
    if not await camera.post_image_conf(new_conf):
        raise HTTPException(502, "camera rejected the image config")
    back = await camera.fetch_image_conf()
    return imageconf.to_typed(back if back is not None else new_conf)


@app.post("/api/camera/image/wb-trigger")
async def camera_wb_trigger():
    if not await camera.wb_trigger():
        raise HTTPException(502, "white-balance trigger failed")
    return {"ok": True}


# ------------------------------------------------------ dashboard
# The unifying dashboard's three bridges (DASHBOARD.md). Statuses
# never raise — a bridge whose hardware is off or unplugged reports
# connected=false and the pane shows its state in plain words.
# Actions raise the bridge's error, mapped: unassigned WING slot
# 409 (a setup fact, not a failure), unreachable hardware 502.

@app.get("/api/vmix/status")
async def vmix_status():
    return await vmix_svc.status()


class VmixPreviewBody(BaseModel):
    input: int = Field(ge=1, le=999)


class VmixFadeBody(BaseModel):
    durationMs: int = Field(default=1000, ge=0, le=10000)


@app.post("/api/vmix/preview")
async def vmix_preview(body: VmixPreviewBody):
    try:
        return await vmix_svc.preview(body.input)
    except vmix.VmixError as e:
        raise HTTPException(502, str(e))


@app.post("/api/vmix/cut")
async def vmix_cut():
    try:
        return await vmix_svc.cut()
    except vmix.VmixError as e:
        raise HTTPException(502, str(e))


@app.post("/api/vmix/fade")
async def vmix_fade(body: VmixFadeBody = VmixFadeBody()):
    try:
        return await vmix_svc.fade(body.durationMs)
    except vmix.VmixError as e:
        raise HTTPException(502, str(e))


@app.get("/api/proclaim/status")
async def proclaim_status():
    return proclaim_svc.status()


@app.post("/api/proclaim/{action}")
async def proclaim_action(action: str):
    try:
        return await proclaim_svc.send(action)
    except proclaim.ProclaimError as e:
        raise HTTPException(400, str(e))


@app.get("/api/wing/status")
async def wing_status():
    return await wing_svc.status()


class WingMuteBody(BaseModel):
    slot: int = Field(ge=1, le=WING_SLOT_COUNT)
    muted: bool | None = None      # omitted = toggle


@app.post("/api/wing/mute")
async def wing_mute(body: WingMuteBody):
    try:
        return await wing_svc.set_mute(body.slot, body.muted)
    except wing.WingNotAssigned as e:
        raise HTTPException(409, str(e))
    except wing.WingError as e:
        raise HTTPException(502, str(e))


# ------------------------------------------------------ macros
# Chained WING actions (app/macros.py; the authoring contract is
# MACROS.md). CRUD + import validate through the one validator, so
# the builder UI, the import endpoint and any AI authoring from
# the doc all meet exactly the same rules. A run that stops on a
# failed step is a 200 with the report's ok=false — the run
# happened; HTTP errors are for unknown macros (404) and a second
# run while one is in flight (409).

class MacroBody(BaseModel):
    name: str
    description: str = ""
    steps: list[dict]


class MacroPatch(BaseModel):
    name: str | None = None
    description: str | None = None
    steps: list[dict] | None = None


def _validate_macro_doc(doc: dict) -> dict:
    try:
        return macros.validate_macro(doc, MACRO_FAVORITES)
    except macros.MacroValidationError as e:
        raise HTTPException(400, str(e))


@app.get("/api/macros")
async def list_macros():
    return macro_store.list()


@app.get("/api/macros/engine")
async def macro_engine_status():
    return macro_engine.status()


@app.post("/api/macros/import")
async def import_macros(request: Request, validateOnly: bool = False):
    """Programmatic authoring (MACROS.md): one macro object, a
    list, or {"macros": [...]}. All-or-nothing — every macro is
    validated (and name-checked against the store and its
    siblings) before any is written, and failures come back as a
    per-macro errors list. validateOnly (body flag or query param)
    runs the same checks and writes nothing."""
    try:
        payload = await request.json()
    except Exception:
        raise HTTPException(400, "the import body is not valid JSON")
    try:
        items, dry = macros.extract_import(payload)
    except macros.MacroValidationError as e:
        return JSONResponse(status_code=400,
                            content={"detail": str(e), "errors": []})
    dry = dry or validateOnly
    normalized, errors = [], []
    seen: dict[str, int] = {}
    for i, item in enumerate(items):
        label = item.get("name") if isinstance(item, dict) else None
        try:
            m = macros.validate_macro(item, MACRO_FAVORITES)
        except macros.MacroValidationError as e:
            errors.append({"index": i, "name": label, "error": str(e)})
            continue
        key = m["name"].lower()
        if key in seen:
            errors.append({
                "index": i, "name": m["name"],
                "error": f"duplicate name within the import "
                         f"(first at index {seen[key]})"})
            continue
        seen[key] = i
        if macro_store.by_name(m["name"]) is not None:
            errors.append({
                "index": i, "name": m["name"],
                "error": f"a macro named {m['name']!r} already exists"})
            continue
        normalized.append(m)
    if errors:
        return JSONResponse(status_code=400, content={
            "detail": f"{len(errors)} of {len(items)} macro(s) "
                      "failed validation; nothing was imported",
            "errors": errors})
    if dry:
        return {"valid": True, "validateOnly": True,
                "macros": normalized}
    created = [macro_store.create(m) for m in normalized]
    return JSONResponse(status_code=201, content={"imported": created})


@app.post("/api/macros", status_code=201)
async def create_macro(body: MacroBody):
    doc = _validate_macro_doc(body.model_dump())
    try:
        return macro_store.create(doc)
    except macros.MacroConflictError as e:
        raise HTTPException(409, str(e))


@app.get("/api/macros/{mid}")
async def get_macro(mid: int):
    m = macro_store.get(mid)
    if m is None:
        raise HTTPException(404, "macro not found")
    return m


@app.patch("/api/macros/{mid}")
async def patch_macro(mid: int, body: MacroPatch):
    m = macro_store.get(mid)
    if m is None:
        raise HTTPException(404, "macro not found")
    doc = _validate_macro_doc({
        "name": body.name if body.name is not None else m["name"],
        "description": (body.description
                        if body.description is not None
                        else m["description"]),
        "steps": body.steps if body.steps is not None else m["steps"],
    })
    try:
        return macro_store.update(mid, doc)
    except macros.MacroConflictError as e:
        raise HTTPException(409, str(e))


@app.delete("/api/macros/{mid}")
async def delete_macro(mid: int):
    if not macro_store.delete(mid):
        raise HTTPException(404, "macro not found")
    return {"ok": True}


@app.post("/api/macros/{mid}/run")
async def run_macro(mid: int):
    try:
        return await macro_engine.run(mid)
    except macros.MacroNotFoundError as e:
        raise HTTPException(404, str(e))
    except macros.MacroConflictError as e:
        raise HTTPException(409, str(e))


# ------------------------------------------------------------- WebSocket

@app.websocket("/ws/control")
async def ws_control(ws: WebSocket):
    await ws.accept()
    ws_clients.add(ws)
    owner = f"{id(ws):x}"
    try:
        await ws.send_text(json.dumps(
            {"t": "status", **camera.snapshot_status()}))
        while True:
            raw = await ws.receive_text()
            try:
                msg = json.loads(raw)
            except ValueError:
                continue
            if msg.get("t") == "drive":
                camera.set_drive(DriveVector(
                    pan=max(-1, min(1, int(msg.get("pan", 0)))),
                    tilt=max(-1, min(1, int(msg.get("tilt", 0)))),
                    zoom=max(-1, min(1, int(msg.get("zoom", 0)))),
                    speed=max(5, min(100, int(msg.get("speed", 40)))),
                ), owner=owner)
            elif msg.get("t") == "stop":
                camera.set_drive(DriveVector(), owner=owner)
    except WebSocketDisconnect:
        pass
    finally:
        ws_clients.discard(ws)
        # If the departing client was driving, stop the camera now rather
        # than waiting for the dead-man's switch.
        if camera._drive_owner == owner and camera._desired.active():
            camera.set_drive(DriveVector(), owner=owner)


# ------------------------------------------------------------------- web

@app.get("/")
async def index():
    return FileResponse(os.path.join(WEB_DIR, "index.html"))


@app.get("/dashboard")
async def dashboard():
    return FileResponse(os.path.join(WEB_DIR, "dashboard.html"))


app.mount("/static", StaticFiles(directory=WEB_DIR), name="static")
