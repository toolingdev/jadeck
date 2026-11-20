import { createContext, useContext, useEffect, useRef, useState } from "react";
import { Puzzle } from "lucide-react";
import { api } from "@/lib/api";
import { useShared, refreshShared } from "@/lib/hooks";
import { useLog } from "@/lib/log";
import { cn } from "@/lib/cn";
import type { Manifest, WidgetDef as AddonWidgetDef } from "@/lib/addons";
import { registerWidgets, type Settings, type WidgetDef } from "./widgets";

// Widgets an add-on brings (kind "list" or "status" in its manifest),
// drawn by the controller from the add-on's state. Their dashboard type
// is addon:<add-on id>:<widget>; settings.connection picks the
// connection (defaults to the add-on's first one).

const str = (v: unknown, d = "") => (typeof v === "string" ? v : d);

/** Preview state for Settings → Add-ons (instead of the live connection). */
export const AddonSample = createContext<Record<string, unknown> | null>(null);

type Conns = { connections: { id: string; addon: string; name: string; ready: boolean; error: string }[] };

function useAddonState(addonId: string, conn: string) {
  const sample = useContext(AddonSample);
  const { data: list } = useShared<Conns>(sample ? null : "/api/devices", 10000);
  const mine = (list?.connections ?? []).filter((c) => c.addon === addonId);
  const c = mine.find((x) => x.id === conn) ?? mine[0];
  const path = !sample && c ? `/api/connections/${c.id}/state` : null;
  const { data } = useShared<Record<string, unknown>>(path, 2000);
  return { state: sample ?? data, conn: c, none: !sample && list && !c };
}

export function AddonWidget({ def, s }: { def: AddonWidgetDef; s: Settings; manifest?: Manifest }) {
  const addonId = str(s.addon);
  const { state, conn, none } = useAddonState(addonId, str(s.connection));
  const { log } = useLog();
  const b = def.bind ?? {};
  const shell = (body: React.ReactNode, title?: string) => (
    <div className="flex h-full flex-col overflow-hidden rounded-[var(--radius-card)] border border-line bg-panel">
      {title && <div className="shrink-0 px-3 pt-2.5 pb-1.5 text-[12px] font-semibold">{title}</div>}
      <div className={cn("min-h-0 flex-1", title ? "px-3 pb-3" : "p-3")}>{body}</div>
    </div>
  );
  if (none) return shell(<p className="text-xs text-dim">Add a connection for this add-on (Settings → Connections).</p>, def.label);
  if (!state) return shell(<p className="text-xs text-dim">Loading…</p>, def.label);
  if (state.error) return shell(<p className="text-xs text-bad">{String(state.error)}</p>, def.label);

  if (def.kind === "chat") return <Chat label={def.label} state={state} conn={conn} />;

  if (def.kind === "status") {
    const on = !!state[b.on ?? "on"];
    const title = str(state[b.title ?? "title"]);
    const slide = state[b.slide ?? ""] as number | undefined, slides = state[b.slides ?? ""] as number | undefined;
    return (
      <div className={cn("flex h-full flex-col justify-center gap-1 rounded-[var(--radius-card)] border-2 bg-panel px-4", on ? "border-pgm" : "border-line")}>
        <span className={cn("flex items-center gap-2 text-sm font-bold tracking-[0.1em]", on ? "text-bad" : "text-dim")}>
          <span className={cn("h-2.5 w-2.5 rounded-full", on ? "animate-pulse bg-pgm" : "bg-dim")} />{on ? "ON AIR" : "OFF AIR"}
        </span>
        {on && title && <span className="truncate text-base font-semibold">{title}{slide ? ` · slide ${slide}${slides ? ` of ${slides}` : ""}` : ""}</span>}
      </div>
    );
  }

  // list
  const items = (state[b.items ?? "items"] as { index: number; title: string }[]) ?? [];
  const cur = Number(state[b.current ?? "current"]) || 0;
  const action = b.action;
  const go = async (index: number, title: string) => {
    if (!action || !conn) return;
    try {
      await api(`/api/connections/${conn.id}/actions/${action}`, "POST", { index });
      log(conn.name, `Go to ${title}`);
      refreshShared(`/api/connections/${conn.id}/state`);
    } catch (e) {
      log(conn.name, `Go to ${title} failed — ${(e as Error).message}`, false);
    }
  };
  return shell(
    items.length === 0 ? <p className="text-xs text-dim">{state.onAir === false ? "Not on air." : "Nothing in the list."}</p> : (
      <div className="h-full space-y-1 overflow-y-auto" data-feed>
        {items.map((it) => (
          <button key={it.index} type="button" onClick={() => go(it.index, it.title)}
            className={cn("flex w-full items-center gap-2 rounded-lg border px-2.5 py-2 text-left text-sm active:scale-[0.99]",
              it.index === cur ? "border-accent bg-accent/12 font-semibold text-accent" : "border-transparent bg-panel-2 hover:border-line-strong")}>
            <span className="w-5 shrink-0 text-right font-mono text-[11px] text-dim">{it.index}</span>
            <span className="min-w-0 flex-1 truncate">{it.title}</span>
            {it.index === cur && <span className="text-[10px] font-bold">● LIVE</span>}
          </button>
        ))}
      </div>
    ),
    def.label,
  );
}

