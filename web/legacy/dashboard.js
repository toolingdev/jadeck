"use strict";
/* Service dashboard client (DASHBOARD.md).
   One page over the same service as the Camera view:
   - Program + confidence panes play the gateway's `program` and
     `confidence` paths (WHEP first, native HLS second, then an
     honest no-signal placeholder — there is no snapshot to fall
     back to for these feeds, and the pane says so).
   - The camera quick strip reuses the app's own machinery: presets
     and PTZ stop/home over REST, drive as a *state* over the same
     /ws/control WebSocket the Camera view uses.
   - The three decks ride the three bridges: vMix over its HTTP
     API, Proclaim over MIDI notes, the WING over OSC — all through
     this service's /api endpoints, so the page never talks to the
     hardware directly. */

const $ = (id) => document.getElementById(id);

async function api(path, method = "GET", body = null) {
  const r = await fetch(path, { method,
    headers: body ? { "Content-Type": "application/json" } : {},
    body: body ? JSON.stringify(body) : null });
  if (!r.ok) {
    const t = await r.json().catch(() => null);
    console.warn("api", path, r.status, t);
    return { __error: (t && t.detail) || `HTTP ${r.status}` };
  }
  return r.json().catch(() => null);
}

/* ------------------------------------------------------------ log */

const logBody = $("dashLog");
function nowT() {
  return new Date().toTimeString().slice(0, 8);
}
function log(sys, cls, text) {
  const line = document.createElement("div");
  line.className = "log-line";
  const t = document.createElement("span"); t.className = "t";
  t.textContent = nowT();
  const s = document.createElement("span"); s.className = `sys ${cls}`;
  s.textContent = sys;
  const m = document.createElement("span"); m.textContent = text;
  line.append(t, s, m);
  logBody.prepend(line);
  while (logBody.children.length > 30)
    logBody.removeChild(logBody.lastChild);
}

/* ------------------------------------------------------------ feeds
   One small player per pane. WHEP (sub-second) first; native HLS
   where the browser has it; otherwise the pane's no-signal state
   with a Retry button. The gateway path simply has nothing on it
   until the NDI ingest exists (DASHBOARD.md), so no-signal is a
   normal state, not an error. */

class Feed {
  constructor(video, badge, noSignal, retryBtn) {
    this.video = video; this.badge = badge;
    this.noSignal = noSignal; this.pc = null;
    this.whep = null; this.hls = null; this.snapTimer = null;
    retryBtn?.addEventListener("click", () => this.start());
  }
  setBadge(text, live) {
    this.badge.textContent = text;
    this.badge.classList.toggle("live", !!live);
  }
  async tryWhep(url) {
    try {
      const pc = new RTCPeerConnection();
      this.pc = pc;
      pc.addTransceiver("video", { direction: "recvonly" });
      pc.addTransceiver("audio", { direction: "recvonly" });
      const got = new Promise((res) => {
        pc.ontrack = (e) => { this.video.srcObject = e.streams[0]; res(true); };
        setTimeout(() => res(false), 6000);
      });
      const offer = await pc.createOffer();
      await pc.setLocalDescription(offer);
      const r = await fetch(url, { method: "POST",
        headers: { "Content-Type": "application/sdp" }, body: offer.sdp });
      if (!r.ok) throw new Error("WHEP " + r.status);
      await pc.setRemoteDescription({ type: "answer", sdp: await r.text() });
      await this.video.play().catch(() => {});
      return await got;
    } catch { return false; }
  }
  stop() {
    if (this.pc) { try { this.pc.close(); } catch {} this.pc = null; }
    if (this.snapTimer) { clearInterval(this.snapTimer); this.snapTimer = null; }
    this.video.removeAttribute("src"); this.video.srcObject = null;
  }
  async start() {
    this.stop();
    this.noSignal.hidden = true;
    this.video.hidden = false;
    this.setBadge("CONNECTING…", false);
    if (await this.tryWhep(this.whep)) {
      this.setBadge("WEBRTC · LIVE", true); return;
    }
    if (this.video.canPlayType("application/vnd.apple.mpegurl")) {
      this.video.src = this.hls;
      try { await this.video.play(); this.setBadge("HLS · LIVE", true); return; }
      catch {}
    }
    this.onNoSignal();
  }
  onNoSignal() {
    this.video.hidden = true;
    this.noSignal.hidden = false;
    this.setBadge("NO SIGNAL", false);
  }
}

