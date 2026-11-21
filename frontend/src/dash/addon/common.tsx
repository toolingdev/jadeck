import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { cn } from "@/lib/cn";
import { api } from "@/lib/api";
import { refreshShared, refreshSharedAll, useShared } from "@/lib/hooks";
import { useLog } from "@/lib/log";
import type { Manifest, WidgetDef as AddonWidgetDef } from "@/lib/addons";
import type { Settings } from "../widgets";

// Shared pieces for add-on widgets (design/WIDGETS-V2.md): reading bound
// state, resolving pictures, and running the add-on's actions.

export type State = Record<string, unknown>;
export type Conn = { id: string; addon: string; name: string; ready: boolean; error: string };

/** Preview state for Settings → Add-ons (instead of the live connection). */
export const AddonSample = createContext<State | null>(null);

/** What every shape gets. `preview` = drawn from the manifest's sample. */
export type ShapeProps = {
  def: AddonWidgetDef; state: State; conn?: Conn; s: Settings; editing: boolean; preview: boolean; manifest?: Manifest;
};

export const str = (v: unknown, d = "") => (typeof v === "string" ? v : typeof v === "number" ? String(v) : d);
export const num = (v: unknown, d: number) => (typeof v === "number" && Number.isFinite(v) ? v : typeof v === "string" && v.trim() !== "" && Number.isFinite(Number(v)) ? Number(v) : d);
export const isObj = (v: unknown): v is Record<string, unknown> => !!v && typeof v === "object" && !Array.isArray(v);
export const objs = (v: unknown): Record<string, unknown>[] => (Array.isArray(v) ? v.filter(isObj) : []);

/** The state value a bind entry names (bind values are state keys). */
export function bound(state: State, b: Record<string, unknown>, name: string, fallbackKey?: string): unknown {
  const k = typeof b[name] === "string" ? (b[name] as string) : fallbackKey;
  return k ? state[k] : undefined;
}

/** A label: the state value if the bind entry names a key that's in state,
 *  else the bind entry itself as text (labels, titles, placeholders). */
export function label(state: State, b: Record<string, unknown>, name: string): string {
  const v = b[name];
  if (typeof v !== "string" || !v) return "";
  return Object.prototype.hasOwnProperty.call(state, v) ? str(state[v]) : v;
}

/** A list: a state key, or a static array in bind (buttons, choices). */
export function listOf(state: State, b: Record<string, unknown>, name: string): Record<string, unknown>[] {
  const v = b[name];
  return Array.isArray(v) ? objs(v) : typeof v === "string" ? objs(state[v]) : [];
}

export const statePath = (conn?: { id: string }) => (conn ? `/api/connections/${conn.id}/state` : "");

/** "slides/a b/1.png?w=640" → "slides/a%20b/1.png?w=640"; null if it tries to
 *  leave the add-on's resource space. */
export function encodeResPath(p: string): string | null {
  const q = p.indexOf("?");
  const path = q < 0 ? p : p.slice(0, q), query = q < 0 ? "" : p.slice(q + 1);
  if (!path || path.length > 512 || path.startsWith("/") || path.includes("\\")) return null;
  const segs = path.split("/");
  if (segs.some((x) => !x || x === "." || x === "..")) return null;
  const qs = query ? new URLSearchParams(query).toString() : "";
  return segs.map(encodeURIComponent).join("/") + (qs ? `?${qs}` : "");
}

/** URL for a picture in state. The host rewrites "res:…" to its route; we
 *  also accept the raw form (sample data) and data: images. Anything else
 *  (other sites, javascript:) is refused. */