/** Widget-library entries for every add-on widget (registered as the add-ons load). */
export function addonWidgetDefs(manifests: Manifest[]): WidgetDef[] {
  const out: WidgetDef[] = [];
  for (const m of manifests) {
    for (const w of m.widgets ?? []) {
      if (w.kind === "core") continue;
      const def = w;
      out.push({
        type: `addon:${m.id}:${w.type}`, label: w.label, group: "addons", icon: Puzzle,
        description: `${m.name}: ${w.description ?? ""}`.trim(),
        size: w.kind === "chat" ? { w: 5, h: 10, pw: 4, ph: 10 } : w.kind === "list" ? { w: 4, h: 8, pw: 4, ph: 8 } : { w: 4, h: 2, pw: 4, ph: 2 },
        minPx: w.kind === "chat" ? { w: 260, h: 260 } : w.kind === "list" ? { w: 180, h: 140 } : { w: 160, h: 60 },
        defaults: { addon: m.id, widget: w.type },
        fields: [{ key: "connection", label: "Connection", type: "connection", addon: m.id }],
        Render: ({ s }) => <AddonWidget def={def} s={s} />,
      });
    }
  }
  return out;
}

/** Registers add-on widgets and returns them (re-renders when add-ons change). */
export function useAddonWidgets(): WidgetDef[] {
  const { data } = useShared<{ addons: { info: { enabled: boolean; manifest: Manifest } }[] }>("/api/addons", 0);
  const defs = addonWidgetDefs((data?.addons ?? []).filter((a) => a.info.enabled).map((a) => a.info.manifest));
  registerWidgets(defs);
  return defs;
}

// ------------------------------------------------------------- chat

type Line = { role: "user" | "assistant" | "action" | "error"; text: string; pending?: { label: string }[]; at?: string };

/** One conversation per screen. */
function chatSession() {
  try {
    let id = localStorage.getItem("ptz.chatSession");
    if (!id) {
      id = (localStorage.getItem("ptz.screen") || "screen") + "-" + Math.random().toString(36).slice(2, 7);
      localStorage.setItem("ptz.chatSession", id);
    }
    return id;
  } catch {
    return "screen";
  }
}

