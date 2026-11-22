import { useShared } from "@/lib/hooks";

// Add-ons and connections (see design/ADDONS-DESIGN.md).

export type Opt = { value: string; label: string };
export type Field = {
  key: string; label: string; type: "text" | "number" | "select" | "bool" | "notes" | "json" | "password" | "scopes" | "textarea" | "order";
  default?: unknown; placeholder?: string; help?: string; required?: boolean; advanced?: boolean;
  min?: number; max?: number; options?: Opt[]; optionsFrom?: string; keys?: string[];
  /** select only (v2.1): pick several; the value is a string array. "order" is always a string array. */
  multiple?: boolean;
};
/** "tool" (apiVersion 2): a general add-on — widgets and actions, but not a switcher, slides, mixer or assistant. */
export type Capability = "switcher" | "slides" | "mixer" | "assistant" | "tool";
export type ConnectionType = {
  id: string; label: string; description: string; capabilities: Capability[]; fields: Field[];
  sample?: Record<string, unknown>; sampleTest?: string;
  /** Short "what you need" line in Add connection. */
  needs?: string;
  /** The "How to set up" card above the fields. */
  guide?: Guide;
};
export type Guide = { minutes?: number; steps: { text: string; code?: string; link?: { label: string; url: string } }[]; note?: string };
export type ActionDef = { id: string; label: string; description?: string; params?: Field[]; macroStep?: boolean };
/** Widget kinds the dashboard can draw (WIDGETS-V2.md). v2 adds the shapes
 *  after "chat" and "frame" (drawn by the add-on in a sandboxed iframe). */
export const WIDGET_KINDS = ["core", "list", "status", "chat", "image", "buttons", "input", "strip", "text", "clock", "sections", "slider", "frame"] as const;
export type WidgetKind = (typeof WIDGET_KINDS)[number];
export type WidgetDef = {
  kind: WidgetKind; type: string; label: string; description?: string;
  /** Per kind: mostly state keys, plus static values (actionParams, buttons, choices, min/max, columns…). */
  bind?: Record<string, any>; settings?: Record<string, unknown>; sample: Record<string, unknown>;
  /** Default grid size on a desktop dashboard (12 columns); the host clamps it. */
  size?: { w: number; h: number };
  /** Smallest box (px) the widget is usable in. */
  minPx?: { w: number; h: number };
  /** Frame widgets: the page in the add-on's ui/ folder. */
  entry?: string;
  /** v2.1: per-widget settings, shown in the Inspector and saved with the widget.
   *  Binds may read one as "$settings.<key>". */
  fields?: Field[];
};
export type Manifest = {
  id: string; name: string; version: string; publisher: string; description: string; apiVersion: number;
  generic?: boolean; repo?: string; reaches: string; controller?: boolean;
  connectionTypes: ConnectionType[]; actions?: ActionDef[]; widgets?: WidgetDef[]; changelog?: string;
};
export type AddonInfo = {
  manifest: Manifest; source: "installed" | "dev"; verified: boolean; enabled: boolean; running: boolean;
  pid?: number; since?: string; lastError?: string; canRollBack?: boolean; path?: string;
};
export type AddonRow = { info: AddonInfo; connections: number; widgets: number; dashboards: number; update?: { version: string; notes?: string } };
export type AddonList = { addons: AddonRow[]; platform: string; checkedAt?: string; checkError?: string };

export type Fav = { label: string; channel: number };
export type Connection = {
  id: string; addon: string; type: string; name: string; config: Record<string, unknown>; favorites: Fav[]; cameraInputs?: Record<string, number>;
  disabled: boolean; capabilities: Capability[]; ready: boolean; error: string; addonName?: string; typeLabel?: string;
};

export const CAP_LABEL: Record<Capability, string> = { switcher: "Video switcher", slides: "Slides", mixer: "Audio mixer", assistant: "Assistant", tool: "Tools & widgets" };

export function useAddons(ms = 0) {
  return useShared<AddonList>("/api/addons", ms);
}

export function useConnections(ms = 0) {
  return useShared<{ connections: Connection[] }>("/api/connections", ms);
}

/** Connections that have a capability (for widget pickers). */
export function useConnectionsWith(cap: Capability) {
  const { data } = useShared<{ connections: { id: string; name: string; capabilities: Capability[]; ready: boolean }[] }>("/api/devices", 10000);
  return (data?.connections ?? []).filter((c) => c.capabilities?.includes(cap));
}

/** ?conn= for a widget's chosen connection ("" = the default one). */
export const withConn = (path: string, conn?: unknown) =>
  typeof conn === "string" && conn ? `${path}${path.includes("?") ? "&" : "?"}conn=${encodeURIComponent(conn)}` : path;

/** A widget's settings for its add-on fields: each field's default, then what's saved. */
export function fieldSettings(fields: Field[] | undefined, s: Record<string, unknown>): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const f of fields ?? []) {
    const v = s[f.key];
    if (v !== undefined && v !== null) out[f.key] = v;
    else if (f.default !== undefined) out[f.key] = f.default;
  }
  return out;
}

/** "$settings.<key>" bind values replaced by that setting (anywhere in bind).
 *  The result is read by the usual bind rules: a string names a state key
 *  (or is text, for labels), an array is a static list, a number a static value. */
export const SETTINGS_REF = "$settings.";
export function resolveBind(bind: Record<string, unknown> | undefined, settings: Record<string, unknown>): Record<string, any> | undefined {
  if (!bind) return bind;
  const walk = (v: unknown): unknown => {
    if (typeof v === "string") return v.startsWith(SETTINGS_REF) ? settings[v.slice(SETTINGS_REF.length)] : v;
    if (Array.isArray(v)) return v.map(walk);
    if (v && typeof v === "object") return Object.fromEntries(Object.entries(v).map(([k, x]) => [k, walk(x)]));
    return v;
  };
  return walk(bind) as Record<string, any>;
}

export function defaults(t: ConnectionType): Record<string, unknown> {
  const c: Record<string, unknown> = {};
  for (const f of t.fields) if (f.default !== undefined) c[f.key] = f.default;
  return c;
}
