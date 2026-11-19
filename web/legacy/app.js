"use strict";
/* PTZOptics web controller client.
   - Control: one WebSocket. Drive input is a *state* (latest vector wins);
     the server coalesces and owns the camera, so this page can send on
     every pointer change without ever flooding the camera.
   - Video: WebRTC (WHEP) first for sub-second latency, native HLS second,
     snapshot refresh as the always-works fallback. */

const $ = (id) => document.getElementById(id);
const video = $("streamVideo"), snapImg = $("snapImg");

/* ------------------------------------------------------------ status/ws */

let ws = null, wsBackoff = 500;
function wsConnect() {
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  ws = new WebSocket(`${proto}//${location.host}/ws/control`);
  ws.onopen = () => { wsBackoff = 500; sendDrive(true); };
  ws.onmessage = (ev) => {
    let m; try { m = JSON.parse(ev.data); } catch { return; }
    if (m.t === "status") renderStatus(m);
  };
  ws.onclose = () => {
    setTimeout(wsConnect, wsBackoff);
    wsBackoff = Math.min(wsBackoff * 2, 8000);
  };
  ws.onerror = () => { try { ws.close(); } catch {} };
}

let camDevice = {};
function renderStatus(m) {
  $("connDot").classList.toggle("on", !!m.connected);
  if (m.device && Object.keys(m.device).length) {
    camDevice = m.device;
    $("camName").textContent = camDevice.devname || "PTZOptics Camera";
    const ver = camDevice.versioninfo || "";
    $("camMeta").textContent =
      [camDevice.devmodel || camDevice.devtype || "", ver].filter(Boolean).join(" · ")
      || "camera";
  }
  $("roPan").textContent = m.pan ?? "—";
  $("roTilt").textContent = m.tilt ?? "—";
  $("roZoom").textContent = m.zoom ?? "—";
  const rd = $("roDrive");
  rd.textContent = m.driving ? "MOVING" : "STOPPED";
  rd.className = m.driving ? "drive-on" : "drive-off";
  if (typeof m.focusAuto === "boolean")
    $("focusAuto").classList.toggle("active", m.focusAuto);
  $("footStatus").textContent = m.connected
    ? `Camera connected${m.error ? " — " + m.error : ""}`
    : "Camera not connected — the service will keep retrying.";
}

/* --------------------------------------------------------------- drive */

const drive = { pan: 0, tilt: 0, zoom: 0, speed: 40 };
const heldPointers = new Map();   // pointerId -> {pan, tilt, zoom}
const heldKeys = new Set();
/* Joystick source: direction signs + magnitude 0..1 (already rescaled out
   of the dead zone). While it is deflected it owns pan/tilt, and the
   Speed slider acts as the cap that the magnitude scales. */
const joyVec = { pan: 0, tilt: 0, mag: 0 };

function composeDrive() {
  let pan = 0, tilt = 0, zoom = 0;
  for (const v of heldPointers.values()) {
    pan += v.pan || 0; tilt += v.tilt || 0; zoom += v.zoom || 0;
  }
  for (const k of heldKeys) {
    if (k === "left") pan -= 1; if (k === "right") pan += 1;
    if (k === "up") tilt += 1; if (k === "down") tilt -= 1;
    if (k === "tele") zoom += 1; if (k === "wide") zoom -= 1;
  }
  if (joyVec.mag > 0) { pan = joyVec.pan; tilt = joyVec.tilt; }
  drive.pan = Math.max(-1, Math.min(1, pan));
  drive.tilt = Math.max(-1, Math.min(1, tilt));
  drive.zoom = Math.max(-1, Math.min(1, zoom));
}

let lastSent = "";
function sendDrive(force = false) {
  composeDrive();
  const speed = joyVec.mag > 0
    ? Math.max(5, Math.min(100, Math.round(drive.speed * joyVec.mag)))
    : drive.speed;
  const payload = JSON.stringify({ t: "drive", pan: drive.pan,
    tilt: drive.tilt, zoom: drive.zoom, speed });
  if (!force && payload === lastSent) return;
  lastSent = payload;
  if (ws && ws.readyState === WebSocket.OPEN) ws.send(payload);
}
/* Keepalive while held: refreshes the server's dead-man timer. */
setInterval(() => {
  if (drive.pan || drive.tilt || drive.zoom) sendDrive(true);
}, 500);

function holdable(el, vec) {
  const start = (e) => {
    e.preventDefault();
    el.setPointerCapture?.(e.pointerId);
    heldPointers.set(e.pointerId, vec);
    el.classList.add("held");
    sendDrive();
  };
  const end = (e) => {
    if (heldPointers.delete(e.pointerId)) {
      el.classList.remove("held");
      sendDrive();
    }
  };
  el.addEventListener("pointerdown", start);
  el.addEventListener("pointerup", end);
  el.addEventListener("pointercancel", end);
  el.addEventListener("contextmenu", (e) => e.preventDefault());
}

document.querySelectorAll("#ptzPad [data-pan]").forEach((btn) => {
  holdable(btn, { pan: +btn.dataset.pan, tilt: +btn.dataset.tilt, zoom: 0 });
});
holdable($("zoomIn"), { pan: 0, tilt: 0, zoom: 1 });
holdable($("zoomOut"), { pan: 0, tilt: 0, zoom: -1 });

$("speed").addEventListener("input", (e) => {
  drive.speed = +e.target.value;
  $("speedVal").textContent = e.target.value;
  sendDrive();
});
$("stopBtn").addEventListener("click", async () => {
  heldPointers.clear(); heldKeys.clear(); resetJoy();
  document.querySelectorAll(".held").forEach((b) => b.classList.remove("held"));
  sendDrive(true);
  await api("/api/ptz/stop", "POST");
});
$("homeBtn").addEventListener("click", () => api("/api/ptz/home", "POST"));

/* -------------------------------------------------------------- joystick
   Pointer Events on the base circle (mouse + touch via one code path).
   The server protocol takes direction signs + one speed, so the stick's
   vector maps as: direction = signs of the unit vector (per-axis 0.35
   gate, so near-axis drags stay axial while true diagonals drive both
   axes); speed = slider x deflection rescaled out of a 12% dead zone —
   push farther, go faster, with the slider value as the top speed.
   Emission rides the same drive-state channel, coalesced to one send
   per animation frame (latest-wins); the server ramps/coalesces further.
   Release (or a cancelled/lost pointer) springs the knob back to center
   and sends a zero state, so the camera stops; the server's dead-man
   switch still backstops a vanished client. A plain tap on the stick
   deliberately does NOTHING: a drag cut short by a dropped connection
   must never be misread as a command (the owner, Nov 7, 2026). Home in this
   mode lives on the dedicated ⌂ HOME button beside the stick. */
const JOY_DEAD = 0.12, JOY_AXIS = 0.35;
const joyBase = $("joyBase"), joyKnob = $("joyKnob");
let joyPointerId = null;
let joyRaf = 0, joyEvt = null;

