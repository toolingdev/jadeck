import type { ReactNode } from "react";
import { useShared } from "@/lib/hooks";
import { cn } from "@/lib/cn";
import { openFeeds } from "@/lib/stream";

type Settings = Record<string, unknown>;
const bool = (v: unknown, d = false) => (typeof v === "boolean" ? v : d);

type Feed = { path: string; name: string; online: boolean; source: string; codec: string; width: number; height: number; inKbps: number; outKbps: number; viewers: number };
type Viewer = { kind: string; feed: string; remote: string; kbps: number; since: string; lost: number; connect: string };
type Sender = { path: string; remote: string; kbps: number; rttMs: number; lossRate: number; dropped: number };
type VideoStats = { running: boolean; inKbps: number; outKbps: number; feeds: Feed[]; viewers: Viewer[]; senders: Sender[] };
type HealthState = { ok: boolean; detail?: string; since: string; changes: number };
type Health = {
  version: string; uptime?: string; health: Record<string, HealthState>;
  problemsLastHour?: number; lastProblem?: { at: string; level: string; msg: string; attrs?: Record<string, string> } | null;
  video?: { state: string; error?: string }; stageScreens?: string[];
};

export function rate(kbps: number) {
  if (kbps >= 1000) return `${(kbps / 1000).toFixed(kbps >= 10000 ? 0 : 1)} Mbps`;
  return `${kbps} kbps`;
}

function Box({ title, right, children }: { title: string; right?: ReactNode; children: ReactNode }) {
  return (
    <div className="flex h-full flex-col overflow-hidden rounded-[var(--radius-card)] border border-line bg-panel">
      <div className="flex shrink-0 items-center gap-2 px-3 pt-2.5 pb-1.5">
        <span className="truncate text-[12px] font-semibold">{title}</span>
        <span className="ml-auto text-[11px] text-dim">{right}</span>
      </div>
      <div data-feed className="min-h-0 flex-1 overflow-y-auto px-3 pb-3">{children}</div>
    </div>
  );
}

function Err({ title, msg }: { title: string; msg: string }) {
  return <Box title={title}><p className="pt-3 text-center text-xs text-dim">{/401|403|trusted/i.test(msg) ? "Only on the venue network or Tailscale." : msg}</p></Box>;
}

const since = (iso: string) => {
  const s = Math.max(0, Math.round((Date.now() - new Date(iso).getTime()) / 1000));
  if (s < 90) return `${s}s`;
  if (s < 5400) return `${Math.round(s / 60)} min`;
  return `${Math.round(s / 3600)} h`;
};

// ------------------------------------------------------- video bandwidth

export function BandwidthWidget({ s }: { s: Settings }) {
  const { data, error } = useShared<VideoStats>("/api/diag/video", 2000);
  if (error && !data) return <Err title="Video bandwidth" msg={error} />;
  if (!data) return <Box title="Video bandwidth"><p className="text-xs text-dim">Reading…</p></Box>;
  data.feeds ??= [];
  data.viewers ??= [];
  data.senders ??= [];
  const here = openFeeds();
  if (!data.running) {
    return <Box title="Video bandwidth"><p className="pt-3 text-center text-xs text-dim">The stream gateway isn't running — Settings → Live video.</p></Box>;
  }
  const max = Math.max(1, ...data.feeds.map((f) => f.outKbps + f.inKbps));
  return (
    <Box title="Video bandwidth" right={`this screen: ${here.length} feed${here.length === 1 ? "" : "s"}`}>
      <div className="mb-2 grid grid-cols-2 gap-2">
        <Stat label="To screens" value={rate(data.outKbps)} sub={`${data.viewers.length} viewer${data.viewers.length === 1 ? "" : "s"}`} tone={data.outKbps > 40000 ? "warn" : "ok"} />
        <Stat label="From cameras / vMix" value={rate(data.inKbps)} sub={`${data.feeds.filter((f) => f.online).length} live feed${data.feeds.filter((f) => f.online).length === 1 ? "" : "s"}`} />
      </div>
      <div className="space-y-1.5">
        {data.feeds.map((f) => (
          <div key={f.path} className="rounded-lg bg-panel-2 px-2.5 py-1.5">
            <div className="flex items-center gap-2 text-[12px]">
              <span className={cn("h-2 w-2 shrink-0 rounded-full", f.online ? "bg-good" : "bg-line-strong")} />
              <span className="min-w-0 flex-1 truncate font-semibold">{f.name}</span>
              {f.width > 0 && <span className="text-[10px] text-dim">{f.width}×{f.height}</span>}
              <span className="w-16 text-right tabular text-muted">{f.viewers} 👁</span>
              <span className="w-20 text-right font-mono tabular">{f.online ? rate(f.outKbps) : "idle"}</span>
            </div>
            {f.online && (
              <div className="mt-1 h-1 overflow-hidden rounded bg-raise">
                <div className="h-full rounded bg-info" style={{ width: `${Math.min(100, ((f.outKbps + f.inKbps) / max) * 100)}%` }} />
              </div>
            )}
          </div>
        ))}
        {data.feeds.length === 0 && <p className="text-xs text-dim">No feeds set up.</p>}
      </div>
      {bool(s.senders, true) && data.senders.length > 0 && (
        <div className="mt-3">
          <div className="mb-1 text-[10px] font-bold uppercase tracking-[0.1em] text-dim">From vMix (SRT)</div>
          {data.senders.map((x) => (
            <div key={x.path + x.remote} className="flex items-center gap-2 text-[12px]">
              <span className="flex-1 truncate">{x.path}</span>
              <span className="font-mono tabular">{rate(x.kbps)}</span>
              <span className={cn("tabular", x.rttMs > 50 ? "text-warn" : "text-muted")}>{x.rttMs.toFixed(0)} ms</span>
              <span className={cn("tabular", x.lossRate > 1 ? "text-bad" : "text-muted")}>{x.lossRate.toFixed(1)}% loss</span>
            </div>
          ))}
        </div>
      )}
      {bool(s.viewers, true) && data.viewers.length > 0 && (
        <div className="mt-3">
          <div className="mb-1 text-[10px] font-bold uppercase tracking-[0.1em] text-dim">Who's watching</div>
          {data.viewers.map((v, i) => (
            <div key={i} className="flex items-center gap-2 text-[11px]">
              <span className="w-24 shrink-0 truncate font-mono text-muted">{v.remote.replace(/:\d+$/, "")}</span>
              <span className="min-w-0 flex-1 truncate">{v.feed}</span>
              <span className="text-dim">{v.kind === "webrtc" ? "WebRTC" : "HLS"}</span>
              <span className="w-16 text-right font-mono tabular">{rate(v.kbps)}</span>
              <span className="w-10 text-right text-dim">{since(v.since)}</span>
            </div>
          ))}
        </div>
      )}
    </Box>
  );
}