const programFeed = new Feed($("programVideo"), $("programBadge"),
  $("programNoSignal"), $("programRetry"));
const confidenceFeed = new Feed($("confidenceVideo"),
  $("confidenceBadge"), $("confidenceNoSignal"), $("confidenceRetry"));

/* The camera pane is the same player plus the snapshot fallback the
   camera always has (tier 3 of the Camera view). */
const camFeed = new Feed($("camVideo"), $("camBadge"), { hidden: true }, null);
camFeed.onNoSignal = function () {
  this.video.hidden = true;
  const img = $("camSnap");
  img.hidden = false;
  this.setBadge("SNAPSHOT", false);
  const tick = () => { img.src = `/api/snapshot?t=${Date.now()}`; };
  tick(); this.snapTimer = setInterval(tick, 900);
};

/* ------------------------------------------- camera status + drive
   Same WebSocket and drive-state protocol as the Camera view. */

let ws = null, wsBackoff = 500;
const drive = { pan: 0, tilt: 0, zoom: 0, speed: 40 };
const heldPointers = new Map();

function composeDrive() {
  let pan = 0, tilt = 0, zoom = 0;
  for (const v of heldPointers.values()) {
    pan += v.pan || 0; tilt += v.tilt || 0; zoom += v.zoom || 0;
  }
  drive.pan = Math.max(-1, Math.min(1, pan));
  drive.tilt = Math.max(-1, Math.min(1, tilt));
  drive.zoom = Math.max(-1, Math.min(1, zoom));
}
let lastSent = "";
function sendDrive(force = false) {
  composeDrive();
  const payload = JSON.stringify({ t: "drive", pan: drive.pan,
    tilt: drive.tilt, zoom: drive.zoom, speed: drive.speed });
  if (!force && payload === lastSent) return;
  lastSent = payload;
  if (ws && ws.readyState === WebSocket.OPEN) ws.send(payload);
}
setInterval(() => {           /* dead-man keepalive while held */
  if (drive.pan || drive.tilt || drive.zoom) sendDrive(true);
}, 500);

function wsConnect() {
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  ws = new WebSocket(`${proto}//${location.host}/ws/control`);
  ws.onopen = () => { wsBackoff = 500; sendDrive(true); };
  ws.onmessage = (ev) => {
    let m; try { m = JSON.parse(ev.data); } catch { return; }
    if (m.t === "status") {
      $("connDot").classList.toggle("on", !!m.connected);
      $("footStatus").textContent = m.connected
        ? "Camera connected."
        : "Camera not connected — the service will keep retrying.";
    }
  };
  ws.onclose = () => {
    setTimeout(wsConnect, wsBackoff);
    wsBackoff = Math.min(wsBackoff * 2, 8000);
  };
  ws.onerror = () => { try { ws.close(); } catch {} };
}

function holdable(el, vec) {
  el.addEventListener("pointerdown", (e) => {
    e.preventDefault();
    el.setPointerCapture?.(e.pointerId);
    heldPointers.set(e.pointerId, vec);
    el.classList.add("held");
    sendDrive();
  });
  const end = (e) => {
    if (heldPointers.delete(e.pointerId)) {
      el.classList.remove("held");
      sendDrive();
    }
  };
  el.addEventListener("pointerup", end);
  el.addEventListener("pointercancel", end);
  el.addEventListener("contextmenu", (e) => e.preventDefault());
}
document.querySelectorAll(".dash-pad [data-pan]").forEach((btn) => {
  holdable(btn, { pan: +btn.dataset.pan, tilt: +btn.dataset.tilt, zoom: 0 });
});
holdable($("dashZoomIn"), { pan: 0, tilt: 0, zoom: 1 });
holdable($("dashZoomOut"), { pan: 0, tilt: 0, zoom: -1 });
$("dashStop").addEventListener("click", async () => {
  heldPointers.clear();
  document.querySelectorAll(".held").forEach((b) => b.classList.remove("held"));
  sendDrive(true);
  await api("/api/ptz/stop", "POST");
  log("PTZ CAM", "ptz", "Stop sent from the quick strip");
});
$("dashHome").addEventListener("click", async () => {
  const r = await api("/api/ptz/home", "POST");
  log("PTZ CAM", "ptz", r?.__error ? `Home failed — ${r.__error}`
                                   : "Camera sent Home");
});