function joySetKnob(dx, dy) {
  joyKnob.style.setProperty("--jx", `${dx}px`);
  joyKnob.style.setProperty("--jy", `${dy}px`);
}
function joyVector(dx, dy, radius) {
  const dist = Math.hypot(dx, dy);
  const m = Math.min(1, dist / radius);
  if (dist === 0 || m <= JOY_DEAD) {
    joyVec.pan = 0; joyVec.tilt = 0; joyVec.mag = 0; return;
  }
  const ux = dx / dist, uy = dy / dist;
  joyVec.pan = Math.abs(ux) >= JOY_AXIS ? Math.sign(ux) : 0;
  joyVec.tilt = Math.abs(uy) >= JOY_AXIS ? -Math.sign(uy) : 0;
  joyVec.mag = (m - JOY_DEAD) / (1 - JOY_DEAD);
}
function joyApply(e) {
  const r = joyBase.getBoundingClientRect();
  const cx = r.left + r.width / 2, cy = r.top + r.height / 2;
  const radius = r.width / 2;
  const dx = e.clientX - cx, dy = e.clientY - cy;
  const dist = Math.hypot(dx, dy);
  const clamped = Math.min(dist, radius * 0.82);
  const s = dist > 0 ? clamped / dist : 0;
  joySetKnob(dx * s, dy * s);
  joyVector(dx, dy, radius);
  sendDrive();
}
function resetJoy(animate = false) {
  joyPointerId = null; joyEvt = null;
  if (joyRaf) { cancelAnimationFrame(joyRaf); joyRaf = 0; }
  joyVec.pan = 0; joyVec.tilt = 0; joyVec.mag = 0;
  joyKnob.classList.remove("active");
  if (animate) joyKnob.classList.add("returning");  /* before the var */
  joySetKnob(0, 0);                                 /* change = spring  */
}
joyBase.addEventListener("pointerdown", (e) => {
  if (joyPointerId !== null) return;
  e.preventDefault();
  joyBase.setPointerCapture?.(e.pointerId);
  joyPointerId = e.pointerId;
  joyKnob.classList.remove("returning");
  joyKnob.classList.add("active");
  joyEvt = e; joyApply(e);
});
joyBase.addEventListener("pointermove", (e) => {
  if (e.pointerId !== joyPointerId) return;
  joyEvt = e;                       /* latest-wins; rAF throttles the send */
  if (!joyRaf) joyRaf = requestAnimationFrame(() => {
    joyRaf = 0; if (joyEvt && joyPointerId !== null) joyApply(joyEvt);
  });
});
function joyEnd(e) {
  if (e.pointerId !== joyPointerId) return;
  resetJoy(true);
  sendDrive(true);                  /* zero state out now: camera stops */
}
joyBase.addEventListener("pointerup", (e) => joyEnd(e));
joyBase.addEventListener("pointercancel", (e) => joyEnd(e));
joyBase.addEventListener("contextmenu", (e) => e.preventDefault());

/* Joystick mode's Home is a real button beside the stick (plain click
   semantics — no pointer handlers), so a drag that ends abruptly can
   never be misread as a Home command. Button mode keeps its pad-center
   Home (homeBtn, wired above). */
$("joyHomeBtn").addEventListener("click", () => api("/api/ptz/home", "POST"));

/* ---------------------------------------------------- control-style mode
   Buttons | Joystick, persisted so the app reopens the way it was left.
   Switching modes never leaves a drive latched: both sources are reset
   and a zero state goes out. */
const MODE_KEY = "ptzControlMode";
function setMode(mode, save = true) {
  const joy = mode === "joystick";
  $("ptzPad").hidden = joy;
  $("joyZone").hidden = !joy;
  $("joyHomeBtn").hidden = !joy;
  $("modeButtons").classList.toggle("active", !joy);
  $("modeJoystick").classList.toggle("active", joy);
  $("modeHint").textContent = joy
    ? "Drag the stick to move · Home is on the side."
    : "Press and hold a direction to move.";
  if (save) {
    try { localStorage.setItem(MODE_KEY, joy ? "joystick" : "buttons"); }
    catch {}
  }
  resetJoy();
  heldPointers.clear();
  document.querySelectorAll(".held").forEach((b) => b.classList.remove("held"));
  sendDrive(true);
}
$("modeButtons").addEventListener("click", () => setMode("buttons"));
$("modeJoystick").addEventListener("click", () => setMode("joystick"));

/* Focus: AF toggle + momentary near/far (REST is fine — not latency-path). */
$("focusAuto").addEventListener("click", async () => {
  const on = !$("focusAuto").classList.contains("active");
  await api("/api/focus", "POST", { action: on ? "auto" : "manual" });
});
function focusHold(el, action) {
  el.addEventListener("pointerdown", (e) => {
    e.preventDefault(); el.classList.add("held");
    api("/api/focus", "POST", { action });
  });
  const end = () => { el.classList.remove("held");
    api("/api/focus", "POST", { action: "stop" }); };
  el.addEventListener("pointerup", end);
  el.addEventListener("pointercancel", end);
}
focusHold($("focusNear"), "near");
focusHold($("focusFar"), "far");
$("focusPush").addEventListener("click", () =>
  api("/api/focus", "POST", { action: "onepush" }));

/* Keyboard parity with the old app: arrows/WASD move, Q/E zoom. */
const KEYMAP = { ArrowLeft: "left", KeyA: "left", ArrowRight: "right",
  KeyD: "right", ArrowUp: "up", KeyW: "up", ArrowDown: "down",
  KeyS: "down", KeyQ: "wide", KeyE: "tele" };
addEventListener("keydown", (e) => {
  if (e.target.matches("input, textarea, select")) return;
  const k = KEYMAP[e.code];
  if (k) { heldKeys.add(k); sendDrive(); e.preventDefault(); }
  if (e.code.startsWith("Digit")) {
    const n = +e.code.slice(5);
    const p = presets[n - 1];
    if (p) recallPreset(p);
  }
  if (e.code === "Home") api("/api/ptz/home", "POST");
  /* Fullscreen conventions: F toggles; Esc exits the API mode on
     its own, so it only needs handling for the CSS fallback. */
  if (e.code === "KeyF") { if (fsMode) exitFs(); else enterFs(); }
  if (e.code === "Escape" && fsMode === "css") exitFs();
});
addEventListener("keyup", (e) => {
  const k = KEYMAP[e.code];
  if (k) { heldKeys.delete(k); sendDrive(); }
});
addEventListener("blur", () => { heldKeys.clear(); heldPointers.clear(); resetJoy(); sendDrive(true); });

/* -------------------------------------------------------------- stream */

let streamCfg = null, pc = null, snapTimer = null, quality = "hd";

