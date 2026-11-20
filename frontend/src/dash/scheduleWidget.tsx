import { useEffect, useState } from "react";
import { api } from "@/lib/api";
import { useShared, refreshShared } from "@/lib/hooks";
import { useLog } from "@/lib/log";
import { cn } from "@/lib/cn";
import type { Settings } from "./widgets";

// Coming up: scheduled actions (from the Assistant, macros, agents). Anyone
// can run one now or cancel it.

type Job = { id: string; at: string; label: string; by: string; status: string; result?: string };

function left(ms: number) {
  if (ms <= 0) return "now";
  const s = Math.round(ms / 1000);
  if (s < 60) return `in ${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `in ${m}:${String(s % 60).padStart(2, "0")}`;
  return `in ${Math.floor(m / 60)} h ${m % 60} min`;
}

export function ComingUp({ s }: { s: Settings }) {
  const { data } = useShared<{ jobs: Job[] }>("/api/schedule", 2000);
  const [, tick] = useState(0);
  const { log } = useLog();
  useEffect(() => {
    const t = setInterval(() => tick((n) => n + 1), 1000);
    return () => clearInterval(t);
  }, []);
  const jobs = data?.jobs ?? [];
  const pending = jobs.filter((j) => j.status === "pending" || j.status === "running");
  const recent = s.recent === false ? [] : jobs.filter((j) => !["pending", "running"].includes(j.status)).slice(0, 3);
  const act = async (j: Job, what: "run" | "cancel") => {
    try {
      await api(what === "run" ? `/api/schedule/${j.id}/run` : `/api/schedule/${j.id}`, what === "run" ? "POST" : "DELETE");
      log("Coming up", `${what === "run" ? "Ran now" : "Cancelled"}: ${j.label}`);
    } catch (e) { log("Coming up", (e as Error).message, false); }
    refreshShared("/api/schedule");
  };
  return (
    <div className="flex h-full flex-col overflow-hidden rounded-[var(--radius-card)] border border-line bg-panel">
      <div className="shrink-0 px-3 pt-2.5 pb-1.5 text-[12px] font-semibold">Coming up</div>
      <div className="min-h-0 flex-1 space-y-1.5 overflow-y-auto px-3 pb-3" data-feed>
        {pending.length === 0 && <p className="text-xs text-dim">Nothing scheduled. Ask the Assistant (“in 5 minutes cut to camera 2”) or an agent.</p>}
        {pending.map((j, i) => {
          const ms = new Date(j.at).getTime() - Date.now();
          return (
            <div key={j.id} className={cn("flex items-center gap-3 rounded-xl border px-3 py-2", i === 0 ? "border-accent bg-accent/10" : "border-line bg-panel-2")}>
              <span className={cn("w-16 shrink-0 font-mono text-sm font-bold tabular", i === 0 ? "text-accent" : "text-muted")}>
                {j.status === "running" ? "now" : ms > 3600_000 ? new Date(j.at).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" }) : left(ms)}
              </span>
              <span className="min-w-0 flex-1">
                <span className="block truncate text-sm font-semibold">{j.label}</span>
                <span className="block truncate text-[11px] text-muted">{new Date(j.at).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })} · {j.by}</span>
              </span>
              {j.status === "pending" && (
                <span className="flex shrink-0 gap-1">
                  <button type="button" className="rounded-lg px-2 py-1 text-xs text-muted hover:bg-raise hover:text-fg" onClick={() => act(j, "run")}>Run now</button>
                  <button type="button" className="rounded-lg px-2 py-1 text-xs text-bad hover:bg-raise" onClick={() => act(j, "cancel")}>Cancel</button>
                </span>
              )}
            </div>
          );
        })}
        {recent.map((j) => (
          <p key={j.id} className={cn("truncate text-[11px]", j.status === "done" ? "text-dim" : j.status === "cancelled" ? "text-dim" : "text-bad")}>
            {j.status === "done" ? "✓" : j.status === "cancelled" ? "–" : "✗"} {new Date(j.at).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })} {j.label}
            {j.status !== "done" && j.result ? ` — ${j.result}` : ""}
          </p>
        ))}
      </div>
    </div>
  );
}
