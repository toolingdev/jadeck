// jadeck shared frame kit v1 (design/WIDGETS-V2.md "Shared frame kit").
// Optional: an add-on frame loads it with
//   <link rel="stylesheet" href="/api/kit/v1/kit.css">
//   <script src="/api/kit/v1/kit.js"></script>
// and gets window.JK: theme, header, buttons, hold to confirm, segmented
// control, lock, fader strip, knob, LED meter and an auto-fit button grid,
// all looking and behaving like jadeck's own widgets (WIDGETS.md §6).
// Plain ES5-style JS, no framework, no build step. It does not talk to the
// host: the frame keeps its own bridge (postMessage) and passes callbacks
// in; call JK.theme(init.theme) and JK.setEditing(init.editing) from it.
// v1 only changes compatibly; a breaking change will be /api/kit/v2/.
(function () {
  "use strict";
  if (window.JK && window.JK.version) return; // loaded twice
  var NS = "http://www.w3.org/2000/svg", uid = 0;
  var JK = window.JK = { version: "1.0.0", editing: false, holds: 0, onIdle: null };

  // ---------------------------------------------------------------- basics
  function el(tag, cls, parent, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text != null) e.textContent = text;
    if (parent) parent.appendChild(e);
    return e;
  }
  JK.el = el;
  // theme applies the host's theme variables (init.theme / theme messages).
  JK.theme = function (vars) {
    for (var k in vars || {}) if (k.indexOf("--") === 0) document.documentElement.style.setProperty(k, vars[k]);
  };
  JK.cssVar = function (name, d) {
    var v = getComputedStyle(document.documentElement).getPropertyValue(name).trim();
    return v || d;
  };
  // setEditing: while the dashboard is edited, controls do nothing
  // (.jk-live stops taking the pointer, holds are cancelled).
  var editHooks = [];
  JK.setEditing = function (on) {
    JK.editing = !!on;
    document.body.classList.toggle("jk-editing", JK.editing);
    if (JK.editing) editHooks.forEach(function (f) { f(); });
  };
  JK.reduced = function () { return typeof matchMedia === "function" && matchMedia("(prefers-reduced-motion: reduce)").matches; };
  function clamp(v, a, b) { return Math.min(b, Math.max(a, v)); }

  // ---------------------------------------------------------------- header
  // JK.header({title, status, source, note, right}) → {el, set(o), note(text, bad), right}
  // title: text or a node (e.g. a picker); status: "live" (red, pulsing:
  // on air only), "on", "ok", "warn", "bad" or "" (no dot); source: the
  // connection, in small caps; right: text (small caps) or a node.
  // Uses the page's first <header> when there is one.
  JK.header = function (o) {
    var h = document.querySelector("header") || document.body.insertBefore(el("header"), document.body.firstChild);
    h.textContent = "";
    h.classList.add("jk-hdr");
    var t = el("h1", "jk-title", h), dot = el("i", "jk-dot", h), src = el("span", "jk-src", h), note = el("span", "jk-note", h), right = el("span", "jk-right", h);
    dot.hidden = true;
    var api = {
      el: h, right: right,
      set: function (p) {
        p = p || {};
        if ("title" in p) { t.textContent = ""; if (p.title && p.title.nodeType) { t.appendChild(p.title); t.style.overflow = "visible"; } else t.textContent = p.title || ""; }
        if ("status" in p) { dot.className = "jk-dot" + (p.status ? " " + p.status : ""); dot.hidden = !p.status; }
        if ("source" in p) src.textContent = p.source || "";
        if ("note" in p) api.note(p.note, p.bad);
        if ("right" in p) { right.textContent = ""; if (p.right && p.right.nodeType) right.appendChild(p.right); else if (p.right) right.textContent = p.right; }
        return api;
      },
      note: function (text, bad) { note.textContent = text || ""; note.classList.toggle("bad", !!bad); note.title = text || ""; },
    };
    return api.set(o || {});
  };

  // --------------------------------------------------------------- buttons
  // JK.button({label, sub, tone, active, disabled, title, onClick}) → <button>
  // tone: "pgm" (red: on air / muted), "pvw" (green: next), "accent"
  // (amber: the primary action); active = selected (amber); disabled =
  // flat and dim. onClick may return a promise: the button shows busy
  // until it settles. btn.set({...}) changes any of these later.
  function toneClass(o) {
    if (o.disabled) return "off";
    if (o.tone === "pgm") return "pgm";
    if (o.tone === "pvw") return "pvw";
    if (o.active || o.tone === "accent" || o.tone === "primary") return "sel";
    return "";
  }
  JK.button = function (o) {
    o = o || {};
    var b = el("button", "jk-btn jk-live");
    b.type = "button";
    var l = el("span", "jk-l", b), s = el("span", "jk-s", b), cur = {};
    b.set = function (p) {
      for (var k in p) cur[k] = p[k];
      if ("label" in p) l.textContent = cur.label == null ? "" : cur.label;
      if ("sub" in p) s.textContent = cur.sub || "";
      b.classList.remove("pgm", "pvw", "sel", "off");
      var tc = toneClass(cur);
      if (tc) b.classList.add(tc);
      b.disabled = !!cur.disabled;
      if ("title" in p || "label" in p) b.title = cur.title || ((cur.label || "") + (cur.sub ? " · " + cur.sub : ""));
      return b;
    };
    b.set(o);
    b.addEventListener("click", function () {
      if (JK.editing || b.disabled || !cur.onClick || b.classList.contains("busy")) return;
      var r = cur.onClick();
      if (r && typeof r.then === "function") {
        b.classList.add("busy");
        r.then(done, done);
      }
      function done() { b.classList.remove("busy"); }
    });
    return b;
  };

  // ------------------------------------------------------- hold to confirm
  // Same as the core HoldButton (frontend/src/dash/addon/hold.tsx): risky
  // actions are pressed and held (5 s by default). The fill grows left →
  // right in the action's colour with a shimmer; letting go or sliding off
  // drains it back and sends nothing; at the end it fires once (vibrating
  // where it can). Space / Enter held work the same.
  //
  // JK.hold(el, {seconds, tone, onProgress(secondsLeft, p), onCancel(), onFire()})
  // makes any element holdable (e.g. a picture tile). seconds may be a
  // function. JK.holds counts holds in progress; JK.onIdle() runs when it
  // drops back to 0 (redraw then, never under a finger).
  var DRAIN_MS = 300;
  JK.hold = function (target, o) {
    var fill = el("i", "jk-hold-fill " + (o.tone || "pgm"));
    target.insertBefore(fill, target.firstChild);
    var p = 0, secs = 5, t0 = 0, raf = 0, held = false, last = -1, counted = false;
    function set(v) { p = v; fill.style.transform = "scaleX(" + v + ")"; }
    function count(on) {
      if (on === counted) return;
      counted = on;
      JK.holds = Math.max(0, JK.holds + (on ? 1 : -1));
      if (!JK.holds && JK.onIdle) setTimeout(function () { if (!JK.holds && JK.onIdle) JK.onIdle(); }, 0);
    }
    function tick(now) {
      if (!held) return;
      var v = Math.min(1, (now - t0) / (secs * 1000)), left = Math.max(1, Math.ceil(secs * (1 - v)));
      set(v);
      if (left !== last && v < 1) { last = left; if (o.onProgress) o.onProgress(left, v); }
      if (v >= 1) { fire(); return; }
      raf = requestAnimationFrame(tick);
    }
    function start() {
      if (JK.editing || target.disabled || target.classList.contains("off") || held) return;
      var s = typeof o.seconds === "function" ? o.seconds() : o.seconds;
      secs = clamp(Number(s) || 5, 0.5, 30);
      held = true; last = -1;
      cancelAnimationFrame(raf); clearTimeout(raf);
      t0 = performance.now() - p * secs * 1000;
      target.classList.add("jk-holding");
      count(true);
      raf = requestAnimationFrame(tick);
    }
    function fire() {
      held = false;
      try { if (navigator.vibrate) navigator.vibrate(40); } catch (e) { /* not a phone */ }
      Promise.resolve().then(o.onFire).catch(function () {});
      // show it full for a moment, then empty
      raf = setTimeout(function () { set(0); target.classList.remove("jk-holding"); if (o.onCancel) o.onCancel(); count(false); }, 250);
    }
    function release() {
      if (!held) return;
      held = false;
      cancelAnimationFrame(raf);
      target.classList.remove("jk-holding");
      var from = p, s0 = performance.now(), dur = DRAIN_MS * Math.max(0.3, from);
      var drain = function (now) {
        var v = from * (1 - Math.min(1, (now - s0) / dur));
        set(v);
        if (v > 0) raf = requestAnimationFrame(drain);
        else count(false);
      };
      raf = requestAnimationFrame(drain);
      if (o.onCancel) o.onCancel();
    }
    target.classList.add("jk-hold");
    target.addEventListener("pointerdown", function (e) {
      if (e.button) return;
      e.preventDefault();
      try { target.setPointerCapture(e.pointerId); } catch (x) { /* fine */ }
      start();
    });
    target.addEventListener("pointermove", function (e) {
      if (!held) return;
      var r = target.getBoundingClientRect();
      if (e.clientX < r.left || e.clientX > r.right || e.clientY < r.top || e.clientY > r.bottom) release(); // slid off
    });
    target.addEventListener("pointerup", release);
    target.addEventListener("pointercancel", release);
    target.addEventListener("lostpointercapture", release);
    target.addEventListener("contextmenu", function (e) { e.preventDefault(); });
    target.addEventListener("click", function (e) { e.preventDefault(); });
    target.addEventListener("keydown", function (e) {
      if (e.key !== " " && e.key !== "Enter") return;
      e.preventDefault();
      if (!e.repeat) start();
    });
    target.addEventListener("keyup", function (e) { if (e.key === " " || e.key === "Enter") { e.preventDefault(); release(); } });
    target.addEventListener("blur", release);
    editHooks.push(release);
    return { cancel: release, holding: function () { return held; } };
  };

  var VERBS = { stop: "STOPS", start: "STARTS", record: "RECORDS", end: "ENDS", cut: "CUTS", go: "GOES", restart: "RESTARTS",
    clear: "CLEARS", reset: "RESETS", "delete": "DELETES", remove: "REMOVES", mute: "MUTES", unmute: "UNMUTES", play: "PLAYS" };
  JK.verb = function (label) { var w = String(label || "").trim().split(/\s+/)[0].toLowerCase(); return VERBS[w] || "SENDS"; };
  function secsText(s) { return s % 1 ? s.toFixed(1) : String(s); }

  // JK.holdButton({label, sub, holdLabel, seconds = 5, tone = "pgm", verb,
  // active, disabled, onFire}) → <button>. Idle: label + "HOLD 5 S · sub";
  // held: "Keep holding…" + "STOPS IN 3 S" (verb from the label's first
  // word, or o.verb). btn.set({...}) changes it later.
  JK.holdButton = function (o) {
    var cur = { tone: "pgm", seconds: 5 };
    var b = JK.button({});
    var l = b.querySelector(".jk-l"), s = b.querySelector(".jk-s"), holding = false;
    function idle() {
      holding = false;
      var secs = clamp(Number(cur.seconds) || 5, 0.5, 30);
      l.textContent = cur.label || "";
      s.textContent = "HOLD " + secsText(secs) + " S" + (cur.sub ? " · " + cur.sub : "");
      b.setAttribute("aria-label", (cur.label || "") + ", press and hold for " + secsText(secs) + " seconds");
      b.title = cur.title || ((cur.label || "") + " — press and hold " + secsText(secs) + " s");
    }
    var baseSet = b.set;
    b.set = function (p) {
      for (var k in p) cur[k] = p[k];
      baseSet({ tone: "", active: cur.active, disabled: cur.disabled });
      b.classList.remove("t-pgm", "t-pvw", "t-accent");
      b.classList.add("t-" + (cur.tone === "pvw" || cur.tone === "accent" ? cur.tone : "pgm"));
      fill.className = "jk-hold-fill " + (cur.tone === "pvw" || cur.tone === "accent" ? cur.tone : "pgm");
      if (!holding) idle();
      return b;
    };
    JK.hold(b, {
      seconds: function () { return cur.seconds; },
      onProgress: function (left) {
        holding = true;
        l.textContent = cur.holdLabel || "Keep holding…";
        s.textContent = (cur.verb || JK.verb(cur.label)) + " IN " + left + " S";
      },
      onCancel: idle,
      onFire: function () { return cur.onFire && cur.onFire(); },
    });
    var fill = b.querySelector(".jk-hold-fill");
    b.set(o || {});
    return b;
  };

  // ------------------------------------------------------------ button grid
  // fitGrid: the best grid for n buttons in a w×h box (same rules as the
  // core buttons widget): near-square buttons, rows 48–160 px; when nothing
  // fits at full size it shrinks (34 px rows, 56 px columns, labels
  // truncate) before giving up; null = too small even for that (then the
  // grid flows and scrolls, the last resort).
  var GAP = 8, MIN_H = 48, MAX_H = 160, MIN_W = 72;
  JK.fitGrid = function (n, w, h, fixedCols, longest) {
    if (n < 1 || w <= 0 || h <= 0) return null;
    var best = null, tries = [], c;
    if (fixedCols) tries.push(Math.min(fixedCols, 8)); else for (c = 1; c <= Math.min(n, 8); c++) tries.push(c);
    function pass(minH, minW, labels) {
      tries.forEach(function (c) {
        var r = Math.ceil(n / c), bw = (w - (c - 1) * GAP) / c, bh = Math.min(MAX_H, (h - (r - 1) * GAP) / r);
        var needW = labels ? Math.max(MIN_W, (longest || 0) * 13.5 * 0.6 + 20) : minW;
        if (bh < minH || (!fixedCols && bw < needW && (labels ? c > 1 : true))) return;
        var score = Math.min(bw, bh * 1.6) * bh - (c * r - n) * 0.01;
        if (!best || score > best.score) best = { cols: c, rows: r, bh: bh, bw: bw, score: score };
      });
    }
    pass(MIN_H, MIN_W, true);
    if (!best && !fixedCols) pass(34, 56, false);
    return best && { cols: best.cols, rows: best.rows, bh: best.bh, bw: best.bw };
  };
  // JK.grid(parent, {columns}) → {el, layout()}: put buttons in grid.el,
  // call layout() after adding them and on resize. Labels grow a little on
  // big buttons (13.5 → 19 px).
  JK.grid = function (parent, o) {
    o = o || {};
    var g = el("div", "jk-grid", parent);
    var api = {
      el: g,
      layout: function () {
        var items = g.children, n = items.length, longest = 0;
        for (var i = 0; i < n; i++) {
          var lb = items[i].querySelector(".jk-l");
          longest = Math.max(longest, Math.min(16, (lb ? lb.textContent : items[i].textContent).length));
        }
        var f = JK.fitGrid(n, g.clientWidth || parent.clientWidth, g.clientHeight || parent.clientHeight, o.columns || 0, longest);
        g.classList.toggle("fit", !!f);
        g.classList.toggle("flow", !f);
        if (f) {
          g.style.gridTemplateColumns = "repeat(" + f.cols + ", minmax(0, 1fr))";
          g.style.gridTemplateRows = "repeat(" + f.rows + ", minmax(" + Math.min(MIN_H, Math.floor(f.bh)) + "px, 1fr))";
          g.style.maxHeight = (f.rows * MAX_H + (f.rows - 1) * GAP) + "px";
          var font = Math.max(13.5, Math.min(19, 13.5 + (f.bh - MIN_H) * 0.06, (f.bw - 20) / (Math.max(1, longest) * 0.6)));
          g.style.setProperty("--jk-font", font.toFixed(1) + "px");
        } else {
          g.style.gridTemplateColumns = o.columns ? "repeat(" + o.columns + ", minmax(0, 1fr))" : "repeat(auto-fill, minmax(96px, 1fr))";
          g.style.gridTemplateRows = g.style.maxHeight = "";
          g.style.removeProperty("--jk-font");
        }
        return f;
      },
    };
    return api;
  };

  // ------------------------------------------------------------- segmented
  // JK.segmented({options: [{value, label}], value, onChange(value)}) → el;
  // el.set(value[, options]); el.disable(bool).
  JK.segmented = function (o) {
    var s = el("div", "jk-seg jk-live"), opts = o.options || [], val = o.value, dis = false;
    function draw() {
      s.textContent = "";
      opts.forEach(function (op) {
        var b = el("button", op.value === val ? "on" : "", s, op.label == null ? String(op.value) : op.label);
        b.type = "button";
        b.disabled = dis;
        b.title = op.title || b.textContent;
        b.onclick = function () { if (JK.editing || op.value === val) return; val = op.value; draw(); if (o.onChange) o.onChange(val); };
      });
    }
    s.set = function (v, options) { if (options) opts = options; val = v; draw(); return s; };
    s.disable = function (d) { dis = !!d; draw(); };
    s.value = function () { return val; };
    draw();
    return s;
  };

  // ------------------------------------------------------------------ lock
  // JK.lock({relockMs = 30000, onChange(locked)}) → {el, locked, set(bool), touch()}
  // Knobs and other fine controls stay locked until "TAP TO EDIT"; they
  // lock again relockMs after the last touch. Call touch() on every change.
  JK.lock = function (o) {
    o = o || {};
    var b = el("button", "jk-lock jk-live"), relock = o.relockMs || 30000, lastTouch = 0;
    b.type = "button";
    var api = {
      el: b, locked: true,
      set: function (on) {
        api.locked = !!on;
        lastTouch = Date.now();
        b.textContent = on ? "LOCKED · TAP TO EDIT" : "EDITING · TAP TO LOCK";
        b.classList.toggle("open", !on);
        b.title = on ? "Tap to change values (locks again after " + Math.round(relock / 1000) + " s)" : "Tap to lock";
        if (o.onChange) o.onChange(api.locked);
      },
      touch: function () { lastTouch = Date.now(); },
    };
    b.onclick = function () { if (!JK.editing) api.set(!api.locked); };
    b.textContent = "LOCKED · TAP TO EDIT";
    b.title = "Tap to change values (locks again after " + Math.round(relock / 1000) + " s)";
    setInterval(function () { if (!api.locked && Date.now() - lastTouch > relock) api.set(true); }, 1000);
    editHooks.push(function () { if (!api.locked) api.set(true); });
    return api;
  };

  // ------------------------------------------------------------ fader laws
  // A law maps dB to the fader's travel (0 bottom … 1 top) and back:
  // {toPos(db), toDb(pos), ticks: [dB…], hot, warm} (meter colours: red
  // from `hot` dB, amber from `warm`). -144 dB means −∞.
  // JK.law(points, ticks, {hot, warm}) builds one from [pos, dB] points.
  JK.law = function (P, ticks, m) {
    m = m || {};
    return {
      toPos: function (db) {
        if (db == null || db <= P[0][1]) return 0;
        for (var i = 1; i < P.length; i++) if (db <= P[i][1]) return P[i - 1][0] + (P[i][0] - P[i - 1][0]) * (db - P[i - 1][1]) / (P[i][1] - P[i - 1][1]);
        return 1;
      },
      toDb: function (pos) {
        if (pos <= 0) return P[0][1];
        for (var i = 1; i < P.length; i++) if (pos <= P[i][0]) return Math.round((P[i - 1][1] + (P[i][1] - P[i - 1][1]) * (pos - P[i - 1][0]) / (P[i][0] - P[i - 1][0])) * 10) / 10;
        return P[P.length - 1][1];
      },
      ticks: ticks, hot: m.hot == null ? 2 : m.hot, warm: m.warm == null ? -6 : m.warm,
    };
  };
  JK.laws = {
    // a console fader (design/wing-polished.svg): +10 at the top, 0 dB at
    // 78 %, -50 at 10 %, −∞ at the bottom
    console: JK.law([[0, -144], [0.0001, -90], [0.10, -50], [0.23, -30], [0.37, -20], [0.54, -10], [0.66, -5], [0.78, 0], [0.90, 5], [1, 10]],
      [10, 5, 0, -5, -10, -20, -30, -50, -144], { hot: 2, warm: -6 }),
  };
  JK.dbText = function (db, unit) {
    if (db == null) return "–";
    if (db <= -143.9 || db === -Infinity) return "−∞";
    return (db > 0.04 ? "+" : db < -0.04 ? "−" : "") + Math.abs(db).toFixed(1) + (unit ? " " + unit : "");
  };

  // ------------------------------------------------------------- LED meter
  // JK.led(canvas, cols, {segs, law}): segmented LEDs along the fader
  // scale, green → amber → red, a peak-hold square. cols = [{level, peak}]
  // in dB: one column for mono, two (L, R) for stereo.
  JK.led = function (cv, cols, o) {
    if (typeof o === "number") o = { segs: o };
    o = o || {};
    var law = o.law || JK.laws.console;
    var dpr = window.devicePixelRatio || 1, w = cv.clientWidth, h = cv.clientHeight;
    if (!w || !h) return;
    if (cv.width !== Math.round(w * dpr) || cv.height !== Math.round(h * dpr)) { cv.width = Math.round(w * dpr); cv.height = Math.round(h * dpr); }
    var g = cv.getContext("2d");
    g.setTransform(dpr, 0, 0, dpr, 0, 0);
    g.clearRect(0, 0, w, h);
    var n = typeof o.segs === "number" ? o.segs : Math.max(8, Math.min(30, Math.floor(h / 10)));
    var gap = n * 3 > h ? 1 : 2, sh = Math.max(0.5, (h - gap * (n - 1)) / n), cgap = cols.length > 1 ? 4 : 0, cw = (w - cgap * (cols.length - 1)) / cols.length;
    var red = JK.cssVar("--jk-led-red", "#e5484d"), amber = JK.cssVar("--jk-led-amber", "#f5a524"), green = JK.cssVar("--jk-led-green", "#30a46c"), unlit = JK.cssVar("--jk-unlit", "#1c2128");
    for (var c = 0; c < cols.length; c++) {
      var lv = cols[c].level, pk = cols[c].peak, x = c * (cw + cgap);
      for (var i = 0; i < n; i++) {
        var db = law.toDb((i + 0.5) / n), y = h - (i + 1) * sh - i * gap, top = law.toDb((i + 1) / n);
        var lit = lv != null && lv >= db, hold = !lit && pk != null && pk >= db && pk < top + 0.01;
        g.fillStyle = lit || hold ? (db >= law.hot ? red : db >= law.warm ? amber : green) : unlit;
        g.globalAlpha = hold ? 0.85 : 1;
        var r = Math.max(0, Math.min(1.2, sh / 3));
        if (g.roundRect) { g.beginPath(); g.roundRect(x, y, cw, sh, r); g.fill(); } else g.fillRect(x, y, cw, sh);
      }
    }
    g.globalAlpha = 1;
  };
  // JK.peaks() → peak(key, db): peak hold per key (1.5 s, then falls).
  JK.peaks = function () {
    var m = {};
    return function (key, db) {
      var now = Date.now(), p = m[key];
      if (db == null) return p ? p.v : null;
      if (!p || db >= p.v || now - p.at > 1500) m[key] = p = { v: db, at: now };
      return p.v;
    };
  };

  // ------------------------------------------------------------------ knob
  // new JK.Knob(parent, {size, color, bipolar, label, def}) → knob
  // A 270° ring; the value arc in the knob's colour (amber gain, blue
  // filter, grey frequency: JK.knobColors), bipolar knobs fill from 12
  // o'clock; shaded body, white pointer, the value below, a small label.
  // Drag up/down or the wheel to turn; Shift or a second finger: fine;
  // double-tap resets to def. knob.onTurn(pos, final); knob.set(pos, text)
  // (ignored while a finger is on it); knob.show(text); knob.setDisabled(d).
  // Pair it with JK.lock() so nothing turns until "TAP TO EDIT".
  var SWEEP = 135;
  JK.knobColors = { gain: "#f5a524", filter: "#3e8ede", freq: "#8a93a0" };
  function svg(tag, attrs, parent) {
    var e = document.createElementNS(NS, tag);
    for (var k in attrs) e.setAttribute(k, attrs[k]);
    if (parent) parent.appendChild(e);
    return e;
  }
  function pt(cx, cy, r, deg) { var a = deg * Math.PI / 180; return [cx + r * Math.sin(a), cy - r * Math.cos(a)]; }
  function arc(cx, cy, r, a0, a1) {
    if (Math.abs(a1 - a0) < 0.01) return "";
    var s = pt(cx, cy, r, Math.min(a0, a1)), e = pt(cx, cy, r, Math.max(a0, a1));
    return "M" + s[0].toFixed(2) + " " + s[1].toFixed(2) + " A" + r + " " + r + " 0 " + (Math.abs(a1 - a0) > 180 ? 1 : 0) + " 1 " + e[0].toFixed(2) + " " + e[1].toFixed(2);
  }
  function Knob(parent, o) {
    var self = this, size = Math.max(40, o.size || 50), r = size / 2 - 3, cx = size / 2, cy = size / 2, br = r - 6;
    this.o = o; this.pos = 0; this.disabled = false;
    this.el = el("div", "jk-knob jk-live", parent);
    var s = svg("svg", { width: size, height: size, viewBox: "0 0 " + size + " " + size }, this.el);
    var id = "jkk" + (++uid), defs = svg("defs", {}, s);
    var rg = svg("radialGradient", { id: id, cx: "0.4", cy: "0.35", r: "0.75" }, defs);
    svg("stop", { offset: "0", "stop-color": "#3a414c" }, rg);
    svg("stop", { offset: "0.6", "stop-color": "#22272f" }, rg);
    svg("stop", { offset: "1", "stop-color": "#161a1f" }, rg);
    var f = svg("filter", { id: id + "s", x: "-30%", y: "-30%", width: "160%", height: "180%" }, defs);
    svg("feDropShadow", { dx: "0", dy: "3", stdDeviation: "3", "flood-color": "#000", "flood-opacity": "0.55" }, f);
    svg("path", { d: arc(cx, cy, r, -SWEEP, SWEEP), stroke: "#262c35", "stroke-width": 4, fill: "none", "stroke-linecap": "round" }, s);
    this.val = svg("path", { d: "", stroke: o.color || JK.knobColors.gain, "stroke-width": 4, fill: "none", "stroke-linecap": "round" }, s);
    svg("circle", { cx: cx, cy: cy, r: br, fill: "url(#" + id + ")", stroke: "#3a414c", "stroke-width": 1, filter: "url(#" + id + "s)" }, s);
    this.ptr = svg("line", { stroke: "#f4f6f9", "stroke-width": 2.5, "stroke-linecap": "round" }, s);
    this.geo = { cx: cx, cy: cy, r: r, br: br };
    this.text = el("div", "jk-kv", this.el);
    if (o.label) el("div", "jk-kl", this.el, o.label);
    this.draw(0);

    var startY = 0, startPos = 0, active = 0, lastTap = 0, moved = false;
    this.el.addEventListener("pointerdown", function (ev) {
      if (self.disabled || JK.editing) return;
      active++;
      self.el.setPointerCapture(ev.pointerId);
      startY = ev.clientY; startPos = self.pos; moved = false;
      self.el.classList.add("drag");
      var now = Date.now();
      if (now - lastTap < 320 && o.def != null) { self.turn(o.def, true); lastTap = 0; return; }
      lastTap = now;
    });
    this.el.addEventListener("pointermove", function (ev) {
      if (!active || self.disabled) return;
      var fine = ev.shiftKey || active > 1 ? 0.2 : 1;
      if (Math.abs(startY - ev.clientY) > 2) moved = true;
      self.turn(clamp(startPos + (startY - ev.clientY) / 200 * fine, 0, 1), false);
    });
    function up() {
      if (!active) return;
      active = Math.max(0, active - 1);
      if (active === 0) { self.el.classList.remove("drag"); if (moved) self.turn(self.pos, true); }
    }
    this.el.addEventListener("pointerup", up);
    this.el.addEventListener("pointercancel", up);
    var wheelT = 0;
    this.el.addEventListener("wheel", function (ev) {
      if (self.disabled || JK.editing) return;
      ev.preventDefault();
      self.turn(clamp(self.pos - ev.deltaY / (ev.shiftKey ? 4000 : 1000), 0, 1), false);
      clearTimeout(wheelT);
      wheelT = setTimeout(function () { self.turn(self.pos, true); }, 250);
    }, { passive: false });
  }
  Knob.prototype.turn = function (pos, final) { this.draw(pos); if (this.onTurn) this.onTurn(pos, final); };
  Knob.prototype.draw = function (pos) {
    this.pos = pos;
    var g = this.geo, a = -SWEEP + 2 * SWEEP * pos, from = this.o.bipolar ? 0 : -SWEEP;
    this.val.setAttribute("d", arc(g.cx, g.cy, g.r, from, a));
    var p0 = pt(g.cx, g.cy, g.br * 0.35, a), p1 = pt(g.cx, g.cy, g.br * 0.75, a);
    this.ptr.setAttribute("x1", p0[0].toFixed(2)); this.ptr.setAttribute("y1", p0[1].toFixed(2));
    this.ptr.setAttribute("x2", p1[0].toFixed(2)); this.ptr.setAttribute("y2", p1[1].toFixed(2));
  };
  Knob.prototype.set = function (pos, text) {
    if (this.el.classList.contains("drag")) return; // a finger is on it
    this.draw(pos);
    this.text.textContent = text;
  };
  Knob.prototype.show = function (text) { this.text.textContent = text; };
  Knob.prototype.setDisabled = function (d) { this.disabled = !!d; this.el.classList.toggle("off", !!d); };
  JK.Knob = Knob;

  // ----------------------------------------------------------- fader strip
  // The console strip (design/wing-polished.svg), for any mixer:
  //   new JK.Strip(parent, opts) → strip; strip.set(data); strip.fit()
  // opts:
  //   law            JK.laws.console (default) or your own (JK.law)
  //   width, height  the strip's box (from JK.stripLayout), for the
  //                  narrow / short / compact variants
  //   fader, scale, meter, readout, mute, solo (booleans, default true;
  //                  fader: false leaves a meter-only strip)
  //   gain           null, or {min, max, step, edit, title(editable)}:
  //                  a gain knob above the fader (only when tall enough)
  //   canWrite()     false → nothing can be changed (read only, editing…)
  //   onLevel(db, final), onMute(), onSolo(), onGain(db, final)
  //                  onMute / onSolo may return a promise (button waits)
  // data: {tag, name, color, stereo, db, edb, muted, emuted, mutedBy,
  //   solo, soloable, gain, l, r} (levels and meters in dB; l/r null = no
  //   meter; emuted = muted by something else, e.g. a DCA, named mutedBy).
  // Fader: drag the cap or anywhere on the track (relative, no jump);
  // Shift or a second finger = fine; double-tap = 0 dB; arrow keys step.
  // Sends at most ~7 a second while dragging and once more on release,
  // and keeps the finger's value 1.5 s so the device can catch up.
  JK.stripMinH = function (o, withGain) {
    o = o || {};
    return 64 + (withGain ? 96 : 0) + 140 + (o.readout !== false ? 30 : 0) + (o.mute !== false ? 68 : 0);
  };
  // JK.stripLayout(w, h, n, {minW = 56, maxW = 160, minH, gap = 10}) →
  // {rows, cols, shown, rowH, colW}: one row when it fits, wrapping into
  // rows when tall, never narrower than minW (the rest are "more") nor
  // wider than a console strip.
  JK.stripLayout = function (w, h, n, o) {
    o = o || {};
    var gap = o.gap == null ? 10 : o.gap, minW = o.minW || 56, maxW = o.maxW || 160, mh = o.minH || JK.stripMinH(o), L = null;
    var maxRows = Math.max(1, Math.floor((h + gap) / (mh + gap)));
    for (var r = 1; r <= maxRows && !L; r++) {
      var cols = Math.ceil(n / r);
      if ((w - gap * (cols - 1)) / cols >= minW) L = { rows: r, cols: cols, shown: n, rowH: (h - gap * (r - 1)) / r, colW: (w - gap * (cols - 1)) / cols };
    }
    if (!L) {
      var c = Math.max(1, Math.floor((w + gap) / (minW + gap)));
      L = { rows: maxRows, cols: c, shown: Math.min(n, c * maxRows), rowH: (h - gap * (maxRows - 1)) / maxRows, colW: (w - gap * (c - 1)) / c };
    }
    L.colW = Math.min(L.colW, maxW);
    L.maxW = maxW;
    return L;
  };
  JK.stripGrid = function (box, L) {
    box.style.gridTemplateColumns = "repeat(" + L.cols + ", minmax(0, " + L.maxW + "px))";
    box.style.gridTemplateRows = "repeat(" + L.rows + ", minmax(0, 1fr))";
  };

  function Strip(parent, o) {
    var self = this;
    o = this.o = o || {};
    this.law = o.law || JK.laws.console;
    this.peaks = JK.peaks();
    this.data = {};
    this.drag = null;
    var w = o.width || 120, h = o.height || 400, base = JK.stripMinH(o, false);
    var showGain = !!o.gain && h >= JK.stripMinH(o, true);
    var r = this.root = el("div", "jk-strip", parent);
    r.classList.toggle("narrow", w < 104);
    r.classList.toggle("short", !showGain);
    r.classList.toggle("compact", h < base);
    r.classList.toggle("tiny", h < base - 70);
    r.classList.toggle("nofader", o.fader === false);
    this.band = el("div", "jk-band", r);
    var hdr = el("div", "jk-shdr", r), nr = el("div", "jk-numrow", hdr);
    this.num = el("div", "jk-num", nr);
    this.st = el("span", "jk-st", nr, "ST");
    this.nm = el("div", "jk-nm", hdr);
    this.gainbox = el("div", "jk-gainbox", r);
    if (showGain) {
      this.knob = new Knob(this.gainbox, { size: 50, color: o.gain.color || JK.knobColors.gain, label: o.gain.label || "Gain" });
      this.knob.onTurn = function (pos, final) { self._gain(pos, final); };
      this.nopre = el("div", "jk-nopre", this.gainbox, o.gain.none || "NO PREAMP");
    }
    var fz = this.fz = el("div", "jk-fz", r);
    this.ticks = [];
    if (o.scale !== false) {
      var sc = el("div", "jk-scale", fz);
      this.law.ticks.forEach(function (db) {
        var t = el("div", "jk-tick" + (db === 0 ? " z" : ""), sc);
        el("b", "", t, db <= -144 ? "−∞" : db > 0 ? "+" + db : db < 0 ? "−" + (-db) : "0");
        el("i", "", t);
        t.dataset.db = db;
        self.ticks.push(t);
      });
    }
    el("div", "jk-rail", fz);
    this.hit = el("div", "jk-hit jk-live", fz);
    this.hit.tabIndex = 0;
    this.hit.setAttribute("role", "slider");
    this.cap = el("div", "jk-cap", fz);
    this.meter = el("canvas", "jk-meter", fz);
    this.lr = el("div", "jk-lr", fz);
    el("span", "", this.lr, "L"); el("span", "", this.lr, "R");
    if (o.meter === false) this.meter.style.display = this.lr.style.display = "none";
    this.pill = el("div", "jk-pill", r);
    if (o.readout === false) this.pill.style.display = "none";
    var btns = el("div", "jk-btns", r);
    if (o.mute === false) btns.style.display = "none";
    this.mute = JK.button({ label: "MUTE", onClick: function () { return self.o.onMute && self.can() ? self.o.onMute() : null; } });
    this.mute.classList.add("jk-mute");
    btns.appendChild(this.mute);
    this.solo = JK.button({ label: "S", title: "Solo", onClick: function () { return self.o.onSolo && self.can() ? self.o.onSolo() : null; } });
    this.solo.classList.add("jk-solo");
    if (o.solo === false || w < 96) this.solo.style.display = "none";
    btns.appendChild(this.solo);
    this._fader();
    this.fit();
  }
  Strip.prototype.can = function () { return !JK.editing && (!this.o.canWrite || this.o.canWrite()); };
  // fit re-measures the track (after a resize) and places the scale.
  Strip.prototype.fit = function () { this._measure(); this.paint(); };
  Strip.prototype._measure = function () {
    var h = this.fz.clientHeight, law = this.law;
    if (h === this.trackH) return;
    this.trackH = h;
    this.ticks.forEach(function (t) { t.style.top = ((1 - law.toPos(Number(t.dataset.db))) * h) + "px"; });
  };
  Strip.prototype._gain = function (pos, final) {
    var g = this.o.gain, step = g.step || 0.5;
    var db = Math.round((g.min + (g.max - g.min) * pos) / step) * step;
    this.knob.show(JK.dbText(db, "dB"));
    if (!this.can() || !g.edit || !this.o.onGain) return;
    var now = Date.now();
    if (!final && now - (this._gainAt || 0) < 250) return;
    this._gainAt = now;
    this.o.onGain(db, final);
  };
  Strip.prototype._fader = function () {
    var self = this, hit = this.hit, law = this.law;
    var lastSent = 0, pendingT = 0, startY = 0, startPos = 0, pointers = 0, lastTap = 0, moved = false;
    function send(final) {
      clearTimeout(pendingT);
      var d = self.drag;
      if (!d || !self.o.onLevel) return;
      var now = Date.now();
      if (!final && now - lastSent < 140) { pendingT = setTimeout(function () { send(false); }, 140 - (now - lastSent)); return; }
      lastSent = now;
      self.o.onLevel(law.toDb(d.pos), final);
    }
    function current() { var s = self.data; return self.drag ? self.drag.pos : law.toPos(s.db == null ? -144 : s.db); }
    hit.addEventListener("pointerdown", function (ev) {
      if (!self.can()) return;
      pointers++;
      hit.setPointerCapture(ev.pointerId);
      startY = ev.clientY;
      startPos = current();
      moved = false;
      var now = Date.now();
      if (now - lastTap < 320) { // double-tap: 0 dB
        lastTap = 0;
        self.drag = { pos: law.toPos(0), active: false, until: now + 1500 };
        self.paint();
        send(true);
        return;
      }
      lastTap = now;
      self.drag = { pos: startPos, active: true, until: 0 };
    });
    hit.addEventListener("pointermove", function (ev) {
      var d = self.drag;
      if (!d || !d.active) return;
      var fine = ev.shiftKey || pointers > 1 ? 0.2 : 1;
      if (Math.abs(startY - ev.clientY) > 2) moved = true;
      d.pos = clamp(startPos + (startY - ev.clientY) / Math.max(40, self.trackH || 200) * fine, 0, 1);
      self.paint();
      send(false);
    });
    function up() {
      pointers = Math.max(0, pointers - 1);
      var d = self.drag;
      if (!d || !d.active || pointers > 0) return;
      d.active = false;
      d.until = Date.now() + 1500; // keep the finger's value until the device catches up
      if (moved) send(true);
    }
    hit.addEventListener("pointerup", up);
    hit.addEventListener("pointercancel", up);
    hit.addEventListener("keydown", function (e) {
      var step = e.key === "ArrowUp" ? 1 : e.key === "ArrowDown" ? -1 : 0;
      if (!step || !self.can()) return;
      e.preventDefault();
      self.drag = { pos: clamp(current() + step * (e.shiftKey ? 0.005 : 0.02), 0, 1), active: false, until: Date.now() + 1500 };
      self.paint();
      send(true);
    });
  };
  // set takes the device's values; paint draws them (cheap: call it per frame).
  Strip.prototype.set = function (s) { this.data = s || {}; this.paint(); };
  Strip.prototype.paint = function () {
    var s = this.data, o = this.o, law = this.law, now = Date.now(), off = !this.can();
    this._measure(); // the box may have settled since it was built
    this.band.hidden = !s.color;
    if (s.color) this.band.style.background = s.color;
    this.num.textContent = s.tag || "";
    this.st.style.display = s.stereo ? "" : "none";
    this.nm.textContent = s.name || "";
    this.root.title = (s.tag || "") + (s.stereo ? " (stereo)" : "") + (s.name ? " · " + s.name : "");
    this.hit.setAttribute("aria-label", (s.name || s.tag || "") + " level");
    this.root.classList.toggle("muted", !!(s.emuted || s.muted));
    this.root.classList.toggle("off", off);
    var d = this.drag;
    if (d && !d.active && now > d.until) { this.drag = d = null; }
    var pos = d ? d.pos : law.toPos(s.db == null ? -144 : s.db);
    this.cap.style.transform = "translateY(" + ((1 - pos) * (this.trackH || 0)).toFixed(1) + "px)";
    var db = d ? law.toDb(pos) : s.db;
    // a narrow strip's pill has room for the number only ("−10.0")
    var full = db == null ? "–" : JK.dbText(db, db <= -143.9 ? "" : "dB");
    this.pill.textContent = db == null ? "–" : JK.dbText(db, db <= -143.9 || this.root.classList.contains("narrow") ? "" : "dB");
    this.pill.classList.toggle("live", !!d);
    this.pill.title = s.edb != null && s.db != null && Math.abs(s.edb - s.db) > 0.05 ? "After DCA: " + JK.dbText(s.edb, "dB") : "";
    this.hit.setAttribute("aria-valuetext", full);
    var by = (s.emuted && !s.muted);
    this.mute.set({ tone: s.muted ? "pgm" : "", disabled: off,
      label: s.muted ? "MUTED" : by ? "MUTED BY " + String(s.mutedBy || "GROUP").toUpperCase() : "MUTE",
      title: by ? "Muted by " + (s.mutedBy || "a DCA or mute group") + ". Its own mute is off." : s.muted ? "Tap to unmute" : "Tap to mute" });
    this.mute.classList.toggle("by", !!by);
    this.mute.setAttribute("aria-pressed", s.muted ? "true" : "false");
    this.solo.set({ active: !!s.solo, disabled: off || s.soloable === false });
    if (this.knob) {
      var has = s.gain != null, g = o.gain;
      this.knob.el.style.display = has ? "" : "none";
      this.nopre.style.display = has ? "none" : "";
      if (has) this.knob.set((s.gain - g.min) / (g.max - g.min), JK.dbText(s.gain, "dB"));
      this.knob.setDisabled(off || !g.edit);
      this.knob.el.title = g.title ? g.title(!!g.edit) : g.edit ? "Drag to change the gain" : "Gain";
    }
    if (o.meter !== false) {
      this.meter.classList.toggle("st", !!s.stereo);
      this.lr.style.display = s.stereo ? "" : "none";
      var cols = [{ level: s.l, peak: this.peaks("L", s.l) }];
      if (s.stereo) cols.push({ level: s.r, peak: this.peaks("R", s.r) });
      JK.led(this.meter, cols, { law: law });
    }
  };
  JK.Strip = Strip;
})();