async function startStream() {
  stopStream();
  const urls = quality === "hd"
    ? { whep: streamCfg.whep, hls: streamCfg.hls }
    : { whep: streamCfg.whepSd, hls: streamCfg.hlsSd };
  $("openPlayer").href = urls.whep.replace(/\/whep$/, "");
  setBadge("CONNECTING…", false);
  if (await tryWhep(urls.whep)) { setBadge("WEBRTC · LIVE", true); return; }
  if (video.canPlayType("application/vnd.apple.mpegurl")) {
    video.src = urls.hls;
    try { await video.play(); setBadge("HLS · LIVE", true); return; }
    catch {}
  }
  startSnapshots();
}
function stopStream() {
  if (pc) { try { pc.close(); } catch {} pc = null; }
  if (snapTimer) { clearInterval(snapTimer); snapTimer = null; }
  video.removeAttribute("src"); video.srcObject = null;
  snapImg.hidden = true; video.hidden = false;
}
async function tryWhep(url) {
  try {
    pc = new RTCPeerConnection();
    pc.addTransceiver("video", { direction: "recvonly" });
    pc.addTransceiver("audio", { direction: "recvonly" });
    const got = new Promise((res) => {
      pc.ontrack = (e) => { video.srcObject = e.streams[0]; res(true); };
      setTimeout(() => res(false), 6000);
    });
    const offer = await pc.createOffer();
    await pc.setLocalDescription(offer);
    const r = await fetch(url, { method: "POST",
      headers: { "Content-Type": "application/sdp" }, body: offer.sdp });
    if (!r.ok) throw new Error("WHEP " + r.status);
    await pc.setRemoteDescription({ type: "answer", sdp: await r.text() });
    await video.play().catch(() => {});
    return await got;
  } catch { return false; }
}
function startSnapshots() {
  video.hidden = true; snapImg.hidden = false;
  $("snapBadge").hidden = false;
  setBadge("SNAPSHOT", false);
  const tick = () => { snapImg.src = `/api/snapshot?t=${Date.now()}`; };
  tick(); snapTimer = setInterval(tick, 900);
}
function setBadge(text, live) {
  const b = $("streamBadge");
  b.textContent = "STREAM: " + text;
  b.classList.toggle("live", live);
  if (!live && text !== "SNAPSHOT") $("snapBadge").hidden = true;
  if (live) $("snapBadge").hidden = true;
}
$("qHd").addEventListener("click", () => setQuality("hd"));
$("qSd").addEventListener("click", () => setQuality("sd"));
function setQuality(q) {
  quality = q;
  $("qHd").classList.toggle("active", q === "hd");
  $("qSd").classList.toggle("active", q === "sd");
  if (streamCfg) startStream();
}
$("snapNow").addEventListener("click", () => {
  if (snapImg.hidden) { /* one-off peek without dropping the live stream */
    const w = window.open("", "_blank");
    if (w) w.document.write(`<img src="/api/snapshot?t=${Date.now()}" style="max-width:100%">`);
  } else startSnapshots();
});

/* ------------------------------------------------ fullscreen / chrome
   the owner (Nov 7, 2026, from the app live in a service): tapping the
   video hides the on-video "web stuff" (stream badge, tier bar,
   fullscreen button); tapping again brings it back. The square
   button at the video's bottom right goes full screen, and in
   fullscreen a tap toggles between just-the-video and the controls
   (the button, now a compress icon, stays bottom-right) — in
   portrait and landscape alike.
   The fullscreen target is the STAGE, not the bare <video>, so the
   custom overlays come along. Entry paths, in order:
     1. the Fullscreen API on the stage (desktop, Android, iPad);
     2. on iPhone, where element fullscreen doesn't exist, the
        video element's native fullscreen for the video tiers (its
        own player chrome takes over there — nothing of ours can
        overlay it, by the platform's rules);
     3. a CSS "maximized" stage fixed over the whole viewport for
        anything else (e.g. the snapshot tier on iPhone) — same tap
        behavior, same bottom-right button.
   In fullscreen (paths 1 and 3) the chrome also auto-hides after
   ~3 s without a touch. WB-pick mode owns the pane while active:
   no tap toggle, no fullscreen button. A drag is never a tap. */
const stageEl = document.querySelector(".stage");
const videoWrap = document.querySelector(".video-wrap");
const fsBtn = $("fsBtn");
let fsMode = null;              /* null | "api" | "native" | "css" */
let chromeTimer = null;

function chromeIsOff() { return stageEl.classList.contains("chrome-off"); }
function armChromeTimer() {
  clearTimeout(chromeTimer); chromeTimer = null;
  if (fsMode && !chromeIsOff())
    chromeTimer = setTimeout(
      () => stageEl.classList.add("chrome-off"), 3000);
}
function setChrome(on) {
  stageEl.classList.toggle("chrome-off", !on);
  armChromeTimer();
}
function fsEnterUi() {
  fsBtn.classList.add("in-fs");
  fsBtn.setAttribute("aria-label", "Exit full screen");
  fsBtn.title = "Exit full screen";
  setChrome(true);
}
function fsExitUi() {
  fsBtn.classList.remove("in-fs");
  fsBtn.setAttribute("aria-label", "Full screen");
  fsBtn.title = "Full screen";
  clearTimeout(chromeTimer); chromeTimer = null;
  stageEl.classList.remove("chrome-off");
}
function enterFs() {
  if (fsMode) return;
  const req = stageEl.requestFullscreen
    || stageEl.webkitRequestFullscreen;
  if (req) {
    try {
      const p = req.call(stageEl);
      if (p && p.catch) p.catch(() => fsFallback());
    } catch { fsFallback(); }
    return;
  }
  fsFallback();
}
function fsFallback() {
  if (fsMode) return;
  /* iPhone: the video tiers can use the element's native fullscreen.
     In snapshot mode the <video> is hidden (the stills <img> is the
     picture) — that tier takes the CSS stage instead. */
  if (!video.hidden && video.webkitEnterFullscreen) {
    try {
      fsMode = "native";
      video.webkitEnterFullscreen();
      return;
    } catch { fsMode = null; }
  }
  fsMode = "css";
  stageEl.classList.add("maximized");
  document.body.classList.add("fs-lock");
  fsEnterUi();
}
function exitFs() {
  if (fsMode === "css") {
    fsMode = null;
    stageEl.classList.remove("maximized");
    document.body.classList.remove("fs-lock");
    fsExitUi();
    return;
  }
  if (fsMode === "native") {
    try { video.webkitExitFullscreen?.(); } catch {}
    return;                     /* webkitendfullscreen tidies up */
  }
  if (document.fullscreenElement || document.webkitFullscreenElement) {
    const ex = document.exitFullscreen || document.webkitExitFullscreen;
    if (ex) {
      try {
        const p = ex.call(document);
        if (p && p.catch) p.catch(() => {});
      } catch {}
    }
  }
}
function syncFs() {
  const el = document.fullscreenElement
    || document.webkitFullscreenElement;
  if (el === stageEl) { fsMode = "api"; fsEnterUi(); }
  else if (fsMode === "api") { fsMode = null; fsExitUi(); }
}
document.addEventListener("fullscreenchange", syncFs);
document.addEventListener("webkitfullscreenchange", syncFs);
video.addEventListener("webkitbeginfullscreen", () => { fsMode = "native"; });
video.addEventListener("webkitendfullscreen", () => {
  if (fsMode === "native") fsMode = null;
});
fsBtn.addEventListener("click", (e) => {
  e.stopPropagation();
  if (fsMode) exitFs(); else enterFs();
});

/* Any touch inside the stage while fullscreen restarts the
   auto-hide countdown (the tap handler below decides what a tap
   *means*; this one only keeps the chrome alive while it's used). */
stageEl.addEventListener("pointerdown", () => {
  if (fsMode && !chromeIsOff()) armChromeTimer();
});

/* Tap = short + nearly still. Anything longer or moved is a drag
   (a loupe adjustment, a scroll attempt) and must not toggle. */
