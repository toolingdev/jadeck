import { useEffect, useRef, useState } from "react";
import * as RSlider from "@radix-ui/react-slider";
import { cn } from "@/lib/cn";
import { Frame } from "../widgets";
import { bound, ConnChip, isObj, label, num, Readout, str, useAddonAction, type ShapeProps } from "./common";

// kind "slider": a big touch slider (T-bar, volume). Upright when the box
// is taller than wide. Sends while dragging (at most every 100 ms, never
// more than one call in flight; the newest value wins) and always on release.
// bind: value (state key), min? (default 0), max? (default 1), action, param? (default "value"), label?,
// unit? (readout shows the value with this unit, e.g. "dB", instead of a
// percent), ticks? (a number: that many evenly spaced marks; or a scale
// [{value, label?}], e.g. dB marks; a mark at value 0 with a label is
// picked out).
// Look (WIDGETS.md §6): a fader: recessed 6px track, metallic cap with a
// centre line and grip lines, the readout monospace in a recessed pill.

const EVERY_MS = 100;

type Mark = { value: number; label: string };

/** Scale marks from bind.ticks (a count, or [{value, label?}]). */
function marks(t: unknown, min: number, max: number): Mark[] {
  if (typeof t === "number" && Number.isFinite(t) && t >= 1) {
    const n = Math.min(40, Math.round(t));
    return Array.from({ length: n + 1 }, (_, i) => ({ value: min + ((max - min) * i) / n, label: "" }));
  }
  if (!Array.isArray(t)) return [];
  return t.filter(isObj).map((m) => ({ value: num(m.value, NaN), label: str(m.label).slice(0, 6) }))
    .filter((m) => Number.isFinite(m.value) && m.value >= min && m.value <= max).slice(0, 40);
}

const fmt = (v: number) => (Math.abs(v) >= 100 ? v.toFixed(0) : v.toFixed(1));

/** min/max: a number, or a state key holding one. */
const limit = (state: Record<string, unknown>, v: unknown, d: number) => (typeof v === "string" && v in state ? num(state[v], d) : num(v, d));