/* ------------------------------------------------------- presets */

async function loadDashPresets() {
  const presets = (await api("/api/presets")) || [];
  const grid = $("dashPresets");
  grid.innerHTML = "";
  presets.forEach((p) => {
    const b = document.createElement("button");
    b.type = "button"; b.className = "preset";
    const name = document.createElement("span");
    name.className = "pname"; name.textContent = p.name;
    const slot = document.createElement("span");
    slot.className = "pslot"; slot.textContent = `VISCA slot ${p.slot}`;
    b.append(name, slot);
    b.addEventListener("click", async () => {
      const r = await api(`/api/presets/${p.id}/recall`, "POST");
      if (r?.ok) {
        b.classList.add("flash");
        setTimeout(() => b.classList.remove("flash"), 600);
        log("PTZ CAM", "ptz", `Preset recalled — ${p.name}`);
      } else {
        log("PTZ CAM", "ptz",
            `Preset ${p.name} failed — ${r?.__error || "no answer"}`);
      }
    });
    grid.append(b);
  });
}

/* ------------------------------------------------------------ vMix */

let vmixState = null;
function setPill(id, on, simulated) {
  const pill = $(id);
  pill.querySelector(".dot").classList.toggle("on", !!on);
  pill.classList.toggle("sim", !!simulated);
}
function inputTitle(n) {
  const i = vmixState?.inputs?.find((x) => x.number === n);
  return i ? i.title : "—";
}
function renderVmix() {
  const st = vmixState;
  setPill("pillVmix", st?.connected, st?.simulated);
  const grid = $("vmixInputs");
  grid.innerHTML = "";
  (st?.inputs || []).forEach((inp) => {
    const b = document.createElement("button");
    b.type = "button"; b.className = "input-btn";
    if (inp.number === st.preview) b.classList.add("is-preview");
    if (inp.number === st.active) b.classList.add("is-program");
    const num = document.createElement("span"); num.className = "num";
    num.textContent = `INPUT ${inp.number}`
      + (inp.number === st.active ? " · PGM"
         : inp.number === st.preview ? " · PVW" : "");
    const lbl = document.createElement("span"); lbl.className = "lbl";
    lbl.textContent = inp.title;
    b.append(num, lbl);
    b.addEventListener("click", async () => {
      const r = await api("/api/vmix/preview", "POST",
                          { input: inp.number });
      if (r && !r.__error) {
        vmixState = r; renderVmix();
        log("vMix", "vmix", `Preview → Input ${inp.number} (${inp.title})`);
      } else {
        log("vMix", "vmix", `Preview failed — ${r?.__error || "no answer"}`);
      }
    });
    grid.append(b);
  });
  $("programFoot").textContent = st?.connected
    ? `On program: Input ${st.active} — ${inputTitle(st.active)}`
      + ` · In preview: Input ${st.preview} — ${inputTitle(st.preview)}`
    : "vMix isn't answering — is it open on this PC?";
  $("vmixStreamStat").textContent =
    `● Stream: ${st?.streaming ? "ON" : "OFF"}`;
  $("vmixStreamStat").classList.toggle("on", !!st?.streaming);
  $("vmixRecordStat").textContent =
    `● Record: ${st?.recording ? "ON" : "OFF"}`;
  $("vmixRecordStat").classList.toggle("on", !!st?.recording);
  $("vmixMsg").textContent = st?.connected
    ? (st.simulated
        ? "Simulated vMix (mock mode) — the buttons exercise the real bridge code against a stand-in."
        : "Calls go from this page to vMix's Web Controller on the same PC — no extra software in between.")
    : `vMix not reachable — ${st?.error || "no answer"}. The switcher buttons will work the moment it answers.`;
}
async function refreshVmix() {
  const r = await api("/api/vmix/status");
  if (r && !r.__error) { vmixState = r; renderVmix(); }
}
$("vmixCut").addEventListener("click", async () => {
  const before = vmixState?.preview;
  const r = await api("/api/vmix/cut", "POST");
  if (r && !r.__error) {
    vmixState = r; renderVmix();
    log("vMix", "vmix", `CUT — Input ${before} is now program`);
  } else log("vMix", "vmix", `CUT failed — ${r?.__error || "no answer"}`);
});
$("vmixFade").addEventListener("click", async () => {
  const before = vmixState?.preview;
  const r = await api("/api/vmix/fade", "POST", { durationMs: 1000 });
  if (r && !r.__error) {
    vmixState = r; renderVmix();
    log("vMix", "vmix", `FADE 1s — Input ${before} is now program`);
  } else log("vMix", "vmix", `FADE failed — ${r?.__error || "no answer"}`);
});