let tapState = null;
videoWrap.addEventListener("pointerdown", (e) => {
  if (wbActive || !e.isPrimary) { tapState = null; return; }
  tapState = { id: e.pointerId, x: e.clientX, y: e.clientY,
               t: performance.now(), moved: false };
});
videoWrap.addEventListener("pointermove", (e) => {
  if (tapState && e.pointerId === tapState.id
      && Math.hypot(e.clientX - tapState.x,
                    e.clientY - tapState.y) > 10)
    tapState.moved = true;
});
function tapEnd(e) {
  if (!tapState || e.pointerId !== tapState.id) return;
  const st = tapState; tapState = null;
  if (wbActive || st.moved || performance.now() - st.t > 400) return;
  /* Taps that land on actual controls are theirs, not a toggle. */
  if (e.target.closest("button, a, input, select")) return;
  setChrome(chromeIsOff());
}
videoWrap.addEventListener("pointerup", tapEnd);
videoWrap.addEventListener("pointercancel", (e) => {
  if (tapState && e.pointerId === tapState.id) tapState = null;
});

/* ------------------------------------------------------------- presets */

let presets = [], editing = false;

async function api(path, method = "GET", body = null) {
  const r = await fetch(path, { method,
    headers: body ? { "Content-Type": "application/json" } : {},
    body: body ? JSON.stringify(body) : null });
  if (!r.ok) {
    const t = await r.text().catch(() => "");
    console.warn("api", path, r.status, t);
    return null;
  }
  return r.json().catch(() => null);
}

async function loadPresets() {
  presets = (await api("/api/presets")) || [];
  renderPresets();
}
function renderPresets() {
  const grid = $("presetGrid");
  grid.innerHTML = "";
  presets.forEach((p, idx) => {
    const b = document.createElement("button");
    b.type = "button"; b.className = "preset";
    b.innerHTML = `<span class="pname"></span><span class="pslot"></span>`;
    b.querySelector(".pname").textContent = p.name;
    b.querySelector(".pslot").textContent =
      `Preset ${idx + 1} · VISCA slot ${p.slot}`;
    b.addEventListener("click", () => { if (!editing) recallPreset(p, b); });
    if (editing) {
      const row = document.createElement("span");
      row.className = "pactions";
      const mk = (label, fn) => {
        const x = document.createElement("button");
        x.type = "button"; x.textContent = label;
        x.addEventListener("click", (e) => { e.stopPropagation(); fn(); });
        return x;
      };
      row.append(
        mk("Store here", async () => {
          await api(`/api/presets/${p.id}/store`, "POST"); }),
        mk("Rename", async () => {
          const name = prompt("Preset name:", p.name);
          if (name) { await api(`/api/presets/${p.id}`, "PATCH",
            { name }); loadPresets(); } }),
        mk("◀", () => movePreset(idx, -1)),
        mk("▶", () => movePreset(idx, 1)),
        mk("Delete", async () => {
          if (confirm(`Delete preset "${p.name}"? (The camera slot itself is kept; only the name is removed.)`)) {
            await api(`/api/presets/${p.id}`, "DELETE"); loadPresets(); } }),
      );
      b.append(row);
    }
    grid.append(b);
  });
  $("addPreset").hidden = !editing;
}
async function recallPreset(p, el) {
  const r = await api(`/api/presets/${p.id}/recall`, "POST");
  if (r && el) { el.classList.add("flash");
    setTimeout(() => el.classList.remove("flash"), 600); }
}
async function movePreset(idx, dir) {
  const j = idx + dir;
  if (j < 0 || j >= presets.length) return;
  const ids = presets.map((p) => p.id);
  [ids[idx], ids[j]] = [ids[j], ids[idx]];
  presets = (await api("/api/presets/reorder", "POST", { ids })) || presets;
  renderPresets();
}
$("editToggle").addEventListener("click", () => {
  editing = !editing;
  $("editToggle").textContent = editing ? "Done" : "Edit";
  renderPresets();
});
$("addPreset").addEventListener("click", async () => {
  const name = prompt("Name for the new preset (it takes the next free camera slot; aim the camera first, then use Store here):");
  if (!name) return;
  await api("/api/presets", "POST", { name });
  loadPresets();
});

/* ------------------------------------------------- advanced settings
   Image / white balance / exposure / focus / noise, read and written
   through the service to the camera's own image config (read-modify-
   write server-side, so untouched fields never change). The schema
   below mirrors server/app/imageconf.py — ranges and enums are the
   camera's documented ones. Rows tied to one WB/exposure mode hide in
   the others, the same way the camera's own menus gate them. */
const R = (name, label, min, max, o = {}) =>
  ({ name, label, kind: "range", min, max, ...o });
const S = (name, label, options, o = {}) =>
  ({ name, label, kind: "select", options, ...o });
const T = (name, label, o = {}) => ({ name, label, kind: "toggle", ...o });
const ADV_GROUPS = {
  advImage: [
    R("brightness", "Brightness", 0, 14), R("saturation", "Saturation", 0, 14),
    R("contrast", "Contrast", 0, 14), R("sharpness", "Sharpness", 0, 14),
    R("hue", "Hue", 0, 14),
    T("flip", "Flip (upside down)"), T("mirror", "Mirror (left-right)"),
  ],
  advWb: [
    S("wbMode", "Mode", [["auto", "Auto"], ["indoor", "Indoor · 3000K"],
      ["outdoor", "Outdoor · 5000K"], ["onepush", "One Push"],
      ["manual", "Manual"], ["var", "Color Temp (VAR)"]]),
    R("redGain", "Red gain", 0, 255, { wb: ["manual"] }),
    R("blueGain", "Blue gain", 0, 255, { wb: ["manual"] }),
    R("colorTemp", "Color temp", 2800, 6500,
      { step: 100, unit: "K", wb: ["var"] }),
    R("redTuning", "Red tuning", -10, 10, { wb: ["auto", "indoor", "outdoor"] }),
    R("blueTuning", "Blue tuning", -10, 10, { wb: ["auto", "indoor", "outdoor"] }),
    S("awbSensitivity", "Auto WB sensitivity",
      [["low", "Low"], ["normal", "Normal"], ["high", "High"]]),
    { name: "wbTrigger", label: "One Push", kind: "button",
      text: "Set white balance now", wb: ["onepush"] },
  ],
  advExp: [
    S("expMode", "Mode", [["auto", "Auto"], ["manual", "Manual"],
      ["shutter", "Shutter priority (SAE)"], ["iris", "Iris priority (AAE)"],
      ["bright", "Bright"]]),
    S("shutter", "Shutter",
      ["1/30", "1/60", "1/90", "1/100", "1/125", "1/180", "1/250", "1/350",
       "1/500", "1/725", "1/1000", "1/1500", "1/2000", "1/3000", "1/4000",
       "1/6000", "1/10000"].map((v) => [v, v]), { exp: ["manual", "shutter"] }),
    S("iris", "Iris",
      ["F1.8", "F2.0", "F2.4", "F2.8", "F3.4", "F4.0", "F4.8", "F5.6",
       "F6.8", "F8.0", "F9.6", "F11.0", "Close"].map((v) => [v, v]),
      { exp: ["manual", "iris"] }),
    R("gain", "Gain", 0, 15, { exp: ["manual"] }),
    R("brightLevel", "Bright level", 0, 17, { exp: ["bright"] }),
    T("expCompOn", "Exposure comp.", { exp: ["auto"] }),
    R("expComp", "Comp. value", -7, 7, { exp: ["auto"] }),
    T("backlight", "Backlight comp.", { exp: ["auto"] }),
    R("gainLimit", "Gain limit", 0, 15, { exp: ["auto", "iris", "bright"] }),
    S("antiFlicker", "Anti-flicker",
      [["off", "Off"], ["50hz", "50 Hz"], ["60hz", "60 Hz"]],
      { exp: ["auto", "bright"] }),
    R("drc", "DRC (dynamic range)", 0, 8),
  ],
  advFocus: [
    S("afSensitivity", "AF sensitivity",
      [["low", "Low"], ["normal", "Normal"], ["high", "High"]]),
    S("afZone", "AF zone",
      [["top", "Top"], ["center", "Center"], ["bottom", "Bottom"]]),
  ],
  advNr: [
    S("nr2d", "2D NR", [["off", "Off"], ["auto", "Auto"], ["1", "1"],
      ["2", "2"], ["3", "3"], ["4", "4"], ["5", "5"]]),
    S("nr3d", "3D NR", [["off", "Off"], ["1", "1"], ["2", "2"], ["3", "3"],
      ["4", "4"], ["5", "5"], ["6", "6"], ["7", "7"], ["8", "8"]]),
  ],
};
const advEls = {};   // field name -> {spec, row, input, val, btn}
let advState = null, advLoaded = false;

