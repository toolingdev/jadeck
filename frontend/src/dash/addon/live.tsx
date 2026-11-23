import { useEffect, useState, type CSSProperties } from "react";

// A button's live line: a short value under its name that keeps itself
// current, e.g. "away 1:42" (time since), "service in 12:30" (time until)
// or "23 watching" (plain text). Add-on buttons send it as `live`:
//   {text}            plain text
//   {prefix, since}   time since a moment (ms since 1970), counting up
//   {prefix, until}   time until a moment, counting down; `after` once past
// Only buttons where it helps get one (design page 37).
export type Live = { text?: string; prefix?: string; since?: number; until?: number; after?: string };

export function liveOf(v: unknown): Live | null {
  if (!v || typeof v !== "object") return null;
  const o = v as Record<string, unknown>;
  const n = (x: unknown) => (typeof x === "number" && isFinite(x) ? x : undefined);
  const s = (x: unknown) => (typeof x === "string" && x.trim() ? x.trim() : undefined);
  const l: Live = { text: s(o.text), prefix: s(o.prefix), since: n(o.since), until: n(o.until), after: s(o.after) };
  return l.text || l.since !== undefined || l.until !== undefined ? l : null;
}

/** m:ss under an hour, h:mm:ss after. */
export function clock(ms: number) {
  const t = Math.max(0, Math.floor(ms / 1000));
  const h = Math.floor(t / 3600), m = Math.floor((t % 3600) / 60), sec = t % 60;
  const ss = String(sec).padStart(2, "0");
  return h ? `${h}:${String(m).padStart(2, "0")}:${ss}` : `${m}:${ss}`;
}

export function liveText(l: Live, now: number): string {
  if (l.since !== undefined) return [l.prefix, clock(now - l.since)].filter(Boolean).join(" ");
  if (l.until !== undefined) return l.until > now ? [l.prefix, clock(l.until - now)].filter(Boolean).join(" ") : l.after ?? "";
  return l.text ?? "";
}

export function LiveLine({ live, className, style }: { live: Live | null; className?: string; style?: CSSProperties }) {
  const ticking = !!live && (live.since !== undefined || live.until !== undefined);
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!ticking) return;
    setNow(Date.now());
    const t = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(t);
  }, [ticking]);
  if (!live) return null;
  const text = liveText(live, now);
  if (!text) return null;
  return <span className={className} style={style}>{text}</span>;
}
