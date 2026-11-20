import { withConn } from "@/lib/addons";
import { ChevronLeft, ChevronRight, Square, Image as ImageIcon, Eraser, Mic, MicOff } from "lucide-react";
import { api, type SwitcherStatus, type PresentationStatus, type MixerStatus } from "@/lib/api";
import { usePoll, useWidgetConn } from "@/lib/hooks";
import { useLog } from "@/lib/log";
import { cn } from "@/lib/cn";
import { Badge, Button, Card, CardBody, CardHeader, Dot, Empty } from "@/components/ui";

function useAct() {
  const { log } = useLog();
  return async <T,>(source: string, label: string, path: string, body?: unknown): Promise<T | null> => {
    try {
      const r = await api<T>(path, "POST", body);
      log(source, label);
      return r;
    } catch (e) {
      log(source, `${label} failed — ${(e as Error).message}`, false);
      return null;
    }
  };
}

// ------------------------------------------------------------- switcher

export function Switcher() {
  const conn = useWidgetConn();
  const { data: st, setData } = usePoll<SwitcherStatus>(withConn("/api/switcher/status", conn), 2000);
  const act = useAct();
  const name = st?.label || "Switcher";
  const run = async (label: string, path: string, body?: unknown) => {
    const r = await act<SwitcherStatus>(name, label, path, body);
    if (r) setData(r);
  };
  return (
    <Card>
      <CardHeader title={name} meta={st?.driver ? (st.simulated ? "simulated" : st.driver) : undefined}>
        {st?.connected && st.streaming && <Badge tone="pgm" dot>Stream</Badge>}
        {st?.connected && st.recording && <Badge tone="pgm" dot>Rec</Badge>}
        <Dot on={st?.connected} />
      </CardHeader>
      <CardBody className="space-y-3">
        {!st?.connected ? <Empty>{st?.error || "Switcher not answering."}</Empty> : (
          <>
            <div className="grid grid-cols-[repeat(auto-fill,minmax(130px,1fr))] gap-2">
              {st.inputs.map((i) => {
                const pgm = i.number === st.active, pvw = i.number === st.preview;
                return (
                  <button key={i.key || i.number} onClick={() => run(`Preview input ${i.number}`, withConn("/api/switcher/preview", conn), { input: i.number })}
                    className={cn("flex h-16 flex-col items-start justify-center rounded-xl border-2 px-3 text-left transition active:scale-[0.98]",
                      pgm ? "border-pgm bg-pgm/15" : pvw ? "border-pvw bg-pvw/12" : "border-line bg-panel-2 hover:border-line-strong")}>
                    <span className="flex items-center gap-1.5 text-[10px] font-semibold uppercase tracking-[0.08em] text-muted">
                      {i.number}{pgm && <span className="text-pgm">PGM</span>}{pvw && <span className="text-pvw">PVW</span>}
                    </span>
                    <span className="w-full truncate text-sm font-semibold">{i.title}</span>
                  </button>
                );
              })}
            </div>
            <div className="grid grid-cols-2 gap-2">
              <Button size="xl" variant="pgm" onClick={() => run(`CUT — input ${st.preview} to program`, withConn("/api/switcher/cut", conn))}>CUT</Button>
              <Button size="xl" onClick={() => run(`FADE — input ${st.preview} to program`, withConn("/api/switcher/fade", conn), { durationMs: 1000 })}>FADE 1s</Button>
            </div>
          </>
        )}
      </CardBody>
    </Card>
  );
}

// --------------------------------------------------------------- slides

const slideIcons: Record<string, React.ReactNode> = {
  prev: <ChevronLeft size={22} />, next: <ChevronRight size={22} />, black: <Square size={18} fill="currentColor" />,
  logo: <ImageIcon size={18} />, clear: <Eraser size={18} />,
};
const slideLabels: Record<string, string> = { prev: "Prev", next: "Next", black: "Black", logo: "Logo", clear: "Clear text" };