function Stat({ label, value, sub, tone = "ok" }: { label: string; value: string; sub?: string; tone?: "ok" | "warn" }) {
  return (
    <div className="rounded-lg bg-panel-2 px-2.5 py-2">
      <div className="text-[10px] font-bold uppercase tracking-[0.1em] text-dim">{label}</div>
      <div className={cn("font-mono text-[clamp(16px,2.6vw,24px)] font-bold tabular", tone === "warn" ? "text-warn" : "text-fg")}>{value}</div>
      {sub && <div className="text-[11px] text-muted">{sub}</div>}
    </div>
  );
}

// --------------------------------------------------------- system health

export function HealthWidget({ s }: { s: Settings }) {
  const { data, error } = useShared<Health>("/api/diag/health", 5000);
  if (error && !data) return <Err title="System health" msg={error} />;
  if (!data) return <Box title="System health"><p className="text-xs text-dim">Reading…</p></Box>;
  const rows = Object.entries(data.health).sort(([a], [b]) => a.localeCompare(b));
  const down = rows.filter(([, h]) => !h.ok).length;
  return (
    <Box title="System health" right={`v${data.version.replace(/^v/, "")} · up ${data.uptime ?? "?"}`}>
      <div className={cn("mb-2 rounded-lg px-2.5 py-1.5 text-sm font-bold", down ? "bg-pgm/15 text-bad" : "bg-good/10 text-good")}>
        {down ? `${down} not answering` : "Everything answering"}
      </div>
      <div className="space-y-1">
        {rows.map(([name, h]) => (
          <div key={name} className="flex items-center gap-2 text-[12px]">
            <span className={cn("h-2 w-2 shrink-0 rounded-full", h.ok ? "bg-good" : "bg-bad")} />
            <span className="min-w-0 flex-1 truncate capitalize">{name}</span>
            <span className={cn("truncate text-[11px]", h.ok ? "text-dim" : "text-bad")}>{h.ok ? `up ${since(h.since)}` : `down ${since(h.since)}${h.detail ? ` · ${h.detail}` : ""}`}</span>
          </div>
        ))}
        {data.video && (
          <div className="flex items-center gap-2 text-[12px]">
            <span className={cn("h-2 w-2 shrink-0 rounded-full", data.video.state === "running" ? "bg-good" : data.video.state === "off" ? "bg-line-strong" : "bg-bad")} />
            <span className="flex-1">Stream gateway</span>
            <span className="text-[11px] text-dim">{data.video.state}{data.video.error ? ` · ${data.video.error}` : ""}</span>
          </div>
        )}
        {data.stageScreens && (
          <div className="flex items-center gap-2 text-[12px]">
            <span className={cn("h-2 w-2 shrink-0 rounded-full", data.stageScreens.length ? "bg-good" : "bg-line-strong")} />
            <span className="flex-1">Stage screens</span>
            <span className="truncate text-[11px] text-dim">{data.stageScreens.length ? data.stageScreens.join(", ") : "none online"}</span>
          </div>
        )}
      </div>
      {bool(s.problems, true) && (
        <div className="mt-3 border-t border-line pt-2 text-[11px]">
          <span className={data.problemsLastHour ? "text-warn" : "text-dim"}>{data.problemsLastHour ?? 0} problem{data.problemsLastHour === 1 ? "" : "s"} in the last hour</span>
          {data.lastProblem && (
            <div className="mt-0.5 truncate text-muted">
              Last: {new Date(data.lastProblem.at).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })} · {data.lastProblem.msg}
              {data.lastProblem.attrs?.reason ? ` — ${data.lastProblem.attrs.reason}` : data.lastProblem.attrs?.path ? ` ${data.lastProblem.attrs.path}` : ""}
            </div>
          )}
        </div>
      )}
    </Box>
  );
}