/* --------------------------------------------------------- Proclaim */

let proclaimState = null;
let midiFlashTimer = null;
async function refreshProclaim() {
  const r = await api("/api/proclaim/status");
  if (r && !r.__error) {
    proclaimState = r;
    setPill("pillProclaim", r.available || r.simulated, r.simulated);
    $("midiFlash").textContent = r.simulated
      ? `♪ MIDI bridge simulated — notes are recorded, not sent`
      : r.available
        ? `♪ MIDI bridge ready — port “${r.port}”`
        : `♪ MIDI port not open — ${r.port}`;
  }
}
async function proclaimSend(action, label) {
  const r = await api(`/api/proclaim/${action}`, "POST");
  const flash = $("midiFlash");
  if (r?.ok) {
    flash.textContent =
      `♪ MIDI sent · Ch ${r.channel} · Note ${r.note} → ${label}`;
    flash.classList.add("hit");
    clearTimeout(midiFlashTimer);
    midiFlashTimer = setTimeout(() => flash.classList.remove("hit"), 1600);
    log("Proclaim", "proclaim", `${label} — MIDI Ch ${r.channel} · Note ${r.note}`);
  } else {
    flash.textContent = `♪ ${label} failed — ${r?.__error || "no answer"}`;
    log("Proclaim", "proclaim", `${label} failed — ${r?.__error || "no answer"}`);
  }
}
$("proNext").addEventListener("click", () => proclaimSend("next", "Next slide"));
$("proPrev").addEventListener("click", () => proclaimSend("prev", "Previous slide"));
$("proBlack").addEventListener("click", () => proclaimSend("black", "Black screen"));
$("proLogo").addEventListener("click", () => proclaimSend("logo", "Logo screen"));
$("proClear").addEventListener("click", () => proclaimSend("clear", "Clear text"));

/* -------------------------------------------------------------- WING */

let wingState = null;
function renderWing() {
  const st = wingState;
  setPill("pillWing", st?.connected, st?.simulated);
  $("wingState").textContent = !st ? "console: …"
    : !st.configured ? "console: not set up yet"
    : st.connected ? `console: linked${st.simulated ? " (simulated)" : ""}`
    : "console: not answering";
  const grid = $("wingGrid");
  grid.innerHTML = "";
  (st?.channels || []).forEach((ch) => {
    const card = document.createElement("div");
    card.className = "chan" + (ch.muted ? " muted" : "");
    const top = document.createElement("div"); top.className = "chan-top";
    const name = document.createElement("span"); name.className = "chan-name";
    name.textContent = ch.label;
    const assign = document.createElement("span");
    assign.className = "chan-assign";
    assign.textContent = ch.channel ? `Channel ${ch.channel}` : "Unassigned";
    const state = document.createElement("span");
    state.className = "chan-state";
    state.textContent = ch.channel == null ? "—"
      : ch.muted == null ? "UNKNOWN" : ch.muted ? "MUTED" : "LIVE";
    top.append(name, assign, state);
    const mute = document.createElement("button");
    mute.type = "button"; mute.className = "mute-btn";
    mute.textContent = "MUTE";
    mute.setAttribute("aria-pressed", ch.muted ? "true" : "false");
    mute.disabled = ch.channel == null || !st?.connected;
    mute.addEventListener("click", async () => {
      mute.disabled = true;
      const r = await api("/api/wing/mute", "POST", { slot: ch.slot });
      if (r && !r.__error) {
        log("WING", "wing",
            `${ch.label} (Ch ${r.channel}) ${r.muted ? "MUTED" : "LIVE"} — read back from the console`);
        await refreshWing();
      } else {
        log("WING", "wing",
            `${ch.label} mute failed — ${r?.__error || "no answer"}`);
        mute.disabled = false;
      }
    });
    card.append(top, mute);
    grid.append(card);
  });
}
async function refreshWing() {
  const r = await api("/api/wing/status");
  if (r && !r.__error) { wingState = r; renderWing(); }
}