export function resUrl(src: unknown, conn?: { id: string }): string | null {
  if (typeof src !== "string" || !src) return null;
  if (/^data:image\/(png|jpe?g|gif|webp|svg\+xml);/i.test(src)) return src;
  if (src.startsWith("res:")) {
    const p = encodeResPath(src.slice(4));
    return conn && p ? `/api/connections/${encodeURIComponent(conn.id)}/res/${p}` : null;
  }
  if (/^\/api\/connections\/[^/?#]+\/res\/[^?#]/.test(src) && !src.includes("..")) return src;
  return null;
}

/** Which dashboard widget asks for state (v2.1): its settings go to the add-on. */
export type WidgetRef = { id: string; dash?: number; settingsKey?: string };

/** The state request for a connection; ?widget=<id>&dash=<id> when the
 *  widget has add-on settings, so the add-on gets them. */
export function widgetStatePath(conn: { id: string }, w?: WidgetRef) {
  const base = statePath(conn);
  if (!w?.id) return base;
  const q = new URLSearchParams({ widget: w.id });
  if (w.dash) q.set("dash", String(w.dash));
  return `${base}?${q}`;
}

export function useAddonState(addonId: string, connId: string, widget?: WidgetRef) {
  const sample = useContext(AddonSample);
  const { data: list } = useShared<{ connections: Conn[] }>(sample ? null : "/api/devices", 10000);
  const mine = (list?.connections ?? []).filter((c) => c.addon === addonId);
  const c = mine.find((x) => x.id === connId) ?? mine[0];
  const path = !sample && c ? widgetStatePath(c, widget) : null;
  const { data } = useShared<State>(path, 2000);
  // Settings changed in the Inspector: ask again once the dashboard has
  // saved them (the host reads the saved ones), instead of waiting a poll.
  const key = widget?.settingsKey;
  const first = useRef(true);
  useEffect(() => {
    if (first.current) { first.current = false; return; }
    if (!path) return;
    const t = window.setTimeout(() => refreshShared(path), 1500);
    return () => clearTimeout(t);
  }, [key, path]);
  return { state: sample ?? data, conn: c, none: !sample && !!list && !c, preview: !!sample };
}

/** Runs one of the add-on's actions the way the list widget does: POST,
 *  write the action log (errors too), refresh the state. `busy` is the key
 *  of the call in flight, so controls can disable themselves. */
export function useAddonAction(conn: Conn | undefined, preview: boolean) {
  const { log } = useLog();
  const [busy, setBusy] = useState<string | null>(null);
  const inFlight = useRef(0);
  const run = useCallback(async (action: string, params: Record<string, unknown>, what: string, key = action, opts?: { quiet?: boolean }) => {
    if (!action || !conn || preview) return false;
    inFlight.current++;
    setBusy(key);
    try {
      await api(`/api/connections/${conn.id}/actions/${encodeURIComponent(action)}`, "POST", params);
      if (!opts?.quiet) log(conn.name, what);
      return true;
    } catch (e) {
      // Quiet calls (a slider mid-drag) leave errors to the final one.
      if (!opts?.quiet) log(conn.name, `${what} failed — ${(e as Error).message}`, false);
      return false;
    } finally {
      if (--inFlight.current === 0) setBusy(null);
      refreshSharedAll(statePath(conn));
    }
  }, [conn, preview, log]);
  return { run, busy };
}

/** The connection-name chip beside a widget's title (WIDGETS.md §3): the
 *  context, top-right, in letter-spaced small caps (§6). */
export function ConnChip({ conn }: { conn?: Conn }) {
  if (!conn) return null;
  return <span className="smallcaps truncate leading-[18px]">{conn.name}</span>;
}

/** Status pill (e.g. LIVE). Red when the tone is program, green for preview. */
export function Badge({ text, tone }: { text: string; tone?: string }) {
  if (!text) return null;
  return (
    <span className={cn("shrink-0 rounded-full px-2 py-0.5 text-[10px] font-bold leading-[15px] tracking-[0.08em]",
      tone === "pvw" ? "btn-pvw" : tone === "none" ? "recess border text-muted" : "btn-pgm")}>
      {text.slice(0, 12)}
    </span>
  );
}

/** Button look for a state (WIDGETS.md §6): colour is state only. */
export function stateClass(tone: string, active: boolean, disabled = false) {
  if (disabled) return "btn-off";
  if (tone === "pgm") return "btn-pgm";
  if (tone === "pvw") return "btn-pvw";
  if (active || tone === "accent" || tone === "primary") return "btn-sel";
  return "btn-raised hover:brightness-125";
}

/** A number readout: monospace in a recessed pill. */
export function Readout({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <span className={cn("recess inline-flex shrink-0 items-center rounded-md border px-1.5 font-mono text-[11px] font-bold leading-[16px] tabular text-fg", className)}>
      {children}
    </span>
  );
}

/** Top-left number tag on a picture: a black pill. */
export function NumTag({ children }: { children: ReactNode }) {
  return (
    <span className="pointer-events-none absolute left-2 top-2 z-[2] min-w-[22px] rounded-[5px] bg-black/60 px-1.5 text-center text-[10.5px] font-extrabold leading-[18px] text-fg tabular">
      {children}
    </span>
  );
}

/** The 3px tally ring with a soft glow, drawn over a picture (no layout). */
export function TallyRing({ tone, radius = "var(--radius-pic)" }: { tone: string; radius?: string }) {
  if (tone !== "pgm" && tone !== "pvw") return null;
  return <span aria-hidden className={cn("pointer-events-none absolute inset-0 z-[3]", tone === "pgm" ? "tally-pgm" : "tally-pvw")} style={{ borderRadius: radius }} />;
}
