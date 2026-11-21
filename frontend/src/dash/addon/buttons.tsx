import { useEffect, useRef, useState } from "react";
import { Loader2 } from "lucide-react";
import { cn } from "@/lib/cn";
import { Frame } from "../widgets";
import { HoldButton, holdSeconds } from "./hold";
import { ConnChip, isObj, listOf, num, stateClass, str, useAddonAction, type ShapeProps } from "./common";

// kind "buttons": a grid of equal buttons (WIDGETS.md: equal choices look
// equal; colour is state, not importance).
// bind: items (state key) or buttons (static), action (default), columns?
// item: {id, label, sub?, tone?, active?, disabled?, params?, action?, hold?, holdLabel?}
// hold (seconds, or bind.hold for every button; "$settings.holdSeconds"
// lets a widget setting choose): press and hold to confirm (hold.tsx).
// `sub` is a small second line under the label ("0.5 s", "ON AIR").
// Look (WIDGETS.md §6): raised; red = pgm (on air / muted), green = pvw
// (next), amber = active (selected) or tone "accent" (the primary action),
// flat and dim = disabled.
// Size: a big box is filled: the column count comes from the number of
// buttons and the box's shape (near-square buttons, not slabs), rows share
// the height (48px at least, 160px at most) and the labels grow a little
// with the buttons. A box too short for that behaves as before: 48px rows
// in 96px+ columns, the box grows to fit.

const GAP = 8, MIN_H = 48, MAX_H = 160, MIN_W = 72;

/** The best grid for n buttons in a w×h box, or null when they don't fit
 *  (then the old flowing grid is used and the box grows). */
export function fitGrid(n: number, w: number, h: number, fixedCols = 0, longest = 0): { cols: number; rows: number; bh: number; bw: number } | null {
  if (n < 1 || w <= 0 || h <= 0) return null;
  let best: { cols: number; rows: number; bh: number; bw: number; score: number } | null = null;
  const tries = fixedCols ? [Math.min(fixedCols, 8)] : Array.from({ length: Math.min(n, 8) }, (_, i) => i + 1);
  for (const c of tries) {
    const r = Math.ceil(n / c);
    const bw = (w - (c - 1) * GAP) / c;
    const bh = Math.min(MAX_H, (h - (r - 1) * GAP) / r);
    // The longest label must fit at the base size (≈0.6em a character).
    const needW = Math.max(MIN_W, longest * 13.5 * 0.6 + 20);
    if (bh < MIN_H || (!fixedCols && bw < needW && c > 1)) continue;
    // Bigger buttons win, but a button much wider than ~1.6× its height
    // counts only that wide; fewer empty cells breaks ties.
    const score = Math.min(bw, bh * 1.6) * bh - (c * r - n) * 0.01;
    if (!best || score > best.score) best = { cols: c, rows: r, bh, bw, score };
  }
  if (best) return { cols: best.cols, rows: best.rows, bh: best.bh, bw: best.bw };
  // Nothing fits at full size: shrink rather than scroll. Labels truncate
  // (full text in the tooltip), so ignore their width and go down to 34px
  // rows; only a box too small even for that flows and grows.
  if (!fixedCols) {
    for (const c of tries) {
      const r = Math.ceil(n / c);
      const bw = (w - (c - 1) * GAP) / c;
      const bh = Math.min(MAX_H, (h - (r - 1) * GAP) / r);
      if (bh < 34 || bw < 56) continue;
      const score = Math.min(bw, bh * 1.6) * bh - (c * r - n) * 0.01;
      if (!best || score > best.score) best = { cols: c, rows: r, bh, bw, score };
    }
  }
  return best && { cols: best.cols, rows: best.rows, bh: best.bh, bw: best.bw };
}