function buildAdv() {
  for (const [gid, fields] of Object.entries(ADV_GROUPS)) {
    const host = $(gid);
    for (const spec of fields) {
      const row = document.createElement("div");
      row.className = "adv-row";
      const lab = document.createElement("label");
      lab.textContent = spec.label;
      row.append(lab);
      const rec = { spec, row };
      if (spec.kind === "range") {
        const inp = document.createElement("input");
        inp.type = "range"; inp.min = spec.min; inp.max = spec.max;
        inp.step = spec.step || 1;
        inp.setAttribute("aria-label", spec.label);
        const val = document.createElement("span");
        val.className = "adv-val";
        inp.addEventListener("input", () => {
          val.textContent = inp.value + (spec.unit || ""); });
        inp.addEventListener("change", () =>
          applyAdv(spec.name, +inp.value));
        rec.input = inp; rec.val = val;
        row.append(inp, val);
      } else if (spec.kind === "select") {
        const sel = document.createElement("select");
        sel.setAttribute("aria-label", spec.label);
        for (const [v, t] of spec.options) {
          const op = document.createElement("option");
          op.value = v; op.textContent = t; sel.append(op);
        }
        sel.addEventListener("change", () => applyAdv(spec.name, sel.value));
        rec.input = sel;
        row.append(sel);
      } else if (spec.kind === "toggle") {
        const b = document.createElement("button");
        b.type = "button"; b.className = "toggle"; b.textContent = "Off";
        b.addEventListener("click", () =>
          applyAdv(spec.name, !advState?.[spec.name]));
        rec.btn = b;
        row.append(b);
      } else if (spec.kind === "button") {
        const b = document.createElement("button");
        b.type = "button"; b.className = "ghost wide"; b.textContent = spec.text;
        b.addEventListener("click", async () => {
          const r = await api("/api/camera/image/wb-trigger", "POST");
          advMsg(r ? "White balance set from the current view — point at something white and tap again if colors still look off."
                   : "White-balance trigger failed — is the camera connected?");
        });
        rec.btn = b;
        row.append(b);
      }
      host.append(row);
      advEls[spec.name] = rec;
    }
  }
}
function advMsg(t) { $("advMsg").textContent = t; }
function renderAdv(cfg) {
  advState = cfg;
  for (const rec of Object.values(advEls)) {
    const { spec } = rec, v = cfg[spec.name];
    if (spec.wb) rec.row.hidden = !spec.wb.includes(cfg.wbMode);
    if (spec.exp) rec.row.hidden = !spec.exp.includes(cfg.expMode);
    if (v === undefined) continue;
    if (spec.kind === "range") {
      rec.input.value = v; rec.val.textContent = v + (spec.unit || "");
    } else if (spec.kind === "select") rec.input.value = v;
    else if (spec.kind === "toggle") {
      rec.btn.textContent = v ? "On" : "Off";
      rec.btn.classList.toggle("active", !!v);
    }
  }
}
async function loadAdv() {
  const cfg = await api("/api/camera/image");
  if (cfg) { renderAdv(cfg); advLoaded = true; }
  else advMsg("Could not read the camera's settings — is it connected?");
}
async function applyAdv(name, value) {
  const cfg = await api("/api/camera/image", "POST", { [name]: value });
  if (cfg) { renderAdv(cfg); advLoaded = true; advMsg("Saved to the camera."); }
  else { advMsg("Could not save — the camera did not accept that change.");
         if (advState) renderAdv(advState); }
}
$("advToggle").addEventListener("click", () => {
  const body = $("advBody"), open = body.hidden;
  body.hidden = !open;
  $("advToggle").textContent = open ? "Hide" : "Show";
  $("advToggle").setAttribute("aria-expanded", String(open));
  if (open && !advLoaded) loadAdv();
  if (open) loadUpdateStatus();
});

/* ------------------------------------------------- pick a white point
   the owner's refinement (Nov 7, 2026): a first fingertip tap is never
   accurate enough, so picking works like a magnifier loupe. Press and
   drag on the snapshot: a 128 px bubble floats above the finger with
   a ~3x zoomed view; the dot at its exact center is the pixel that
   will be used. Release pins a marker there (re-draggable); the
   coordinates posted on Confirm are the refined loupe-center point,
   never the raw first tap. A swatch + plain-language color read of
   the chosen pixel sits next to Confirm — guidance, never a block.
   The pane may letterbox (object-fit: contain), so the pick layer is
   fitted to the image's ACTUAL rendered rect and every mapping —
   loupe source, marker, posted coordinates — goes through it. */
const wbLayer = $("wbPickLayer"), wbMarker = $("wbMarker"),
      wbLoupe = $("wbLoupe"), wbLoupeCv = $("wbLoupeCanvas"),
      wbBar = $("wbBar"), wbSwatch = $("wbSwatch");
const WB_ZOOM = 3, WB_LOUPE_R = 64;      /* bubble radius, CSS px */
/* Matches the phone-landscape block in style.css: there the pane is
   nearly the whole screen, so the bubble must stay inside the
   viewport itself (see wbDrawLoupe). */
const LANDSCAPE_MQ =
  matchMedia("(orientation: landscape) and (max-height: 540px)");
const wbSrc = document.createElement("canvas");  /* latest snapshot */
let wbActive = false, wbPoint = null;
let wbPointerId = null, wbRaf = 0, wbEvt = null;

snapImg.addEventListener("load", () => {
  if (!snapImg.naturalWidth) return;
  wbSrc.width = snapImg.naturalWidth;
  wbSrc.height = snapImg.naturalHeight;
  wbSrc.getContext("2d").drawImage(snapImg, 0, 0);
  if (wbActive) { wbFitLayer(); if (wbPoint && wbPointerId === null)
    wbSampleFeedback(); }
});