/* ------------------------------------------------------------ macros
   Chained WING actions (MACROS.md). The builder here, the JSON
   import below it, and any AI authoring from the doc all write
   through the same /api/macros endpoints and the same server-side
   validator — this page never invents rules of its own. A run's
   per-step results come back from the engine and are shown on the
   card; the summary line uses the same label shape the server
   puts in its run reports. */

/* Fallback favorites before the first WING status lands — the
   shipped defaults from config.py; the live status replaces them. */
const MACRO_DEFAULT_FAVS = [
  ["MAIN", 1], ["WIRELESS GRAY", 2], ["WIFI MIC 2", 3],
  ["VOX BK 1", 7], ["VOX BK 2", 8], ["ZGTAR", 10],
  ["MONTAGE", 12], ["AUDIENCE", 6],
];
let macroList = [];
let macroRunning = false;
let editingMacroId = null;   /* null = creating */
let editSteps = [];

function macroFavs() {
  const fromWing = (wingState?.channels || [])
    .filter((c) => c.channel != null)
    .map((c) => [c.label, c.channel]);
  return fromWing.length ? fromWing : MACRO_DEFAULT_FAVS;
}
function describeStepJS(step) {
  if (step.type === "delay") {
    if (!step.ms) return "Wait 0 ms";
    return step.ms >= 1000 ? `Wait ${step.ms / 1000} s`
                           : `Wait ${step.ms} ms`;
  }
  const fav = step.channelName
    || (macroFavs().find((f) => f[1] === step.channel) || [])[0];
  const verb = step.muted ? "Mute" : "Unmute";
  return `${verb} ${fav ? `${fav} · Ch ${step.channel}`
                         : `Ch ${step.channel}`}`;
}
function lastRunHtml(m) {
  const wrap = document.createElement("div");
  wrap.className = "macro-lastrun";
  const r = m.lastRun;
  if (!r) {
    wrap.textContent = "Not run yet.";
    return wrap;
  }
  const head = document.createElement("span");
  head.className = r.ok ? "ok" : "bad";
  head.textContent = r.ok ? "✓ Last run: all steps landed"
    : `✗ Last run stopped at step ${r.failedStep}`;
  wrap.append(head);
  if (!r.ok && r.error) wrap.append(` — ${r.error}`);
  const chips = document.createElement("div");
  chips.className = "step-chips";
  r.steps.forEach((s) => {
    const chip = document.createElement("div");
    chip.className = `step-chip ${s.ok ? "pass" : "fail"}`;
    const mark = document.createElement("span"); mark.className = "mark";
    mark.textContent = s.ok ? "✓" : "✗";
    const txt = document.createElement("span");
    txt.textContent = `${s.step}. ${s.label}`
      + (s.ok ? "" : ` — ${s.error || "failed"}`);
    chip.append(mark, txt);
    chips.append(chip);
  });
  for (let i = r.steps.length; i < m.steps.length; i++) {
    const chip = document.createElement("div");
    chip.className = "step-chip skip";
    const mark = document.createElement("span"); mark.className = "mark";
    mark.textContent = "–";
    const txt = document.createElement("span");
    txt.textContent = `${i + 1}. ${describeStepJS(m.steps[i])} — not run`;
    chip.append(mark, txt);
    chips.append(chip);
  }
  wrap.append(chips);
  return wrap;
}
function renderMacros() {
  const box = $("macroList");
  box.innerHTML = "";
  if (!macroList.length) {
    const p = document.createElement("p");
    p.className = "hint";
    p.textContent = "No macros yet — build one below.";
    box.append(p);
    return;
  }
  macroList.forEach((m) => {
    const card = document.createElement("div");
    card.className = "macro-card"
      + (m.lastRun && !m.lastRun.ok ? " failed" : "");
    card.dataset.macroId = m.id;
    const top = document.createElement("div"); top.className = "macro-top";
    const name = document.createElement("span");
    name.className = "macro-name"; name.textContent = m.name;
    top.append(name);
    card.append(top);
    if (m.description) {
      const d = document.createElement("p");
      d.className = "macro-desc"; d.textContent = m.description;
      card.append(d);
    }
    const sum = document.createElement("p");
    sum.className = "macro-summary";
    sum.textContent = m.steps.map(describeStepJS).join("  →  ");
    card.append(sum);
    card.append(lastRunHtml(m));
    const actions = document.createElement("div");
    actions.className = "macro-actions";
    const run = document.createElement("button");
    run.type = "button"; run.className = "macro-run";
    run.textContent = "▶ Run";
    run.disabled = macroRunning;
    run.addEventListener("click", () => runMacro(m, run));
    const edit = document.createElement("button");
    edit.type = "button"; edit.className = "macro-small";
    edit.textContent = "Edit";
    edit.addEventListener("click", () => openMacroEditor(m));
    const del = document.createElement("button");
    del.type = "button"; del.className = "macro-small";
    del.textContent = "Delete";
    del.addEventListener("click", async () => {
      if (!confirm(`Delete the macro “${m.name}”?`)) return;
      const r = await api(`/api/macros/${m.id}`, "DELETE");
      if (r?.ok) {
        log("MACRO", "macro", `Deleted “${m.name}”`);
        if (editingMacroId === m.id) closeMacroEditor();
        refreshMacros();
      } else {
        log("MACRO", "macro",
            `Delete failed — ${r?.__error || "no answer"}`);
      }
    });
    actions.append(run, edit, del);
    card.append(actions);
    box.append(card);
  });
}
async function refreshMacros() {
  const r = await api("/api/macros");
  if (Array.isArray(r)) { macroList = r; renderMacros(); }
}
async function runMacro(m, btn) {
  if (macroRunning) return;
  macroRunning = true;
  btn.textContent = "Running…";
  renderMacrosButtonsOnly();
  log("MACRO", "macro", `Running “${m.name}” — ${m.steps.length} steps`);
  const r = await api(`/api/macros/${m.id}/run`, "POST");
  macroRunning = false;
  if (r && !r.__error && Array.isArray(r.steps)) {
    r.steps.forEach((s) => log("MACRO", "macro",
      `Step ${s.step}: ${s.label} — ${s.ok ? "✓ " + (s.detail || "done")
                                             : "✗ " + (s.error || "failed")}`));
    log("MACRO", "macro", r.ok
      ? `“${m.name}” finished — every step verified on the console`
      : `“${m.name}” stopped — ${r.error}`);
  } else {
    log("MACRO", "macro",
        `“${m.name}” run failed — ${r?.__error || "no answer"}`);
  }
  await refreshMacros();
  refreshWing();
}
function renderMacrosButtonsOnly() {
  document.querySelectorAll(".macro-run")
    .forEach((b) => { b.disabled = macroRunning; });
}

