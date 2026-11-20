import { chromium, webkit, devices, type Browser, type BrowserContext, type Page } from "@playwright/test";
import fs from "node:fs";
import path from "node:path";

export const BASE = process.env.JADECK_URL ?? "http://127.0.0.1:8080";
export const SIM_VMIX = process.env.SIM_VMIX ?? "http://127.0.0.1:8088";
export const REPORT = path.resolve(import.meta.dirname, "report");

export type Device = { name: string; engine: "webkit" | "chromium"; viewport: { width: number; height: number }; touch: boolean; ua?: string; scale?: number };

// The screens people actually hold. iPhone / iPad run in WebKit (Safari's engine).
export const DEVICES: Record<string, Device> = {
  "iphone-se": { name: "iPhone SE", engine: "webkit", viewport: { width: 375, height: 667 }, touch: true, ua: devices["iPhone SE"].userAgent, scale: 2 },
  "iphone": { name: "iPhone 15", engine: "webkit", viewport: { width: 393, height: 852 }, touch: true, ua: devices["iPhone 15"].userAgent, scale: 3 },
  "iphone-side": { name: "iPhone 15 sideways", engine: "webkit", viewport: { width: 852, height: 393 }, touch: true, ua: devices["iPhone 15"].userAgent, scale: 3 },
  "ipad": { name: "iPad (portrait)", engine: "webkit", viewport: { width: 820, height: 1180 }, touch: true, ua: devices["iPad (gen 7)"].userAgent, scale: 2 },
  "ipad-side": { name: "iPad (landscape)", engine: "webkit", viewport: { width: 1180, height: 820 }, touch: true, ua: devices["iPad (gen 7)"].userAgent, scale: 2 },
  "laptop": { name: "Laptop 1280×720", engine: "chromium", viewport: { width: 1280, height: 720 }, touch: false },
  "desktop": { name: "Desktop 1920×1080", engine: "chromium", viewport: { width: 1920, height: 1080 }, touch: false },
};

const browsers: Partial<Record<Device["engine"], Browser>> = {};
export async function browserFor(d: Device) {
  return (browsers[d.engine] ??= await (d.engine === "webkit" ? webkit : chromium).launch());
}
export async function closeBrowsers() {
  for (const b of Object.values(browsers)) await b?.close();
  for (const k of Object.keys(browsers)) delete browsers[k as Device["engine"]];
}

/** A device with its own storage: screen name and the dashboard it shows. */
export async function openDevice(d: Device, opts: { screen?: string; dashboard?: number; path?: string } = {}): Promise<{ ctx: BrowserContext; page: Page; errors: string[] }> {
  const b = await browserFor(d);
  const ctx = await b.newContext({ viewport: d.viewport, hasTouch: d.touch, isMobile: d.touch && d.viewport.width < 900, userAgent: d.ua, deviceScaleFactor: d.scale ?? 1 });
  await ctx.addInitScript(({ screen, dashboard }) => {
    try {
      if (screen) localStorage.setItem("ptz.screen", screen);
      if (dashboard) localStorage.setItem("ptz.dashboard", String(dashboard));
    } catch { /* ignore */ }
  }, { screen: opts.screen, dashboard: opts.dashboard });
  const page = await ctx.newPage();
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(`page error: ${e.message}`));
  page.on("console", (m) => {
    if (m.type() !== "error") return;
    const t = m.text();
    // video players retrying while a feed starts are expected in the simulator
    if (/WebRTC|whep|hls|ERR_CONNECTION|Failed to load resource|net::/i.test(t)) return;
    errors.push(`console: ${t}`);
  });
  await page.goto(BASE + (opts.path ?? "/"));
  return { ctx, page, errors };
}

export async function api<T = unknown>(method: string, p: string, body?: unknown): Promise<T> {
  const r = await fetch(BASE + p, { method, headers: body ? { "Content-Type": "application/json" } : {}, body: body ? JSON.stringify(body) : undefined });
  const t = await r.text();
  if (!r.ok) throw new Error(`${method} ${p}: ${r.status} ${t}`);
  return (t ? JSON.parse(t) : null) as T;
}

export async function dashboardId(name: string) {
  const list = await api<{ id: number; name: string }[]>("GET", "/api/dashboards");
  const d = list.find((x) => x.name === name);
  if (!d) throw new Error(`no dashboard named ${name}`);
  return d.id;
}

export async function shot(page: Page, file: string) {
  const p = path.join(REPORT, file);
  fs.mkdirSync(path.dirname(p), { recursive: true });
  // full-page captures draw fixed things (the phone's bottom tab) mid-page
  await page.addStyleTag({ content: "[data-phone-nav]{display:none!important}" }).catch(() => {});
  await page.screenshot({ path: p, fullPage: true });
  return file;
}

export function writeJSON(file: string, data: unknown) {
  const p = path.join(REPORT, file);
  fs.mkdirSync(path.dirname(p), { recursive: true });
  fs.writeFileSync(p, JSON.stringify(data, null, 2));
}
