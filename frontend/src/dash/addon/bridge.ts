// The postMessage bridge between the dashboard and a frame widget (an add-on
// page in <iframe sandbox="allow-scripts">). design/WIDGETS-V2.md.
//
// host → frame: {t:"init", v:1, state, settings, theme, size, editing, touch}
//               (sent again whenever the frame says ready, and when editing
//               or the widget's settings change), {t:"state", state},
//               {t:"size", size}, {t:"theme", theme},
//               {t:"result", id, ok, error?, result?, blob?}
// frame → host: {t:"action", id, name, params}, {t:"resource", id, path},
//               {t:"log", text}, {t:"ready"}
//
// Pure helpers here (no React) so the checks are easy to read and reuse.

export const BRIDGE_VERSION = 1;
export const MAX_ACTIONS_PER_SEC = 10;
export const MAX_RESOURCES_PER_SEC = 30;
export const MAX_LOGS_PER_SEC = 5;
export const MAX_RESOURCE_BYTES = 4 << 20;
const MAX_PARAMS_BYTES = 64 << 10;

export type ReqId = string | number;
export type FromFrame =
  | { t: "ready" }
  | { t: "log"; text: string }
  | { t: "action"; id: ReqId; name: string; params: Record<string, unknown> }
  | { t: "resource"; id: ReqId; path: string };

const isId = (v: unknown): v is ReqId => (typeof v === "string" && v.length > 0 && v.length <= 64) || (typeof v === "number" && Number.isFinite(v));
const plain = (v: unknown): v is Record<string, unknown> => !!v && typeof v === "object" && !Array.isArray(v) && Object.getPrototypeOf(v) === Object.prototype;

/** Validates the shape of a message from a frame. Returns null (ignore it)
 *  when it isn't a bridge message; an error string when it is one but bad. */
export function parseFromFrame(data: unknown): FromFrame | { bad: string; id?: ReqId } | null {
  if (!plain(data) || typeof data.t !== "string") return null;
  switch (data.t) {
    case "ready":
      return { t: "ready" };
    case "log":
      return typeof data.text === "string" ? { t: "log", text: data.text.slice(0, 300) } : { bad: "log needs text" };
    case "action": {
      if (!isId(data.id)) return { bad: "action needs an id" };
      if (typeof data.name !== "string" || !data.name) return { bad: "action needs a name", id: data.id };
      const params = data.params === undefined ? {} : data.params;
      if (!plain(params)) return { bad: "params must be an object", id: data.id };
      let size = 0;
      try { size = JSON.stringify(params).length; } catch { return { bad: "params must be plain JSON", id: data.id }; }
      if (size > MAX_PARAMS_BYTES) return { bad: "params too big", id: data.id };
      return { t: "action", id: data.id, name: data.name, params };
    }
    case "resource":
      if (!isId(data.id)) return { bad: "resource needs an id" };
      if (typeof data.path !== "string" || !data.path) return { bad: "resource needs a path", id: data.id };
      return { t: "resource", id: data.id, path: data.path };
    default:
      return null;
  }
}

/** Sliding one-second window. */
export class RateLimit {
  private times: number[] = [];
  constructor(private perSec: number) {}
  take(now = Date.now()): boolean {
    this.times = this.times.filter((t) => now - t < 1000);
    if (this.times.length >= this.perSec) return false;
    this.times.push(now);
    return true;
  }
}

/** The theme a frame gets: the dashboard's CSS variables, read live. */
export const THEME_VARS = [
  "--color-bg", "--color-panel", "--color-panel-2", "--color-raise", "--color-line", "--color-line-strong",
  "--color-fg", "--color-muted", "--color-dim", "--color-accent", "--color-accent-fg",
  "--color-pgm", "--color-pvw", "--color-info", "--color-warn", "--color-bad", "--color-good",
  "--radius-card", "--font-sans", "--font-mono",
];

export function readTheme(el: Element = document.documentElement): Record<string, string> {
  const cs = getComputedStyle(el);
  const out: Record<string, string> = { "color-scheme": "dark" };
  for (const k of THEME_VARS) {
    const v = cs.getPropertyValue(k).trim();
    if (v) out[k] = v;
  }
  return out;
}

/** Turns an entry like "ui/live.html" into the path under the host's
 *  /api/addons/{id}/ui/ route (which serves the package's ui/ folder). */
export function entryPath(entry: string): string | null {
  const e = entry.replace(/^\.\//, "").replace(/^ui\//, "");
  if (!e || e.startsWith("/") || e.includes("\\") || /^[a-z][a-z0-9+.-]*:/i.test(e)) return null;
  const q = e.search(/[?#]/);
  const path = q < 0 ? e : e.slice(0, q);
  const segs = path.split("/");
  if (segs.some((x) => !x || x === "." || x === "..")) return null;
  return segs.map(encodeURIComponent).join("/") + (q < 0 ? "" : e.slice(q));
}