/* ---- builder ---- */

function macroChannelOptions(selectedName) {
  return macroFavs().map(([name, ch]) => {
    const o = document.createElement("option");
    o.value = name;
    o.textContent = `${name} · Ch ${ch}`;
    if (name === selectedName) o.selected = true;
    return o;
  });
}
function syncEditorFromDom() {
  const rows = [...$("macroSteps").querySelectorAll(".mstep")];
  editSteps = rows.map((row) => {
    const kind = row.querySelector(".ms-kind").value;
    if (kind === "delay") {
      return { type: "delay",
               ms: parseInt(row.querySelector(".ms-ms").value, 10) || 0 };
    }
    const chanNum = row.querySelector(".ms-chan-num").value.trim();
    const step = { type: "wing_mute",
                   muted: row.querySelector(".ms-action").value === "mute" };
    if (chanNum) step.channel = parseInt(chanNum, 10);
    else step.channelName = row.querySelector(".ms-chan").value;
    return step;
  });
}
function renderStepRows() {
  const box = $("macroSteps");
  box.innerHTML = "";
  editSteps.forEach((step, i) => {
    const row = document.createElement("div");
    row.className = "mstep";
    const num = document.createElement("span");
    num.className = "mstep-num"; num.textContent = i + 1;
    const kind = document.createElement("select");
    kind.className = "ms-kind";
    [["wing_mute", "Mute / unmute"], ["delay", "Delay"]]
      .forEach(([v, t]) => {
        const o = document.createElement("option");
        o.value = v; o.textContent = t;
        if (step.type === v) o.selected = true;
        kind.append(o);
      });
    kind.addEventListener("change", () => {
      syncEditorFromDom();
      editSteps[i] = kind.value === "delay"
        ? { type: "delay", ms: 1000 }
        : { type: "wing_mute",
            channelName: (macroFavs()[0] || ["MAIN"])[0], muted: true };
      renderStepRows();
    });
    row.append(num, kind);
    if (step.type === "delay") {
      const ms = document.createElement("input");
      ms.type = "number"; ms.className = "ms-ms";
      ms.min = 0; ms.max = 60000; ms.step = 250;
      ms.value = step.ms;
      const unit = document.createElement("span");
      unit.className = "chan-assign"; unit.textContent = "ms";
      row.append(ms, unit);
      /* The other fields stay in the DOM (hidden) so the row's
         reader can treat every row the same way. */
      const hid = (cls, val) => {
        const inp = document.createElement("input");
        inp.type = "hidden"; inp.className = cls; inp.value = val;
        row.append(inp);
      };
      const hsel = document.createElement("select");
      hsel.className = "ms-chan"; hsel.hidden = true;
      hsel.append(...macroChannelOptions(""));
      row.append(hsel);
      hid("ms-chan-num", "");
      const act = document.createElement("select");
      act.className = "ms-action"; act.hidden = true;
      row.append(act);
    } else {
      const chan = document.createElement("select");
      chan.className = "ms-chan";
      chan.append(...macroChannelOptions(step.channelName));
      if (!step.channelName && step.channel != null) {
        const fav = macroFavs().find((f) => f[1] === step.channel);
        if (fav) chan.value = fav[0];
      }
      const chanNum = document.createElement("input");
      chanNum.type = "number"; chanNum.className = "ms-chan-num";
      chanNum.min = 1; chanNum.max = 48;
      chanNum.placeholder = "or Ch #";
      const fav = macroFavs().find((f) => f[1] === step.channel);
      chanNum.value = (!step.channelName && step.channel != null
                       && !fav) ? step.channel : "";
      const act = document.createElement("select");
      act.className = "ms-action";
      [["mute", "Mute"], ["unmute", "Unmute"]].forEach(([v, t]) => {
        const o = document.createElement("option");
        o.value = v; o.textContent = t;
        if ((step.muted ? "mute" : "unmute") === v) o.selected = true;
        act.append(o);
      });
      row.append(chan, chanNum, act);
    }
    const btns = document.createElement("div");
    btns.className = "mstep-btns";
    [["↑", "Move step up", () => {
        if (i > 0) { [editSteps[i - 1], editSteps[i]] =
                       [editSteps[i], editSteps[i - 1]]; }
      }],
     ["↓", "Move step down", () => {
        if (i < editSteps.length - 1) {
          [editSteps[i + 1], editSteps[i]] =
            [editSteps[i], editSteps[i + 1]]; }
      }],
     ["✕", "Remove step", () => { editSteps.splice(i, 1); }]]
      .forEach(([glyph, label, fn]) => {
        const b = document.createElement("button");
        b.type = "button"; b.textContent = glyph;
        b.setAttribute("aria-label", label);
        b.addEventListener("click", () => {
          syncEditorFromDom(); fn(); renderStepRows();
        });
        btns.append(b);
      });
    row.append(btns);
    box.append(row);
  });
}
function openMacroEditor(m) {
  editingMacroId = m ? m.id : null;
  editSteps = m ? m.steps.map((s) => ({ ...s }))
                : [{ type: "wing_mute",
                     channelName: (macroFavs()[0] || ["MAIN"])[0],
                     muted: true }];
  $("macroName").value = m ? m.name : "";
  $("macroDesc").value = m ? m.description : "";
  $("macroErr").hidden = true;
  $("macroEditor").hidden = false;
  renderStepRows();
  $("macroEditor").scrollIntoView({ behavior: "smooth",
                                    block: "nearest" });
}
function closeMacroEditor() {
  editingMacroId = null;
  $("macroEditor").hidden = true;
}
$("macroNew").addEventListener("click", () => openMacroEditor(null));
$("macroCancel").addEventListener("click", closeMacroEditor);
$("macroAddMute").addEventListener("click", () => {
  syncEditorFromDom();
  editSteps.push({ type: "wing_mute",
                   channelName: (macroFavs()[0] || ["MAIN"])[0],
                   muted: true });
  renderStepRows();
});
$("macroAddDelay").addEventListener("click", () => {
  syncEditorFromDom();
  editSteps.push({ type: "delay", ms: 1000 });
  renderStepRows();
});
$("macroSave").addEventListener("click", async () => {
  syncEditorFromDom();
  const doc = { name: $("macroName").value.trim(),
                description: $("macroDesc").value.trim(),
                steps: editSteps };
  const r = editingMacroId
    ? await api(`/api/macros/${editingMacroId}`, "PATCH", doc)
    : await api("/api/macros", "POST", doc);
  if (r && !r.__error && r.id) {
    log("MACRO", "macro",
        `${editingMacroId ? "Updated" : "Created"} macro “${r.name}”`);
    closeMacroEditor();
    refreshMacros();
  } else {
    const err = $("macroErr");
    err.textContent = r?.__error || "the service did not answer";
    err.hidden = false;
  }
});