export function SliderShape({ def, state, conn, preview, editing }: ShapeProps) {
  const b = def.bind ?? {};
  const min = limit(state, b.min, 0), max0 = limit(state, b.max, 1), max = max0 > min ? max0 : min + 1;
  const action = str(b.action), param = str(b.param, "value");
  const lbl = label(state, b, "label");
  const remote = Math.min(max, Math.max(min, num(bound(state, b, "value"), min)));
  const { run } = useAddonAction(conn, preview);

  // While dragging (and a moment after) the thumb follows the finger, not
  // the polled state.
  const [local, setLocal] = useState<number | null>(null);
  const hold = useRef(0);
  const sending = useRef(false), queued = useRef<{ v: number; final: boolean } | null>(null), lastAt = useRef(0), timer = useRef(0);
  useEffect(() => () => { clearTimeout(timer.current); clearTimeout(hold.current); }, []);
  const send = async (v: number, final: boolean) => {
    if (sending.current) { queued.current = { v, final: final || !!queued.current?.final }; return; }
    sending.current = true;
    lastAt.current = Date.now();
    const pct = Math.round(((v - min) / (max - min)) * 100);
    await run(action, { [param]: v }, `${def.label}${lbl ? ` (${lbl})` : ""}: ${pct}%`, "slide", { quiet: !final });
    sending.current = false;
    const q = queued.current;
    queued.current = null;
    if (q && (q.v !== v || (q.final && !final))) send(q.v, q.final);
  };
  const onMove = (v: number) => {
    setLocal(v);
    clearTimeout(hold.current);
    clearTimeout(timer.current);
    const wait = EVERY_MS - (Date.now() - lastAt.current);
    if (wait <= 0) send(v, false);
    else timer.current = window.setTimeout(() => send(v, false), wait);
  };
  const onCommit = (v: number) => {
    clearTimeout(timer.current);
    send(v, true);
    hold.current = window.setTimeout(() => setLocal(null), 1500);
  };

  const box = useRef<HTMLDivElement>(null);
  const [upright, setUpright] = useState(false);
  useEffect(() => {
    const el = box.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setUpright(el.clientHeight > el.clientWidth * 1.2));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const value = local ?? remote;
  const pct = Math.round(((value - min) / (max - min)) * 100);
  const off = !action || editing || (!conn && !preview);
  const unit = label(state, b, "unit").slice(0, 6);
  const scale = marks(b.ticks, min, max);
  const at = (v: number) => `${((v - min) / (max - min)) * 100}%`;
  return (
    <Frame title={lbl ? `${def.label} · ${lbl}` : def.label}
      right={<><ConnChip conn={conn} /><Readout>{unit ? `${value > 0 && min < 0 ? "+" : ""}${fmt(value)} ${unit}` : `${pct}%`}</Readout></>}>
      <div ref={box} className="flex h-full items-center justify-center">
        <RSlider.Root orientation={upright ? "vertical" : "horizontal"} disabled={off}
          value={[value]} min={min} max={max} step={(max - min) / 1000}
          onValueChange={(v) => onMove(v[0])} onValueCommit={(v) => onCommit(v[0])} aria-label={def.label}
          className={cn("relative flex touch-none select-none items-center",
            upright ? "h-full max-h-[480px] w-16 flex-col" : "h-16 w-full max-w-[640px]")}>
          {/* the scale, beside the track (drawn only, no hit area) */}
          {scale.length > 0 && (
            <span aria-hidden className={cn("pointer-events-none absolute", upright ? "inset-y-4 right-[calc(50%+10px)] w-6" : "inset-x-4 bottom-[calc(50%+10px)] h-5")}>
              {scale.map((m, i) => {
                const zero = m.value === 0 && !!m.label;
                return (
                  <span key={i} className={cn("absolute flex items-center gap-1", upright ? "right-0 -translate-y-1/2 flex-row" : "bottom-0 -translate-x-1/2 flex-col-reverse")}
                    style={upright ? { bottom: at(m.value) } : { left: at(m.value) }}>
                    <span className={cn("block rounded-full", zero ? "bg-muted" : "bg-line-strong", upright ? (zero ? "h-[2px] w-2.5" : "h-px w-1.5") : (zero ? "h-2.5 w-[2px]" : "h-1.5 w-px"))} />
                    {m.label && <span className={cn("font-mono text-[8.5px] leading-none tabular", zero ? "font-bold text-muted" : "text-dim", upright && "order-first")}>{m.label}</span>}
                  </span>
                );
              })}
            </span>
          )}
          <RSlider.Track className={cn("recess relative grow overflow-hidden rounded-full border", upright ? "mx-auto w-1.5" : "h-1.5")}>
            <RSlider.Range className={cn("absolute rounded-full bg-accent/45", upright ? "w-full" : "h-full")} />
          </RSlider.Track>
          {/* Metallic cap: same touch size as before (64×32). */}
          <RSlider.Thumb className={cn("fader-cap relative flex items-center justify-center rounded-md transition-[filter] hover:brightness-105 focus:outline-none focus-visible:ring-4 focus-visible:ring-accent/40",
            upright ? "h-8 w-16 flex-col" : "h-16 w-8 flex-row", off && "brightness-75")}>
            <span aria-hidden className={cn("flex items-center justify-center gap-[3px]", upright ? "flex-col" : "flex-row")}>
              <span className={cn("block bg-black/25", upright ? "h-px w-7" : "h-7 w-px")} />
              <span className={cn("block bg-black/70", upright ? "h-[2px] w-10" : "h-10 w-[2px]")} />
              <span className={cn("block bg-black/25", upright ? "h-px w-7" : "h-7 w-px")} />
            </span>
          </RSlider.Thumb>
        </RSlider.Root>
      </div>
    </Frame>
  );
}