function Chat({ label, state, conn }: { label: string; state: Record<string, unknown>; conn?: { id: string; name: string } }) {
  const sid = useRef(chatSession()).current;
  const preview = useContext(AddonSample);
  const saved = ((state.sessions as Record<string, { lines: Line[]; pending: boolean }> | undefined)?.[sid]);
  const [lines, setLines] = useState<Line[]>(() => (preview ? (state.messages as Line[]) ?? [] : saved?.lines ?? []));
  const [pending, setPending] = useState(!!saved?.pending || !!preview);
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const list = useRef<HTMLDivElement>(null);
  const loaded = useRef(!!saved);
  useEffect(() => { // first state from the add-on (a reopened screen sees its conversation)
    if (!loaded.current && saved) { loaded.current = true; setLines(saved.lines ?? []); setPending(!!saved.pending); }
  }, [saved]);
  useEffect(() => { const el = list.current; if (el && !preview) el.scrollTop = el.scrollHeight; }, [lines.length, busy, preview]);
  const send = async (action: "chat" | "approve" | "reject" | "reset", message?: string) => {
    if (!conn || preview) return;
    setBusy(true);
    if (action === "chat" && message) setLines((l) => [...l, { role: "user", text: message }]);
    try {
      const r = await api<{ result: { lines: Line[]; pending: boolean } }>(`/api/connections/${conn.id}/actions/${action}`, "POST", { session: sid, message });
      const got = r.result?.lines ?? [];
      setLines((l) => action === "reset" ? [] : [...(action === "chat" ? l.slice(0, -1) : l), ...got]);
      setPending(!!r.result?.pending);
    } catch (e) {
      setLines((l) => [...l, { role: "error", text: (e as Error).message }]);
    } finally {
      setBusy(false);
    }
  };
  const lastPending = pending ? [...lines].reverse().find((l) => l.pending?.length) : undefined;
  return (
    <div className="flex h-full flex-col overflow-hidden rounded-[var(--radius-card)] border border-line bg-panel">
      <div className="flex shrink-0 items-center gap-2 px-3 pt-2.5 pb-1.5">
        <span className="text-[12px] font-semibold">✦ {label}</span>
        <span className="ml-auto truncate text-[11px] text-dim">{conn?.name ?? ""}{state.askFirst === false ? " · acts right away" : " · asks before acting"}</span>
        {lines.length > 0 && !preview && <button type="button" className="text-[11px] text-muted hover:text-fg" onClick={() => send("reset")}>New</button>}
      </div>
      <div ref={list} data-feed className="min-h-0 flex-1 space-y-2 overflow-y-auto px-3 pb-2">
        {lines.length === 0 && <p className="pt-2 text-xs text-dim">Tell the booth what to do — “go to the pulpit shot”, “next slide”, “in 5 minutes cut to camera 2”.</p>}
        {lines.map((l, i) => (
          <div key={i} className={cn("max-w-[90%] rounded-xl px-3 py-2 text-sm",
            l.role === "user" ? "ml-auto bg-info/15" : l.role === "action" ? "bg-transparent px-1 py-0 text-xs text-good" : l.role === "error" ? "bg-bad/10 text-bad" : "bg-panel-2")}>
            {l.text}
            {l.pending?.length ? (
              <div className="mt-2 space-y-1">
                {l.pending.map((p, j) => <div key={j} className="rounded-lg border border-line bg-bg px-2 py-1.5 text-xs">▶ {p.label}</div>)}
              </div>
            ) : null}
          </div>
        ))}
        {busy && <p className="text-xs text-dim">…</p>}
      </div>
      {lastPending && (
        <div className="flex shrink-0 gap-2 px-3 pb-2">
          <button type="button" disabled={busy} onClick={() => send("approve")} className="rounded-lg bg-accent px-4 py-2 text-sm font-bold text-accent-fg disabled:opacity-50">Do it</button>
          <button type="button" disabled={busy} onClick={() => send("reject")} className="rounded-lg bg-raise px-4 py-2 text-sm disabled:opacity-50">Cancel</button>
        </div>
      )}
      <form className="flex shrink-0 gap-2 border-t border-line p-2" onSubmit={(e) => { e.preventDefault(); const m = text.trim(); if (m && !busy) { setText(""); send("chat", m); } }}>
        <input value={text} onChange={(e) => setText(e.target.value)} disabled={busy || !conn} placeholder={conn || preview ? "Tell the booth what to do…" : "Add an AI controller connection first"}
          className="h-10 min-w-0 flex-1 rounded-xl border border-line bg-bg px-3 text-sm focus:border-accent focus:outline-none" />
        <button type="submit" disabled={busy || !text.trim()} className="rounded-xl px-3 text-sm font-bold text-accent disabled:opacity-40">Send</button>
      </form>
    </div>
  );
}