function wbImageRect() {
  const wrap = snapImg.parentElement.getBoundingClientRect();
  const nw = snapImg.naturalWidth || 16, nh = snapImg.naturalHeight || 9;
  const s = Math.min(wrap.width / nw, wrap.height / nh);
  const w = nw * s, h = nh * s;
  return { wrap, left: (wrap.width - w) / 2, top: (wrap.height - h) / 2,
           width: w, height: h };
}
function wbFitLayer() {
  const r = wbImageRect();
  wbLayer.style.left = r.left + "px";
  wbLayer.style.top = r.top + "px";
  wbLayer.style.width = r.width + "px";
  wbLayer.style.height = r.height + "px";
  if (wbPoint) wbPlaceMarker();
}
function wbPlaceMarker() {
  if (wbPointerId !== null) { wbMarker.hidden = true; return; }
  wbMarker.hidden = false;
  wbMarker.style.left = (wbPoint.x * 100) + "%";
  wbMarker.style.top = (wbPoint.y * 100) + "%";
}
function wbPointFromEvent(e) {
  const r = wbLayer.getBoundingClientRect();
  return { x: Math.max(0, Math.min(1, (e.clientX - r.left) / r.width)),
           y: Math.max(0, Math.min(1, (e.clientY - r.top) / r.height)) };
}
function wbDrawLoupe() {
  if (!wbPoint || !wbSrc.width) return;
  const lay = wbLayer.getBoundingClientRect();
  const wrap = snapImg.parentElement.getBoundingClientRect();
  /* Bubble placement: centered on the point's x (clamped on-screen),
     floating above the finger; drops below it when there is no room. */
  const px = (lay.left - wrap.left) + wbPoint.x * lay.width;
  const py = (lay.top - wrap.top) + wbPoint.y * lay.height;
  let cx, cy;
  if (LANDSCAPE_MQ.matches) {
    /* Landscape: the stage nearly fills the screen, so place the
       bubble against the VIEWPORT (in viewport coordinates, then
       convert back to the wrap's). Above/below the finger as usual;
       when a very short screen fits neither, the bubble goes BESIDE
       the finger instead of half off-screen. */
    const R = WB_LOUPE_R, OFF = WB_LOUPE_R + 28;
    const vx = wrap.left + px, vy = wrap.top + py;
    const fitsY = (c) => c - R >= 2 && c + R <= innerHeight - 2;
    let cxv = vx, cyv = null;
    if (py > wrap.height / 2 && fitsY(vy - OFF)) cyv = vy - OFF;
    else if (py <= wrap.height / 2 && fitsY(vy + OFF)) cyv = vy + OFF;
    else if (fitsY(vy - OFF)) cyv = vy - OFF;
    else if (fitsY(vy + OFF)) cyv = vy + OFF;
    if (cyv === null) {
      if (vx + OFF + R <= innerWidth - 2) cxv = vx + OFF;
      else if (vx - OFF - R >= 2) cxv = vx - OFF;
      cyv = Math.max(R + 2, Math.min(innerHeight - R - 2, vy));
    }
    cxv = Math.max(R + 2, Math.min(innerWidth - R - 2, cxv));
    cx = cxv - wrap.left;
    cy = cyv - wrap.top;
  } else {
    cx = Math.max(WB_LOUPE_R + 2,
                  Math.min(wrap.width - WB_LOUPE_R - 2, px));
    /* Above the finger in the lower half, below it in the upper half —
       a 16:9 pane is shorter than the bubble + offset either way, so
       the wrap lets it overflow while picking rather than clip it. */
    cy = py <= wrap.height / 2 ? py + WB_LOUPE_R + 28
                               : py - (WB_LOUPE_R + 28);
  }
  wbLoupe.style.left = cx + "px";
  wbLoupe.style.top = cy + "px";
  /* Zoomed view: the source rect is centered on the exact point, so
     the bubble's center dot IS the chosen pixel. */
  const ctx = wbLoupeCv.getContext("2d");
  const S = wbLoupeCv.width;
  const sw = (2 * WB_LOUPE_R / WB_ZOOM) * (wbSrc.width / lay.width);
  const sh = (2 * WB_LOUPE_R / WB_ZOOM) * (wbSrc.height / lay.height);
  const sx = wbPoint.x * wbSrc.width - sw / 2;
  const sy = wbPoint.y * wbSrc.height - sh / 2;
  ctx.fillStyle = "#000";
  ctx.fillRect(0, 0, S, S);
  ctx.drawImage(wbSrc, sx, sy, sw, sh, 0, 0, S, S);
}
function wbSampleFeedback() {
  if (!wbPoint || !wbSrc.width) return;
  let r = 0, g = 0, b = 0, n = 0;
  try {
    const ctx = wbSrc.getContext("2d");
    const px = Math.round(wbPoint.x * wbSrc.width);
    const py = Math.round(wbPoint.y * wbSrc.height);
    const x0 = Math.max(0, px - 2), y0 = Math.max(0, py - 2);
    const d = ctx.getImageData(x0, y0,
      Math.min(5, wbSrc.width - x0), Math.min(5, wbSrc.height - y0)).data;
    for (let i = 0; i < d.length; i += 4) {
      r += d[i]; g += d[i + 1]; b += d[i + 2]; n++;
    }
  } catch { return; }
  if (!n) return;
  r = Math.round(r / n); g = Math.round(g / n); b = Math.round(b / n);
  wbSwatch.hidden = false;
  wbSwatch.style.background = `rgb(${r}, ${g}, ${b})`;
  let lean = "looks neutral";
  if (r > b * 1.12) lean = "this spot currently leans warm";
  else if (b > r * 1.12) lean = "this spot currently leans cool";
  const luma = 0.2126 * r + 0.7152 * g + 0.0722 * b;
  const spread = Math.max(r, g, b) - Math.min(r, g, b);
  let hint = "";
  if (luma < 55)
    hint = " It is quite dark though — whites work best, so a brighter spot will balance better.";
  else if (spread > 100)
    hint = " It is strongly colored though — a paler, whiter spot will balance better.";
  $("wbFeedback").textContent =
    `The marked spot: ${lean} (R ${r} · G ${g} · B ${b}).${hint}`;
}
function wbShowLoupe() {
  wbMarker.hidden = true;
  wbLoupe.hidden = false;
  wbDrawLoupe();
}
function wbDragEnd() {
  /* The point at RELEASE is the one that counts: apply any move still
     waiting on its animation frame before pinning, never post a stale
     position from an earlier frame. */
  if (wbEvt) wbPoint = wbPointFromEvent(wbEvt);
  wbPointerId = null; wbEvt = null;
  if (wbRaf) { cancelAnimationFrame(wbRaf); wbRaf = 0; }
  wbLoupe.hidden = true;
  if (wbPoint) {
    wbPlaceMarker();
    wbSampleFeedback();
    $("wbConfirm").disabled = false;
  }
}
wbLayer.addEventListener("pointerdown", (e) => {
  if (!wbActive || wbPointerId !== null) return;
  e.preventDefault();
  try { wbLayer.setPointerCapture(e.pointerId); } catch {}
  wbPointerId = e.pointerId;
  wbPoint = wbPointFromEvent(e);       /* rough placement… */
  wbShowLoupe();
});
wbLayer.addEventListener("pointermove", (e) => {
  if (e.pointerId !== wbPointerId) return;
  wbEvt = e;                           /* …refined continuously, */
  if (!wbRaf) wbRaf = requestAnimationFrame(() => {   /* latest-wins */
    wbRaf = 0;
    if (wbEvt && wbPointerId !== null) {
      wbPoint = wbPointFromEvent(wbEvt);
      wbDrawLoupe();
    }
  });
});
wbLayer.addEventListener("pointerup", (e) => {
  if (e.pointerId === wbPointerId) wbDragEnd();
});
wbLayer.addEventListener("pointercancel", (e) => {
  if (e.pointerId === wbPointerId) wbDragEnd();
});
wbLayer.addEventListener("contextmenu", (e) => e.preventDefault());
addEventListener("resize", () => { if (wbActive) wbFitLayer(); });

