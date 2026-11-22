import { test, type Frame, type Page } from "@playwright/test";
import fs from "node:fs";
import path from "node:path";
import { api, closeBrowsers, openDevice, type Device } from "./lib";

// Every widget, alone on a board, at the sizes people stretch them to:
// its minimum, minimum width × very tall, full width × minimum height,
// the whole board, and a middle size, on a phone, an iPad and a desktop.
// Inside the widget box (and inside add-on frames) nothing may scroll,
// no text under 10px, no text running out of its box, no two controls on
// top of each other, no control under 28×28 on touch screens, nothing
// outside the box and no control cut off.
//
// SIZES_ONLY=<regex> limits it to matching variants (e.g. "wing|vmix").
// Output: e2e/out/sizes/<screen>/<variant>/<case>.png and
// e2e/out/sizes/report.json (merged from findings/<variant>.json).

const OUT = path.resolve(import.meta.dirname, "out", "sizes");

type Screen = { key: "phone" | "tablet" | "desktop"; dev: Device; cols: number };
const SCREENS: Screen[] = [
  { key: "phone", cols: 4, dev: { name: "Phone 390×844", engine: "webkit", viewport: { width: 390, height: 844 }, touch: true, scale: 2,
    ua: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1" } },
  { key: "tablet", cols: 12, dev: { name: "iPad 1180×820", engine: "webkit", viewport: { width: 1180, height: 820 }, touch: true, scale: 2,
    ua: "Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1" } },
  { key: "desktop", cols: 12, dev: { name: "Desktop 1920×1080", engine: "chromium", viewport: { width: 1920, height: 1080 }, touch: false } },
];

// Grid cells asked for; the board grows anything below the widget's minimum
// (and anything shorter than its content) to fit, so 1×1 means "its minimum".
const CASES = (cols: number) => [
  { key: "min", w: 1, h: 1 },
  { key: "minw-tall", w: 1, h: 24 },
  { key: "wide-short", w: cols, h: 1 },
  { key: "full", w: cols, h: 20 },
  { key: "mid", w: Math.ceil(cols / 2), h: 8 },
];

type Variant = { name: string; type: string; settings: Record<string, unknown> };
type Finding = { kind: string; where: "core" | "frame"; detail: string };
type Case = { variant: string; type: string; screen: string; size: string; cells: string; box: string; shot: string; findings: Finding[] };

const LONG = "Fade the screen to black and bring the logo up slowly";

async function builtins(): Promise<Variant[]> {
  const cams = await api<{ id: string }[]>("GET", "/api/cameras");
  const cam = cams[0]?.id ?? "";
  const presets = await api<{ id: number; name: string }[]>("GET", "/api/presets");
  const long = presets.reduce((a, p) => (p.name.length > a.name.length ? p : a), presets[0]);
  const macros = await api<{ id: number; name: string }[]>("GET", "/api/macros");
  const v = (name: string, type: string, settings: Record<string, unknown> = {}): Variant => ({ name, type, settings });
  return [
    v("cue-incoming", "stage.cue", { channels: ["pulpit"], incoming: true, outgoing: false }),
    v("cue-outgoing", "stage.cue", { channels: ["pulpit", "worship"], incoming: false, outgoing: true, textBox: true,
      quick: ["Wrap up", "Speak up, please", "Five more minutes then the closing song", "Mic is off"], timers: ["Sermon 30", "Announcements 5"] }),
    v("cue-both", "stage.cue", { channels: ["pulpit"], incoming: true, outgoing: true, textBox: true, quick: ["Wrap up", "Louder"] }),
    v("send-legacy", "stage.send", { channels: ["pulpit"], textBox: true }),
    v("show-legacy", "stage.show", { channel: "pulpit", style: "big" }),
    v("timer", "stage.timer", { channel: "pulpit" }),
    v("timer-controls", "stage.timer", { channel: "pulpit", controls: true }),
    v("live", "stage.live", {}),
    v("live-stream", "stage.live", { mode: "stream" }),
    v("tally", "stage.tally", { camera: cam }),
    v("video-cam", "video", { feed: `cam:${cam}`, tally: true, switchHere: true }),
    v("video-program", "video", { feed: "program" }),
    v("camera-live-legacy", "camera.live", { camera: cam, quality: "sd" }),
    v("move-switchable", "camera.move", { controls: "switchable", opensIn: "buttons", home: true, zoom: true, speed: true }),
    v("move-joystick", "camera.move", { controls: "joystick", home: true, zoom: true, speed: true }),
    v("move-bare", "camera.move", { controls: "buttons", home: false, zoom: false, speed: false }),
    v("preset-long", "camera.preset", { preset: long?.id, camera: cam }),
    v("preset-label", "camera.preset", { preset: presets[0]?.id, label: "Main" }),
    v("presets", "camera.presets", { edit: true }),
    v("presets-noedit", "camera.presets", { edit: false }),
    v("picture", "camera.picture", {}),
    v("home", "camera.home", {}),
    v("stop", "camera.stop", {}),
    v("zoom", "camera.zoom", {}),
    v("speed", "camera.speed", {}),
    v("program-legacy", "switcher.program", {}),
    v("confidence-legacy", "switcher.confidence", {}),
    v("inputs", "switcher.inputs", { connection: "vmix-simulated" }),
    v("cutfade", "switcher.cutfade", { connection: "vmix-simulated", fadeMs: 1000 }),
    v("prevnext", "slides.prevnext", { connection: "proclaim-simulated" }),
    v("slide-button-long", "slides.button", { connection: "proclaim-simulated", action: "black", label: LONG }),
    v("favorites", "audio.favorites", { connection: "wing-simulated" }),
    v("mute", "audio.mute", { connection: "wing-simulated", slot: "2", showChannel: true }),
    v("macro-button-long", "macros.button", { macro: macros[0]?.id }),
    v("macro-list", "macros.list", {}),
    v("bandwidth", "metrics.bandwidth", { viewers: true, senders: true }),
    v("health", "metrics.health", { problems: true }),
    v("clock", "other.clock", {}),
    v("clock-full", "other.clock", { seconds: true, date: true }),
    v("countdown", "other.countdown", { target: "11:00", label: "Service starts in" }),
    v("text-xl-long", "other.text", { text: "Welcome! Please silence your phones and find a seat near the front", size: "xl" }),
    v("text-sm", "other.text", { text: "Hi", size: "sm" }),
    v("schedule", "other.schedule", { recent: true }),
    v("log", "other.log", {}),
  ];
}

type Manifest = { id: string; widgets?: { kind: string; type: string }[] };

async function addonVariants(): Promise<Variant[]> {
  const { addons } = await api<{ addons: { info: { enabled: boolean; manifest: Manifest } }[] }>("GET", "/api/addons");
  const { connections } = await api<{ connections: { id: string; addon: string }[] }>("GET", "/api/connections");
  const opts = (addon: string, name: string, conn: string) => api<{ value: string }[]>("GET", `/api/connections/options/${addon}/${name}?conn=${conn}`).catch(() => []);
  const out: Variant[] = [];
  for (const a of addons) {
    const m = a.info.manifest;
    const conn = connections.find((c) => c.addon === m.id)?.id;
    if (!conn || !a.info.enabled) continue;
    const base = (w: string, extra: Record<string, unknown> = {}) => ({ addon: m.id, widget: w, connection: conn, ...extra });
    for (const w of m.widgets ?? []) {
      if (w.kind === "core") continue;
      const t = `addon:${m.id}:${w.type}`;
      const short = m.id === "behringer-wing" ? "wing" : m.id;
      out.push({ name: `${short}-${w.type}`, type: t, settings: base(w.type) });
      // fewest and most of what it lists
      const key = `${m.id}:${w.type}`;
      if (key === "behringer-wing:faders" || key === "behringer-wing:meter_bridge" || key === "behringer-wing:mute_grid") {
        const all = (await opts(m.id, "wing.faders", conn)).map((o) => o.value).filter((v) => v.startsWith("ch:"));
        out.push({ name: `${short}-${w.type}-1`, type: t, settings: base(w.type, { strips: all.slice(0, 1) }) });
        out.push({ name: `${short}-${w.type}-16`, type: t, settings: base(w.type, { strips: all.slice(0, 16) }) });
      }
      if (key === "vmix:audio_mixer") {
        const all = (await opts(m.id, "vmix.audio", conn)).map((o) => o.value);
        out.push({ name: `${short}-${w.type}-1`, type: t, settings: base(w.type, { strips: all.slice(0, 1) }) });
        out.push({ name: `${short}-${w.type}-all`, type: t, settings: base(w.type, { strips: all, solo: true }) });
      }
      if (key === "vmix:input_grid" || key === "vmix:input_buttons") {
        const all = (await opts(m.id, "vmix.inputs", conn)).map((o) => o.value);
        out.push({ name: `${short}-${w.type}-1`, type: t, settings: base(w.type, { inputs: all.slice(0, 1) }) });
      }
      if (key === "vmix:button_pad") {
        const all = (await opts(m.id, "vmix.padActions", conn)).map((o) => o.value);
        out.push({ name: `${short}-${w.type}-1`, type: t, settings: base(w.type, { actions: all.slice(0, 1) }) });
        out.push({ name: `${short}-${w.type}-16-long`, type: t, settings: base(w.type, { actions: all.slice(0, 16),
          labels: Array.from({ length: 16 }, (_, i) => (i % 3 === 0 ? `${LONG} ${i + 1}` : "")).join("\n") }) });
      }
      if (key === "proclaim:button_pad") {
        const all = (await opts(m.id, "proclaim.actions", conn)).map((o) => o.value);
        out.push({ name: `${short}-${w.type}-1`, type: t, settings: base(w.type, { pad: all.slice(0, 1) }) });
        out.push({ name: `${short}-${w.type}-12-long`, type: t, settings: base(w.type, { pad: all.slice(0, 12), padLabels: `Next slide = ${LONG}` }) });
      }
    }
  }
  return out;
}

// --------------------------------------------------------------- checks

// Runs in the page (root = the widget box) and in each add-on frame
// (root = the frame's <html>). Self-contained: no closures.
const inspect = (arg: { frame: boolean; touch: boolean }) => {
  const out: { kind: string; detail: string }[] = [];
  const root: Element = arg.frame ? document.documentElement : (document.querySelector("[data-wid]") as Element);
  if (!root) return [{ kind: "missing", detail: "no widget box" }];
  const R = arg.frame ? { left: 0, top: 0, right: innerWidth, bottom: innerHeight } : root.getBoundingClientRect();
  const name = (el: Element) => {
    const t = (el.textContent || "").trim().replace(/\s+/g, " ").slice(0, 32);
    const cls = typeof (el as HTMLElement).className === "string" ? (el as HTMLElement).className.split(/\s+/).slice(0, 2).join(".") : "";
    return `${el.tagName.toLowerCase()}${cls ? "." + cls : ""}${t ? ` “${t}”` : ""}`;
  };
  const shown = (el: Element) => {
    const s = getComputedStyle(el);
    if (s.display === "none" || s.visibility === "hidden" || s.visibility === "collapse") return false;
    let p: Element | null = el;
    while (p) {
      if (getComputedStyle(p).opacity === "0") return false;
      if (p === root) break;
      p = p.parentElement;
    }
    const r = el.getBoundingClientRect();
    return r.width > 1 && r.height > 1;
  };
  const all = [root, ...root.querySelectorAll("*")].filter((el) => !(el instanceof SVGElement && !(el instanceof SVGSVGElement)));
  const vis = all.filter(shown);
  const ownText = (el: Element) => [...el.childNodes].some((n) => n.nodeType === 3 && (n.textContent || "").trim().length > 0);

  // the frame's page itself scrolling
  if (arg.frame) {
    const se = document.scrollingElement || document.documentElement;
    const hs = getComputedStyle(document.documentElement), bs = getComputedStyle(document.body);
    const hidden = (s: CSSStyleDeclaration, ax: "overflowX" | "overflowY") => /hidden|clip/.test(s[ax]);
    if (se.scrollHeight > innerHeight + 1 && !hidden(hs, "overflowY") && !hidden(bs, "overflowY")) out.push({ kind: "scrollbar", detail: `frame page scrolls: ${se.scrollHeight}px tall in ${innerHeight}px` });
    if (se.scrollWidth > innerWidth + 1 && !hidden(hs, "overflowX") && !hidden(bs, "overflowX")) out.push({ kind: "scrollbar", detail: `frame page scrolls sideways: ${se.scrollWidth}px in ${innerWidth}px` });
  }
  for (const el of vis) {
    const s = getComputedStyle(el);
    const r = el.getBoundingClientRect();
    // scrollbars
    if (el !== document.documentElement && el !== document.body) {
      if (/(auto|scroll)/.test(s.overflowY) && el.scrollHeight > el.clientHeight + 1 && el.clientHeight > 0) out.push({ kind: "scrollbar", detail: `${name(el)}: ${el.scrollHeight}px of content in ${el.clientHeight}px` });
      if (/(auto|scroll)/.test(s.overflowX) && el.scrollWidth > el.clientWidth + 1 && el.clientWidth > 0) out.push({ kind: "scrollbar", detail: `${name(el)} sideways: ${el.scrollWidth}px in ${el.clientWidth}px` });
    }
    if (ownText(el)) {
      const fs = parseFloat(s.fontSize);
      if (fs < 10) out.push({ kind: "tiny text", detail: `${name(el)} at ${fs}px` });
      // text running out of its box (inline boxes have no scroll size)
      if (s.display !== "inline" && el.clientWidth > 0) {
        const over = el.scrollWidth > el.clientWidth + 1;
        const clipX = /hidden|clip/.test(s.overflowX);
        if (over && !clipX) out.push({ kind: "text overflow", detail: `${name(el)}: ${el.scrollWidth}px of text in ${el.clientWidth}px` });
        if (over && clipX && s.textOverflow !== "ellipsis") out.push({ kind: "text cut", detail: `${name(el)}: ${el.scrollWidth}px of text in ${el.clientWidth}px, no …` });
      }
    }
    // outside the widget box, or cut off by something inside it
    const outBy = Math.max(R.left - r.left, r.right - R.right, R.top - r.top, r.bottom - R.bottom);
    if (outBy > 1 && el !== root) {
      // clipped on the way up?
      let p = el.parentElement, clipper: Element | null = null;
      while (p && p !== root && p !== document.body) {
        const ps = getComputedStyle(p);
        if (/hidden|clip|auto|scroll/.test(ps.overflowX + ps.overflowY)) { clipper = p; break; }
        p = p.parentElement;
      }
      if (!clipper) {
        if (!arg.frame) out.push({ kind: "outside box", detail: `${name(el)} by ${Math.round(outBy)}px` });
        else if (el.matches("button,input,select,textarea,[role=button],[role=slider],a[href]") || ownText(el)) out.push({ kind: "outside box", detail: `${name(el)} by ${Math.round(outBy)}px (frame edge)` });
      }
    }
  }
  // controls
  const ctl = vis.filter((el) => el.matches("button,input:not([type=hidden]),select,textarea,[role=button],[role=slider],[role=switch],a[href]"))
    .filter((el) => getComputedStyle(el).pointerEvents !== "none");
  // a control inside another counts once (the outer one)
  const top = ctl.filter((el) => !ctl.some((o) => o !== el && o.contains(el)));
  for (const el of top) {
    const r = el.getBoundingClientRect();
    // cut off by a clipping parent
    let p = el.parentElement;
    while (p && p !== document.body) {
      const ps = getComputedStyle(p);
      if (/hidden|clip/.test(ps.overflowX + ps.overflowY)) {
        const q = p.getBoundingClientRect();
        const cut = Math.max(q.left - r.left, r.right - q.right, q.top - r.top, r.bottom - q.bottom);
        if (cut > 2) { out.push({ kind: "control cut off", detail: `${name(el)} by ${Math.round(cut)}px` }); break; }
      }
      if (p === root) break;
      p = p.parentElement;
    }
    if (arg.touch && (r.width < 28 || r.height < 28)) out.push({ kind: "small tap target", detail: `${name(el)} is ${Math.round(r.width)}×${Math.round(r.height)}px` });
  }
  for (let i = 0; i < top.length; i++) for (let j = i + 1; j < top.length; j++) {
    const a = top[i].getBoundingClientRect(), b = top[j].getBoundingClientRect();
    const x = Math.min(a.right, b.right) - Math.max(a.left, b.left), y = Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top);
    if (x > 2 && y > 2) out.push({ kind: "controls overlap", detail: `${name(top[i])} and ${name(top[j])} (${Math.round(x)}×${Math.round(y)}px)` });
  }
  return [...new Map(out.map((o) => [o.kind + o.detail, o])).values()];
};

async function settle(page: Page) {
  await page.waitForSelector("[data-wid]", { timeout: 15000 });
  // add-on state loads, then frames boot
  await page.waitForFunction(() => !document.querySelector("[data-wid]")?.textContent?.includes("Loading…"), null, { timeout: 8000 }).catch(() => {});
  await page.waitForTimeout(900);
  const frames = await page.$$("[data-wid] iframe");
  for (const f of frames) {
    const fr = await f.contentFrame();
    if (!fr) continue;
    await fr.waitForFunction(() => document.body && document.body.children.length > 0 && document.body.getBoundingClientRect().height > 0, null, { timeout: 8000 }).catch(() => {});
  }
  if (frames.length) await page.waitForTimeout(900);
}

async function frameFindings(page: Page, touch: boolean): Promise<Finding[]> {
  const out: Finding[] = [];
  const frames = await page.$$("[data-wid] iframe");
  for (const f of frames) {
    const fr: Frame | null = await f.contentFrame();
    if (!fr) continue;
    const found = await fr.evaluate(inspect, { frame: true, touch }).catch((e) => [{ kind: "frame unreadable", detail: String(e).slice(0, 120) }]);
    for (const x of found) out.push({ ...x, where: "frame" });
  }
  return out;
}

// ------------------------------------------------------------------ run

const ONLY = process.env.SIZES_ONLY ? new RegExp(process.env.SIZES_ONLY) : null;

test.describe.configure({ mode: "parallel" });

// The variant list comes from the running jadeck, so tests are made per
// slot and each picks its variants by index (Playwright needs the test
// list before any request can be made).
const SLOTS = Number(process.env.SIZES_SLOTS ?? 8);

for (let slot = 0; slot < SLOTS; slot++) {
  test(`widget sizes · part ${slot + 1} of ${SLOTS}`, async () => {
    test.setTimeout(90 * 60_000);
    const variants = [...(await builtins()), ...(await addonVariants())].filter((v) => !ONLY || ONLY.test(v.name) || ONLY.test(v.type));
    const mine = variants.filter((_, i) => i % SLOTS === slot);
    fs.mkdirSync(path.join(OUT, "findings"), { recursive: true });
    for (const scr of SCREENS) {
      const created = await api<{ id: number }>("POST", "/api/dashboards", { name: `Sizes ${scr.key} ${slot}`, kind: scr.key, widgets: [], layout: [] });
      const { ctx, page } = await openDevice(scr.dev, { screen: `Sizes ${slot}`, dashboard: created.id });
      try {
        for (const v of mine) {
          const cases: Case[] = [];
          const file = path.join(OUT, "findings", `${scr.key}--${v.name}.json`);
          for (const c of CASES(scr.cols)) {
            await api("PUT", `/api/dashboards/${created.id}`, { id: created.id, name: `Sizes ${scr.key} ${slot}`, kind: scr.key,
              widgets: [{ id: "w1", type: v.type, settings: v.settings }], layout: [{ i: "w1", x: 0, y: 0, w: c.w, h: c.h }] });
            await page.reload();
            await settle(page);
            const box = page.locator("[data-wid]").first();
            const bb = await box.boundingBox();
            const core = (await page.evaluate(inspect, { frame: false, touch: scr.dev.touch })).map((x) => ({ ...x, where: "core" as const }));
            const fr = await frameFindings(page, scr.dev.touch);
            const shotRel = path.join(scr.key, v.name, `${c.key}.png`);
            fs.mkdirSync(path.dirname(path.join(OUT, shotRel)), { recursive: true });
            await box.screenshot({ path: path.join(OUT, shotRel), timeout: 15000 }).catch(() => {});
            cases.push({ variant: v.name, type: v.type, screen: scr.key, size: c.key, cells: `${c.w}×${c.h}`,
              box: bb ? `${Math.round(bb.width)}×${Math.round(bb.height)}` : "?", shot: shotRel, findings: [...core, ...fr] });
          }
          fs.writeFileSync(file, JSON.stringify(cases, null, 2));
          const n = cases.reduce((a, c) => a + c.findings.length, 0);
          console.log(`${scr.key} · ${v.name}: ${n} findings`);
        }
      } finally {
        await ctx.close();
        await api("DELETE", `/api/dashboards/${created.id}`).catch(() => {});
      }
    }
    await closeBrowsers();
    merge();
  });
}

/** report.json from every findings file there (the last part to finish has them all). */
function merge() {
  const dir = path.join(OUT, "findings");
  const cases: Case[] = fs.readdirSync(dir).filter((f) => f.endsWith(".json")).flatMap((f) => JSON.parse(fs.readFileSync(path.join(dir, f), "utf8")));
  const byVariant: Record<string, { core: number; frame: number; kinds: Record<string, number> }> = {};
  for (const c of cases) {
    const b = (byVariant[c.variant] ??= { core: 0, frame: 0, kinds: {} });
    for (const f of c.findings) {
      b[f.where]++;
      b.kinds[f.kind] = (b.kinds[f.kind] ?? 0) + 1;
    }
  }
  const total = { cases: cases.length, core: 0, frame: 0 };
  for (const b of Object.values(byVariant)) { total.core += b.core; total.frame += b.frame; }
  fs.writeFileSync(path.join(OUT, "report.json"), JSON.stringify({ total, byVariant, cases: cases.filter((c) => c.findings.length) }, null, 2));
}