/* ---- JSON import ---- */

$("macroImportToggle").addEventListener("click", () => {
  const box = $("macroImportBox");
  box.hidden = !box.hidden;
  if (!box.hidden) $("macroImportText").focus();
});
$("macroImportCancel").addEventListener("click", () => {
  $("macroImportBox").hidden = true;
});
$("macroImportBtn").addEventListener("click", async () => {
  const err = $("macroImportErr");
  err.hidden = true;
  let payload;
  try {
    payload = JSON.parse($("macroImportText").value);
  } catch (e) {
    err.textContent = `That is not valid JSON: ${e.message}`;
    err.hidden = false;
    return;
  }
  /* A direct fetch, not the api() helper: a refused import comes
     back with a per-macro errors list, and the operator (or the AI
     that authored the JSON) needs every line of it. */
  let r, body = null;
  try {
    r = await fetch("/api/macros/import", { method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload) });
    body = await r.json().catch(() => null);
  } catch {
    err.textContent = "The service did not answer.";
    err.hidden = false;
    return;
  }
  if (r.ok && Array.isArray(body?.imported)) {
    log("MACRO", "macro",
        `Imported ${body.imported.length} macro(s): `
        + body.imported.map((m) => m.name).join(", "));
    $("macroImportText").value = "";
    $("macroImportBox").hidden = true;
    refreshMacros();
  } else {
    const lines = [body?.detail || `Import refused (HTTP ${r.status})`];
    (body?.errors || []).forEach((e) => lines.push(
      `• ${e.name ? `“${e.name}”` : `macro ${e.index + 1}`}: ${e.error}`));
    err.textContent = lines.join("\n");
    err.hidden = false;
  }
});

/* -------------------------------------------------------------- boot */

(async () => {
  const cfg = await api("/api/config");
  if (cfg) {
    programFeed.whep = cfg.programWhep; programFeed.hls = cfg.programHls;
    confidenceFeed.whep = cfg.confidenceWhep;
    confidenceFeed.hls = cfg.confidenceHls;
    camFeed.whep = cfg.whep; camFeed.hls = cfg.hls;
    programFeed.start(); confidenceFeed.start(); camFeed.start();
  }
  wsConnect();
  loadDashPresets();
  refreshVmix(); refreshProclaim(); refreshWing(); refreshMacros();
  setInterval(refreshVmix, 4000);
  setInterval(refreshWing, 6000);
  log("SYSTEM", "sys", "Dashboard opened. Program, slides, camera and audio in one place.");
})();

if ("serviceWorker" in navigator) {
  navigator.serviceWorker.register("/static/sw.js").catch(() => {});
}