function wbEnter() {
  if (wbActive) return;
  wbActive = true;
  wbPoint = null;
  snapImg.parentElement.classList.add("picking");
  $("wbBadge").hidden = false;
  wbBar.hidden = false;
  wbLayer.hidden = false;
  wbMarker.hidden = true;
  wbLoupe.hidden = true;
  wbSwatch.hidden = true;
  $("wbConfirm").disabled = true;
  $("wbCancel").disabled = false;
  $("wbMsg").textContent = "";
  $("wbFeedback").textContent =
    "Press the picture and drag — the magnifier shows the exact spot. Let go to pin it; drag the pin to adjust.";
  startSnapshots();              /* pick on the stills element */
  requestAnimationFrame(wbFitLayer);
  /* Portrait: bring the stage into view. Landscape: the stage is
     already the whole screen and the page must not move — a scroll
     here would shove the header off and fight the fixed layout. */
  if (!LANDSCAPE_MQ.matches)
    document.querySelector(".stage")
      .scrollIntoView({ behavior: "smooth", block: "start" });
}
function wbExit() {
  wbActive = false;
  wbPointerId = null;
  snapImg.parentElement.classList.remove("picking");
  $("wbBadge").hidden = true;
  wbBar.hidden = true;
  wbLayer.hidden = true;
  wbLoupe.hidden = true;
  if (streamCfg) startStream();       /* back to the live tiers */
}
$("wbPickBtn").addEventListener("click", wbEnter);
$("wbCancel").addEventListener("click", wbExit);
$("wbConfirm").addEventListener("click", async () => {
  if (!wbPoint) return;
  const btn = $("wbConfirm");
  btn.disabled = true; $("wbCancel").disabled = true;
  $("wbMsg").textContent =
    "Setting white balance — the camera is moving to your spot and will come right back…";
  try {
    const r = await fetch("/api/camera/wb-pick", { method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ x: wbPoint.x, y: wbPoint.y }) });
    const data = await r.json().catch(() => null);
    if (!r.ok) throw new Error((data && data.detail) || ("HTTP " + r.status));
    advMsg("White balance set from the spot you picked — the camera aimed at it, balanced, and came back to where it was.");
    wbExit();
  } catch (err) {
    $("wbMsg").textContent =
      "That did not work: " + err.message + " You can adjust the spot and try again, or cancel.";
    btn.disabled = false; $("wbCancel").disabled = false;
  }
});

/* Preview/test hooks (?wbPickDemo=loupe|pin, ?wbPickTest=1): drive the
   picker deterministically for headless screenshots and the UI check
   that a drag posts the REFINED coordinates, never the first tap. */
function wbMaybeDemo() {
  const q = new URLSearchParams(location.search);
  const lay = () => wbLayer.getBoundingClientRect();
  const ev = (type, fx, fy) => new PointerEvent(type, {
    bubbles: true, pointerId: 7,
    clientX: lay().left + fx * lay().width,
    clientY: lay().top + fy * lay().height });
  if (q.has("wbPickDemo")) {
    wbEnter();
    setTimeout(() => {
      wbFitLayer();
      if (q.get("wbPickDemo") === "loupe") {
        wbLayer.dispatchEvent(ev("pointerdown", 0.62, 0.46));
        wbLayer.dispatchEvent(ev("pointermove", 0.635, 0.44));
      } else {
        wbLayer.dispatchEvent(ev("pointerdown", 0.30, 0.30));
        wbLayer.dispatchEvent(ev("pointermove", 0.58, 0.42));
        wbLayer.dispatchEvent(ev("pointerup", 0.58, 0.42));
      }
    }, 600);
  }
  if (q.has("wbPickTest")) {
    const out = document.createElement("div");
    out.id = "wbTestResult"; out.textContent = "running";
    document.body.append(out);
    const posted = [];
    const of = window.fetch;
    window.fetch = (u, o) => {
      if (String(u).includes("/api/camera/wb-pick"))
        posted.push(JSON.parse(o.body));
      return of(u, o);
    };
    wbEnter();
    setTimeout(() => {
      wbFitLayer();
      wbLayer.dispatchEvent(ev("pointerdown", 0.25, 0.25));
      wbLayer.dispatchEvent(ev("pointermove", 0.66, 0.44));
      wbLayer.dispatchEvent(ev("pointerup", 0.66, 0.44));
      setTimeout(async () => {
        $("wbConfirm").click();
        for (let i = 0; i < 40 && !posted.length; i++)
          await new Promise((r) => setTimeout(r, 250));
        const p = posted[0] || {};
        const ok = Math.abs(p.x - 0.66) < 0.02 &&
                   Math.abs(p.y - 0.44) < 0.02 &&
                   Math.abs(p.x - 0.25) > 0.1;
        out.textContent = JSON.stringify(
          { ok, posted: p, marker: !!wbPoint });
      }, 400);
    }, 600);
  }
}

/* ------------------------------------------------------------- updates
   Staged, hash-verified updates. New builds are copied onto this PC
   into an updates folder ahead of time; the service verifies EVERY
   file against the package's fingerprint manifest before offering
   anything, so a stray file or archive dropped in that folder is
   ignored, never installed. "Update now" hands off to a helper that
   backs up, swaps, restarts just this app, and rolls back on its
   own if the new version doesn't answer. This page rides out the
   restart: it shows the updating overlay, polls for the app's
   return, and reloads itself onto the new version. */
const updState = { data: null, polling: false, bannerKey: null };
const UPD_DISMISS_KEY = "ptzUpdDismiss";
function updDismissed() {
  try { return JSON.parse(sessionStorage.getItem(UPD_DISMISS_KEY)) || {}; }
  catch { return {}; }
}
function updDismiss(patch) {
  try {
    sessionStorage.setItem(UPD_DISMISS_KEY,
      JSON.stringify({ ...updDismissed(), ...patch }));
  } catch {}
}
function updMsg(t) { $("updMsg").textContent = t || ""; }