export function Slides() {
  const conn = useWidgetConn();
  const { data: st } = usePoll<PresentationStatus>(withConn("/api/presentation/status", conn), 5000);
  const act = useAct();
  const name = st?.label || "Slides";
  const actions = st?.actions ?? [];
  const main = ["prev", "next"].filter((a) => actions.includes(a));
  const rest = actions.filter((a) => !main.includes(a));
  return (
    <Card>
      <CardHeader title={name} meta={st?.driver ? (st.simulated ? "simulated" : st.driver) : undefined}><Dot on={st?.available} /></CardHeader>
      <CardBody className="space-y-2">
        {!st?.driver ? <Empty>Slides not set up — add a presentation driver in devices.json.</Empty> : (
          <>
            <div className="grid grid-cols-[1fr_2fr] gap-2">
              {main.map((a) => (
                <Button key={a} size="xl" variant={a === "next" ? "accent" : "default"} disabled={!st.available}
                  onClick={() => act(name, slideLabels[a] ?? a, withConn(`/api/presentation/${a}`, conn))}>
                  {a === "prev" && slideIcons.prev}{slideLabels[a]}{a === "next" && slideIcons.next}
                </Button>
              ))}
            </div>
            <div className="grid grid-cols-3 gap-2">
              {rest.map((a) => (
                <Button key={a} size="lg" disabled={!st.available} onClick={() => act(name, slideLabels[a] ?? a, withConn(`/api/presentation/${a}`, conn))}>
                  {slideIcons[a]}{slideLabels[a] ?? a}
                </Button>
              ))}
            </div>
            <p className="truncate text-[11px] text-dim">{st.available ? `via ${st.port}` : st.port}</p>
          </>
        )}
      </CardBody>
    </Card>
  );
}

// ---------------------------------------------------------------- mixer

export function Mixer() {
  const conn = useWidgetConn();
  const { data: st, reload } = usePoll<MixerStatus>(withConn("/api/mixer/status", conn), 3000);
  const act = useAct();
  const name = st?.label || "Audio";
  return (
    <Card>
      <CardHeader title={`${name} · favorites`} meta={st?.simulated ? "simulated" : st?.console ?? undefined}><Dot on={st?.connected} /></CardHeader>
      <CardBody>
        {!st?.configured && <p className="mb-2 text-xs text-muted">{st?.error ?? "Mixer not set up."}</p>}
        {st?.configured && !st.connected && <p className="mb-2 text-xs text-bad">{st.error}</p>}
        <div className="grid grid-cols-[repeat(auto-fill,minmax(130px,1fr))] gap-2">
          {(st?.channels ?? []).map((c) => (
            <button key={c.slot}
              disabled={c.channel == null || !st?.connected}
              onClick={async () => { await act(name, `${c.label} ${c.muted ? "unmuted" : "muted"} (read back)`, withConn("/api/mixer/mute", conn), { slot: c.slot }); reload(); }}
              className={cn("flex h-[76px] flex-col justify-between rounded-xl border-2 p-2.5 text-left transition active:scale-[0.98] disabled:opacity-40",
                c.muted ? "border-pgm/70 bg-pgm/15" : "border-line bg-panel-2 hover:border-line-strong")}>
              <span className="flex w-full items-center gap-1.5">
                <span className="min-w-0 flex-1 truncate text-sm font-semibold">{c.label}</span>
                {c.muted ? <MicOff size={15} className="text-bad" /> : <Mic size={15} className="text-good" />}
              </span>
              <span className="flex w-full items-center justify-between text-[10px] font-semibold uppercase tracking-[0.08em]">
                <span className="text-dim">{c.channel ? `Ch ${c.channel}` : "Unassigned"}</span>
                <span className={c.muted ? "text-bad" : c.muted === false ? "text-good" : "text-dim"}>{c.muted == null ? "—" : c.muted ? "Muted" : "Live"}</span>
              </span>
            </button>
          ))}
        </div>
      </CardBody>
    </Card>
  );
}
