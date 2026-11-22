import { useContext, useEffect, useRef, useState } from "react";
import { api } from "@/lib/api";
import { cn } from "@/lib/cn";
import { AddonSample } from "./common";
import { WidgetHeader, Ctx } from "../header";

// kind "chat": a conversation with an assistant add-on (the AI controller).

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

export function Chat({ label, state, conn }: { label: string; state: Record<string, unknown>; conn?: { id: string; name: string; ready?: boolean } }) {
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
    <div className="card-surface flex h-full flex-col overflow-hidden rounded-[var(--radius-card)] border border-line">
      <WidgetHeader title={`✦ ${label}`} source={conn?.name} online={conn && "ready" in conn ? !!conn.ready : undefined}
        right={<>
          <Ctx>{state.askFirst === false ? "acts right away" : "asks before acting"}</Ctx>
          {lines.length > 0 && !preview && <button type="button" className="text-[11px] text-muted hover:text-fg" onClick={() => send("reset")}>New</button>}
        </>} />
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