async function loadUpdateStatus() {
  const s = window.__updDemo || await api("/api/update/status");
  if (s) renderUpdate(s);
  return s;
}
function renderUpdate(s) {
  updState.data = s;
  /* Advanced > Updates row: always reflects the latest known state. */
  $("updCurrent").textContent = (s.current && s.current.version) || "—";
  let stateText = "", canApply = false;
  if (s.state === "available") {
    stateText = `Version ${s.update.version} is ready to install — ` +
      `${s.update.fileCount} files, every one checked against its ` +
      `fingerprint.`;
    canApply = true;
  } else if (s.state === "invalid") {
    stateText = "A staged update failed its safety check and was ignored.";
  } else if (s.state === "applying") {
    stateText = "An update is being applied…";
  } else if (s.state === "applied") {
    stateText = `Updated to version ${(s.result || {}).version || ""}.`;
  } else if (s.state === "rolled_back") {
    stateText = "An update didn't take, so the previous version was " +
      "restored. Nothing was changed.";
  } else if (s.state === "failed") {
    stateText = "The last update attempt failed before anything changed.";
  } else {
    stateText = "No update is staged right now.";
  }
  $("updState").textContent = stateText;
  $("updApplyBtn").hidden = !canApply;
  /* Banner: only when there is something to say. Dismissals are per
     staged version / per result, for this browser session only. */
  const banner = $("updateBanner"), dis = updDismissed();
  const show = (text, key, quiet, withApply, dismissLabel) => {
    $("updateBannerText").textContent = text;
    banner.classList.toggle("quiet", !!quiet);
    $("updateApplyBtn").hidden = !withApply;
    $("updateDismissBtn").textContent = dismissLabel;
    updState.bannerKey = key;
    banner.hidden = false;
  };
  banner.hidden = true;
  updState.bannerKey = null;
  if (s.state === "available") {
    const key = "avail:" + s.update.version + "@" + (s.update.commit || "");
    if (dis.banner !== key)
      show(`An update is ready — version ${s.update.version}. ` +
           `It has already passed its safety check.`,
           { banner: key }, false, true, "Later");
  } else if (s.state === "invalid") {
    const key = "inv:" + (s.reason || "");
    if (dis.banner !== key)
      show("A staged update failed its safety check and was ignored.",
           { banner: key }, true, false, "OK");
  } else if (["applied", "rolled_back", "failed"].includes(s.state)
             && s.result && s.result.at
             && dis.outcome !== s.result.at) {
    const text = s.state === "applied"
      ? `Updated to version ${s.result.version}. You're on the new version now.`
      : s.state === "rolled_back"
        ? "An update didn't take and was rolled back — you're still " +
          "on the previous version. Nothing was changed."
        : "The last update attempt failed before anything was changed.";
    show(text, { outcome: s.result.at }, s.state !== "applied",
         false, "OK");
  }
}
$("updateDismissBtn").addEventListener("click", () => {
  if (updState.bannerKey) updDismiss(updState.bannerKey);
  $("updateBanner").hidden = true;
});
$("updateApplyBtn").addEventListener("click", applyUpdate);
$("updApplyBtn").addEventListener("click", applyUpdate);
$("updateReloadBtn").addEventListener("click", () => location.reload());

async function applyUpdate() {
  if (!updState.data || updState.data.state !== "available"
      || updState.polling) return;
  updState.polling = true;
  updMsg("");
  $("updateBanner").hidden = true;
  $("updateOverlayMsg").textContent = "";
  $("updateReloadBtn").hidden = true;
  $("updateOverlay").hidden = false;
  try {
    const r = await fetch("/api/update/apply", { method: "POST" });
    if (r.status === 409) {
      const d = await r.json().catch(() => null);
      updState.polling = false;
      $("updateOverlay").hidden = true;
      updMsg((d && d.detail) || "The update can't start right now.");
      loadUpdateStatus();
      return;
    }
    /* Any other response — or the connection dropping as the app
       restarts — means the handoff happened; poll for the return. */
  } catch { /* the app may already be going down; polling decides */ }
  pollUpdateReturn();
}
async function pollUpdateReturn() {
  const t0 = Date.now();
  while (updState.polling) {
    await new Promise((r) => setTimeout(r, 2000));
    let s = null;
    try {
      const r = await fetch("/api/update/status",
                            { cache: "no-store" });
      if (r.ok) s = await r.json();
    } catch { /* app is down for the restart; keep waiting */ }
    if (s) {
      if (s.state === "applied") { location.reload(); return; }
      if (["rolled_back", "failed", "invalid"].includes(s.state)) {
        updState.polling = false;
        $("updateOverlay").hidden = true;
        renderUpdate(s);
        window.scrollTo({ top: 0, behavior: "smooth" });
        return;
      }
      if (s.state === "available") {
        /* The helper couldn't start (e.g. service wouldn't stop):
           the update is still staged and nothing changed. */
        updState.polling = false;
        $("updateOverlay").hidden = true;
        renderUpdate(s);
        updMsg("The update didn't start — " +
               ((s.result && s.result.reason) || "try again in a moment."));
        return;
      }
      /* "applying" / "none": still in flight, keep waiting. */
    }
    if (Date.now() - t0 > 100000) {
      $("updateOverlayMsg").textContent =
        "This is taking longer than usual. The app may still be " +
        "restarting — or something needs a look on the PC.";
      $("updateReloadBtn").hidden = false;
    }
  }
}

/* Preview/test hooks (?updateDemo=available|invalid|rolledback|
   applied|applying|row): canned update states for headless
   screenshots, same pattern as the white-point demo hooks. */
function updMaybeDemo() {
  const q = new URLSearchParams(location.search);
  const d = q.get("updateDemo");
  if (!d) return;
  const base = { current: { version: "bf7c536", commit: "bf7c5360" },
                 update: null, reason: null, result: null };
  const canned = {
    available: { ...base, state: "available",
      update: { version: "c0ffee1", commit: "c0ffee1cafe",
                created: "2026-11-07T23:30:00Z", fileCount: 21,
                totalSize: 412345 } },
    invalid: { ...base, state: "invalid",
      reason: "payload file web/app.js does not match its manifest " +
              "fingerprint" },
    rolledback: { ...base, state: "rolled_back",
      result: { result: "rolled_back", version: "c0ffee1", at: "demo",
                reason: "the updated app did not answer" } },
    applied: { ...base, state: "applied",
      result: { result: "applied", version: "c0ffee1", at: "demo" } },
  };
  if (d === "applying") {
    updState.polling = true;
    $("updateOverlay").hidden = false;
    return;
  }
  window.__updDemo = canned[d === "row" ? "available" : d]
    || canned.available;
  if (d === "row") {
    setTimeout(() => {
      if ($("advBody").hidden) $("advToggle").click();
      setTimeout(() => $("advUpdatesGroup")
        .scrollIntoView({ block: "center" }), 350);
    }, 500);
  }
}

/* ---------------------------------------------------------------- init */

(async function init() {
  if ("serviceWorker" in navigator) {
    navigator.serviceWorker.register("/static/sw.js").catch(() => {});
  }
  let savedMode = "buttons";
  try { savedMode = localStorage.getItem(MODE_KEY) || "buttons"; } catch {}
  setMode(savedMode === "joystick" ? "joystick" : "buttons", false);
  buildAdv();
  streamCfg = await api("/api/config");
  await loadPresets();
  const s = await api("/api/status");
  if (s) renderStatus(s);
  wsConnect();
  if (streamCfg) startStream();
  wbMaybeDemo();
  updMaybeDemo();
  loadUpdateStatus();
  if (!window.__updDemo)
    setInterval(() => { if (!updState.polling) loadUpdateStatus(); },
                45000);
})();
