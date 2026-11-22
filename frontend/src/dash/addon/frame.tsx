import { useEffect, useMemo, useRef } from "react";
import { api } from "@/lib/api";
import { refreshSharedAll } from "@/lib/hooks";
import { fieldSettings, useAddons } from "@/lib/addons";
import { useShared } from "@/lib/hooks";
import { useLog } from "@/lib/log";
import { encodeResPath, statePath, str, type ShapeProps } from "./common";
import {
  BRIDGE_VERSION, entryPath, MAX_ACTIONS_PER_SEC, MAX_LOGS_PER_SEC, MAX_RESOURCE_BYTES, MAX_RESOURCES_PER_SEC,
  parseFromFrame, RateLimit, readTheme, type ReqId,
} from "./bridge";

// kind "frame": the add-on draws the widget itself, in a sandboxed iframe
// (allow-scripts only: opaque origin, no cookies, no API, no storage). The
// bridge in bridge.ts is its only way out; every message is checked here.

const touch = () => typeof matchMedia === "function" && matchMedia("(pointer: coarse)").matches;

export function FrameShape({ def, state, conn, s, editing, preview, manifest }: ShapeProps) {
  const { log } = useLog();
  const addonId = str(s.addon) || manifest?.id || "";
  const path = def.entry ? entryPath(def.entry) : null;
  const src = addonId && path ? `/api/addons/${encodeURIComponent(addonId)}/ui/${path}` : null;
  const frame = useRef<HTMLIFrameElement>(null);
  const box = useRef<HTMLDivElement>(null);

  // Everything the message handler needs, kept current without re-binding it.
  // Every setting: the manifest's preview settings, the add-on fields'
  // defaults, then what's saved with the widget (v2.1).
  const settings = useMemo(() => ({ ...(def.settings ?? {}), ...fieldSettings(def.fields, {}), ...s }), [def.settings, def.fields, s]);
  const declared = useMemo(() => new Map((manifest?.actions ?? []).map((a) => [a.id, a.label || a.id])), [manifest]);
  // "via": the widget's connection fields may point at other add-ons'
  // connections (Live chat → vMix); their actions are checked against
  // that add-on's own list.
  const viaKeys = useMemo(() => new Set((def.fields ?? []).filter((f) => f.type === "connection").map((f) => f.key)), [def.fields]);
  const { data: addonList } = useAddons();
  const { data: devs } = useShared<{ connections: { id: string; name: string; addon: string }[] }>(viaKeys.size ? "/api/devices" : null, 10000);
  const others = { addons: addonList?.addons ?? [], conns: devs?.connections ?? [] };
  const live = useRef({ state, settings, editing, conn, preview, declared, label: def.label, viaKeys, others });
  live.current = { state, settings, editing, conn, preview, declared, label: def.label, viaKeys, others };

  const io = useRef({ ready: false, state: "", size: "", theme: "", limits: {
    action: new RateLimit(MAX_ACTIONS_PER_SEC), resource: new RateLimit(MAX_RESOURCES_PER_SEC), log: new RateLimit(MAX_LOGS_PER_SEC),
  } });

  const post = (msg: Record<string, unknown>) => {
    // The frame's origin is opaque ("null"), so "*" is the only target that
    // reaches it; what we send is the widget's own state, nothing secret.
    try { frame.current?.contentWindow?.postMessage(msg, "*"); } catch { /* frame gone */ }
  };
  const sizeNow = () => {
    const r = box.current?.getBoundingClientRect();
    return { w: Math.round(r?.width ?? 0), h: Math.round(r?.height ?? 0) };
  };
  const sendInit = () => {
    const L = live.current, size = sizeNow(), theme = readTheme();
    io.current.state = JSON.stringify(L.state ?? {});
    io.current.size = JSON.stringify(size);
    io.current.theme = JSON.stringify(theme);
    post({ t: "init", v: BRIDGE_VERSION, state: L.state ?? {}, settings: L.settings, theme, size, editing: L.editing, touch: touch() });
  };

  // frame → host
  useEffect(() => {
    const reply = (id: ReqId, r: Record<string, unknown>) => post({ t: "result", id, ...r });
    const onMessage = async (e: MessageEvent) => {
      const win = frame.current?.contentWindow;
      // Only this widget's own frame, and only while it's sandboxed (opaque origin).
      if (!win || e.source !== win || e.origin !== "null") return;
      const m = parseFromFrame(e.data);
      if (!m) return;
      const L = live.current;
      const who = L.conn?.name ?? L.label;
      if ("bad" in m) {
        if (m.id !== undefined) reply(m.id, { ok: false, error: m.bad });
        return;
      }
      switch (m.t) {
        case "ready":
          io.current.ready = true;
          sendInit();
          return;
        case "log":
          if (!L.preview && io.current.limits.log.take()) log(who, `${L.label}: ${m.text}`);
          return;
        case "action": {
          if (m.via) {
            if (!L.viaKeys.has(m.via)) return reply(m.id, { ok: false, error: `“${m.via}” isn't a connection setting of this widget` });
            const id = typeof L.settings[m.via] === "string" ? (L.settings[m.via] as string) : "";
            const target = L.others.conns.find((c) => c.id === id);
            if (!target) return reply(m.id, { ok: false, error: id ? "that connection was removed" : "no connection picked in this widget's settings" });
            const acts = L.others.addons.find((a) => a.info.manifest.id === target.addon)?.info.manifest.actions ?? [];
            const act = acts.find((a) => a.id === m.name);
            if (!act) return reply(m.id, { ok: false, error: `“${m.name}” isn't one of ${target.name}'s actions` });
            if (!io.current.limits.action.take()) return reply(m.id, { ok: false, error: "too many actions (10 a second at most)" });
            if (L.editing) return reply(m.id, { ok: false, error: "the dashboard is being edited" });
            if (L.preview) return reply(m.id, { ok: false, error: "preview: actions don't run" });
            try {
              const r = await api<unknown>(`/api/connections/${encodeURIComponent(target.id)}/actions/${encodeURIComponent(m.name)}`, "POST", m.params);
              reply(m.id, { ok: true, result: r && typeof r === "object" && "result" in r ? (r as { result: unknown }).result : r });
            } catch (err) {
              const msg = (err as Error).message;
              log(target.name, `${L.label}: ${act.label || act.id} failed — ${msg}`, false);
              reply(m.id, { ok: false, error: msg });
            }
            return;
          }
          if (!L.declared.has(m.name)) return reply(m.id, { ok: false, error: `“${m.name}” isn't one of this add-on's actions` });
          if (!io.current.limits.action.take()) return reply(m.id, { ok: false, error: "too many actions (10 a second at most)" });
          if (L.editing) return reply(m.id, { ok: false, error: "the dashboard is being edited" });
          if (L.preview) return reply(m.id, { ok: false, error: "preview: actions don't run" });
          if (!L.conn) return reply(m.id, { ok: false, error: "no connection for this add-on" });
          const what = `${L.label}: ${L.declared.get(m.name)}`;
          try {
            const r = await api<unknown>(`/api/connections/${L.conn.id}/actions/${encodeURIComponent(m.name)}`, "POST", m.params);
            log(L.conn.name, what);
            reply(m.id, { ok: true, result: r && typeof r === "object" && "result" in r ? (r as { result: unknown }).result : r });
          } catch (err) {
            const msg = (err as Error).message;
            log(L.conn.name, `${what} failed — ${msg}`, false);
            reply(m.id, { ok: false, error: msg });
          } finally {
            refreshSharedAll(statePath(L.conn));
          }
          return;
        }
        case "resource": {
          if (!io.current.limits.resource.take()) return reply(m.id, { ok: false, error: "too many resource requests" });
          const p = encodeResPath(m.path.replace(/^res:/, ""));
          if (!p) return reply(m.id, { ok: false, error: "bad resource path" });
          if (!L.conn) return reply(m.id, { ok: false, error: L.preview ? "preview: no connection" : "no connection for this add-on" });
          try {
            const r = await fetch(`/api/connections/${encodeURIComponent(L.conn.id)}/res/${p}`, { credentials: "same-origin" });
            if (!r.ok) return reply(m.id, { ok: false, error: `HTTP ${r.status}` });
            const blob = await r.blob();
            if (blob.size > MAX_RESOURCE_BYTES) return reply(m.id, { ok: false, error: "resource too big" });
            reply(m.id, { ok: true, blob });
          } catch (err) {
            reply(m.id, { ok: false, error: (err as Error).message });
          }
          return;
        }
      }
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
    // post/sendInit read refs only.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [log]);

  // host → frame: state on change
  useEffect(() => {
    if (!io.current.ready) return;
    const j = JSON.stringify(state ?? {});
    if (j !== io.current.state) { io.current.state = j; post({ t: "state", state: state ?? {} }); }
  }, [state]);

  // editing or settings changed: a fresh init (the frame already handles it).
  const settingsKey = JSON.stringify(settings);
  useEffect(() => {
    if (io.current.ready) sendInit();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [editing, settingsKey]);

  // size
  useEffect(() => {
    const el = box.current;
    if (!el) return;
    const ro = new ResizeObserver(() => {
      if (!io.current.ready) return;
      const size = sizeNow(), j = JSON.stringify(size);
      if (j !== io.current.size) { io.current.size = j; post({ t: "size", size }); }
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  // theme (the dashboard is dark only today; this keeps frames in step if
  // the variables ever change at runtime)
  useEffect(() => {
    const mo = new MutationObserver(() => {
      if (!io.current.ready) return;
      const theme = readTheme(), j = JSON.stringify(theme);
      if (j !== io.current.theme) { io.current.theme = j; post({ t: "theme", theme }); }
    });
    mo.observe(document.documentElement, { attributes: true, attributeFilter: ["class", "style", "data-theme"] });
    return () => mo.disconnect();
  }, []);

  // A new page in the frame (src changed) must say ready again.
  useEffect(() => { io.current.ready = false; }, [src]);

  return (
    <div ref={box} className="card-surface relative h-full overflow-hidden rounded-[var(--radius-card)] border border-line">
      {src ? (
        <iframe ref={frame} key={src} src={src} title={def.label} sandbox="allow-scripts" referrerPolicy="no-referrer"
          allow="" className="absolute inset-0 h-full w-full border-0 bg-transparent" />
      ) : (
        <div className="flex h-full items-center justify-center p-3 text-center text-xs text-dim">This widget's page is missing from the add-on.</div>
      )}
      {/* While editing, drags and taps go to the editor, not the add-on. */}
      {editing && <div className="absolute inset-0 z-10 cursor-move bg-transparent" data-frame-cover />}
    </div>
  );
}
