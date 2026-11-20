import { useShared } from "@/lib/hooks";

// Add-ons and connections (see design/ADDONS-DESIGN.md).

export type Opt = { value: string; label: string };
export type Field = {
  key: string; label: string; type: "text" | "number" | "select" | "bool" | "notes" | "json" | "password" | "scopes" | "textarea";
  default?: unknown; placeholder?: string; help?: string; required?: boolean; advanced?: boolean;
  min?: number; max?: number; options?: Opt[]; optionsFrom?: string; keys?: string[];
};
export type Capability = "switcher" | "slides" | "mixer" | "assistant";
export type ConnectionType = {
  id: string; label: string; description: string; capabilities: Capability[]; fields: Field[];
  sample?: Record<string, unknown>; sampleTest?: string;
};
export type ActionDef = { id: string; label: string; description?: string; params?: Field[]; macroStep?: boolean };
export type WidgetDef = {
  kind: "core" | "list" | "status" | "chat"; type: string; label: string; description?: string;
  bind?: Record<string, string>; settings?: Record<string, unknown>; sample: Record<string, unknown>;
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

export const CAP_LABEL: Record<Capability, string> = { switcher: "Video switcher", slides: "Slides", mixer: "Audio mixer", assistant: "Assistant" };

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

export function defaults(t: ConnectionType): Record<string, unknown> {
  const c: Record<string, unknown> = {};
  for (const f of t.fields) if (f.default !== undefined) c[f.key] = f.default;
  return c;
}