export function ButtonsShape({ def, state, conn, preview, editing }: ShapeProps) {
  const b = def.bind ?? {};
  const items = listOf(state, b, Array.isArray(b.buttons) ? "buttons" : "items");
  const cols = b.columns === undefined ? 0 : Math.max(1, Math.min(8, Math.round(num(b.columns, 1))));
  const { run, busy } = useAddonAction(conn, preview);
  const box = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState({ w: 0, h: 0 });
  useEffect(() => {
    const el = box.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setSize((o) => (o.w === el.clientWidth && o.h === el.clientHeight ? o : { w: el.clientWidth, h: el.clientHeight })));
    ro.observe(el);
    return () => ro.disconnect();
  }, [items.length > 0]);
  // Long labels truncate (the full text is the tooltip), so past ~16
  // characters they must not push the grid into one narrow column or the
  // flowing fallback, which left big boxes mostly empty.
  const longest = Math.min(16, Math.max(0, ...items.map((it, i) => [...str(it.label, str(it.id, String(i)))].length)));
  const fit = fitGrid(items.length, size.w, size.h, cols, longest);
  // Label: 13.5px at 48px tall, up to 19px on big buttons, never more than
  // the longest label has room for.
  const font = fit ? Math.max(13.5, Math.min(19, 13.5 + (fit.bh - MIN_H) * 0.06, (fit.bw - 20) / (Math.max(1, longest) * 0.6))) : 13.5;
  return (
    <Frame title={def.label} right={<ConnChip conn={conn} />}>
      {items.length === 0 ? <p className="text-xs text-dim">No buttons right now.</p> : (
        // Grows to fit: fixed-height buttons, the box gets taller instead of scrolling.
        <div ref={box} className="h-full overflow-y-auto">
          <div className={cn("grid content-start gap-2", fit && "h-full")} style={fit ? {
            gridTemplateColumns: `repeat(${fit.cols}, minmax(0, 1fr))`,
            gridTemplateRows: `repeat(${fit.rows}, minmax(${Math.min(MIN_H, Math.floor(fit.bh))}px, 1fr))`,
            maxHeight: fit.rows * MAX_H + (fit.rows - 1) * GAP,
          } : {
            gridTemplateColumns: cols ? `repeat(${cols}, minmax(0, 1fr))` : "repeat(auto-fill, minmax(96px, 1fr))",
            gridAutoRows: `${MIN_H}px`,
          }}>
            {items.map((it, i) => {
              const id = str(it.id, String(i));
              const lbl = str(it.label, id);
              const action = str(it.action) || str(b.action);
              const off = it.disabled === true || !action || !!busy || editing;
              const sub = str(it.sub);
              const hold = holdSeconds(it.hold ?? b.hold);
              const params = isObj(it.params) ? it.params : {};
              if (hold) {
                const tone = str(it.tone);
                return (
                  <HoldButton key={id} seconds={hold} label={lbl} sub={sub} holdLabel={str(it.holdLabel)} tone={tone || "pgm"} font={font}
                    disabled={off} busyIcon={busy === id ? <Loader2 size={14} className="shrink-0 animate-spin" /> : undefined}
                    // idle: raised, edged in the action's colour; the fill shows the colour while held
                    className={cn(it.disabled === true ? "btn-off" : it.active === true ? "btn-sel" : "btn-raised hover:brightness-125",
                      it.disabled !== true && it.active !== true && (tone === "pvw" ? "border-pvw/55" : tone === "accent" ? "border-accent/55" : "border-pgm/55"),
                      busy && busy !== id && "opacity-70")}
                    onFire={() => run(action, params, `${def.label}: ${lbl} (held ${hold} s)`, id)} />
                );
              }
              return (
                <button key={id} type="button" disabled={off} title={sub ? `${lbl} · ${sub}` : lbl}
                  onClick={() => run(action, params, `${def.label}: ${lbl}`, id)}
                  className={cn("flex h-full min-h-0 min-w-0 flex-col items-center justify-center rounded-[10px] border px-2 transition active:scale-[0.98] disabled:active:scale-100",
                    stateClass(str(it.tone), it.active === true, it.disabled === true), busy && busy !== id && "opacity-70")}>
                  <span className="flex w-full min-w-0 items-center justify-center gap-1.5">
                    {busy === id && <Loader2 size={14} className="shrink-0 animate-spin" />}
                    <span className="min-w-0 truncate font-bold leading-[1.25]" style={{ fontSize: font }}>{lbl}</span>
                  </span>
                  {sub && <span className="block w-full truncate text-center font-bold uppercase leading-[1.35] tracking-[0.1em] opacity-60" style={{ fontSize: Math.round(font * 0.67 * 2) / 2 }}>{sub}</span>}
                </button>
              );
            })}
          </div>
        </div>
      )}
    </Frame>
  );
}
