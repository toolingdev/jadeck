import { useEffect, useRef, useState, type ReactNode } from "react";
import { cn } from "@/lib/cn";

// Hold to confirm (design/WIDGETS-V2.md "Hold to confirm", mock
// design/hold-button-mock.png): risky actions (start/stop a stream or a
// recording…) are pressed and held. The button fills left→right in the
// action's colour with a countdown; letting go or sliding off drains it back
// (~300 ms) and sends nothing; at the end it fires once and vibrates where
// it can. Space/Enter held works the same. Everything drawn is absolutely
// positioned, so nothing moves. Under reduced motion the fill still grows
// (it's the progress), only the shimmer stops.

const DRAIN_MS = 300;

/** Seconds from an item / bind value: a number or numeric text, 0.5–30; 0 = no hold. */
export function holdSeconds(v: unknown): number {
  const n = typeof v === "number" ? v : typeof v === "string" && v.trim() !== "" ? Number(v) : NaN;
  return Number.isFinite(n) && n > 0 ? Math.min(30, Math.max(0.5, n)) : 0;
}

const VERBS: Record<string, string> = {
  stop: "STOPS", start: "STARTS", record: "RECORDS", end: "ENDS", cut: "CUTS", go: "GOES", restart: "RESTARTS",
  clear: "CLEARS", reset: "RESETS", delete: "DELETES", remove: "REMOVES", mute: "MUTES", unmute: "UNMUTES", play: "PLAYS",
};
/** "Stop stream" → "STOPS IN 3 S"; anything else → "SENDS IN 3 S". */
function countdown(label: string, left: number) {
  const w = label.trim().split(/\s+/)[0]?.toLowerCase() ?? "";
  return `${VERBS[w] ?? "SENDS"} IN ${Math.max(1, Math.ceil(left))} S`;
}

const FILL: Record<string, string> = {
  pgm: "var(--color-pgm)", pvw: "var(--color-pvw)", accent: "var(--color-accent)",
};

export function HoldButton({ seconds, label, holdLabel, sub, tone, className, disabled, title, font = 13.5, busyIcon, onFire }: {
  seconds: number; label: string; holdLabel?: string; sub?: string; tone: string; className?: string;
  disabled?: boolean; title?: string; font?: number; busyIcon?: ReactNode; onFire: () => void;
}) {
  const [p, setP] = useState(0); // 0..1
  const [held, setHeld] = useState(false);
  const raf = useRef(0);
  const live = useRef({ p: 0, held: false, fired: false });
  const ms = seconds * 1000;
  const set = (v: number) => { live.current.p = v; setP(v); };

  useEffect(() => () => cancelAnimationFrame(raf.current), []);
  // Disabled mid-hold (edit mode, a call in flight): drop it, send nothing.
  useEffect(() => { if (disabled && live.current.held) release(); }, [disabled]); // eslint-disable-line react-hooks/exhaustive-deps

  const start = () => {
    if (disabled || live.current.held) return;
    live.current.held = true;
    live.current.fired = false;
    setHeld(true);
    cancelAnimationFrame(raf.current);
    const t0 = performance.now() - live.current.p * ms;
    const tick = (now: number) => {
      if (!live.current.held) return;
      const v = Math.min(1, (now - t0) / ms);
      set(v);
      if (v >= 1) {
        live.current.held = false;
        live.current.fired = true;
        setHeld(false);
        try { navigator.vibrate?.(40); } catch { /* not on this device */ }
        onFire();
        // Show it full for a moment, then empty.
        raf.current = window.setTimeout(() => set(0), 250) as unknown as number;
        return;
      }
      raf.current = requestAnimationFrame(tick);
    };
    raf.current = requestAnimationFrame(tick);
  };
  const release = () => {
    if (!live.current.held) return;
    live.current.held = false;
    setHeld(false);
    cancelAnimationFrame(raf.current);
    // Drain back from wherever it got to.
    const from = live.current.p, t0 = performance.now(), dur = DRAIN_MS * Math.max(0.3, from);
    const tick = (now: number) => {
      const v = from * (1 - Math.min(1, (now - t0) / dur));
      set(v);
      if (v > 0) raf.current = requestAnimationFrame(tick);
    };
    raf.current = requestAnimationFrame(tick);
  };

  const holding = held || (p > 0 && !live.current.fired);
  const left = seconds * (1 - p);
  const fill = FILL[tone] ?? FILL.pgm;
  const idleSub = `HOLD ${seconds % 1 ? seconds.toFixed(1) : seconds} S${sub ? ` · ${sub}` : ""}`;
  const subSize = Math.round(font * 0.67 * 2) / 2;
  return (
    <button type="button" disabled={disabled} title={title ?? `${label} — press and hold ${seconds} s`}
      aria-label={`${label}, press and hold for ${seconds} seconds`}
      onPointerDown={(e) => { if (e.button === 0) { e.preventDefault(); start(); } }}
      onPointerUp={release} onPointerCancel={release} onPointerLeave={release}
      onKeyDown={(e) => { if ((e.key === " " || e.key === "Enter") && !e.repeat) { e.preventDefault(); start(); } }}
      onKeyUp={(e) => { if (e.key === " " || e.key === "Enter") { e.preventDefault(); release(); } }}
      onBlur={release}
      onContextMenu={(e) => e.preventDefault()}
      onClick={(e) => e.preventDefault()}
      className={cn("touch-hold relative isolate flex h-full min-h-12 min-w-0 flex-col items-center justify-center overflow-hidden rounded-[10px] border px-2", className)}>
      {/* the fill: transform only, so it never moves anything */}
      <span aria-hidden className="pointer-events-none absolute inset-0 -z-10 origin-left overflow-hidden"
        style={{ transform: `scaleX(${p})`, background: `linear-gradient(90deg, color-mix(in srgb, ${fill} 82%, transparent), ${fill})` }}>
        {holding && <span className="hold-shimmer absolute inset-0" />}
      </span>
      <span className={cn("flex w-full min-w-0 items-center justify-center gap-1.5", holding && tone !== "pvw" && tone !== "accent" && "text-white")}>
        {busyIcon}
        <span className="min-w-0 truncate font-bold leading-[1.25]" style={{ fontSize: font }}>{holding ? holdLabel || "Keep holding…" : label}</span>
      </span>
      <span className={cn("block w-full truncate text-center font-bold uppercase leading-[1.35] tracking-[0.1em]", holding ? "opacity-85" : "opacity-60")} style={{ fontSize: subSize }}>
        {holding ? countdown(label, left) : idleSub}
      </span>
    </button>
  );
}
