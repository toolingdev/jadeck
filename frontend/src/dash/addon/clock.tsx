import { useEffect, useState } from "react";
import { cn } from "@/lib/cn";
import { Frame } from "../header";
import { bound, connHead, label, Readout, str, type ShapeProps } from "./common";

// kind "clock": the wall clock, plus a count up from / down to `at`.
// bind: at? (state key: ISO time), label? (key or text)

function useNow(ms = 1000) {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const t = setInterval(() => setNow(new Date()), ms);
    return () => clearInterval(t);
  }, [ms]);
  return now;
}

export function span(secs: number) {
  const a = Math.abs(Math.round(secs));
  const h = Math.floor(a / 3600), m = Math.floor((a % 3600) / 60), s = a % 60;
  return `${h ? `${h}:${String(m).padStart(2, "0")}` : m}:${String(s).padStart(2, "0")}`;
}

export function ClockShape({ def, state, conn }: ShapeProps) {
  const now = useNow();
  const b = def.bind ?? {};
  const atRaw = str(bound(state, b, "at"));
  const at = atRaw ? new Date(atRaw) : null;
  const valid = !!at && !Number.isNaN(at.getTime());
  const lbl = label(state, b, "label");
  const diff = valid ? (now.getTime() - at!.getTime()) / 1000 : 0; // > 0: since, < 0: until
  const hm = (d: Date) => d.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
  return (
    <Frame title={def.label} {...connHead(conn)}>
      <div className="flex h-full min-w-0 flex-col justify-center gap-1 [container-type:size]">
        {/* Scales with the box, never wraps out of it. */}
        <div className="truncate font-mono text-[clamp(22px,min(40cqh,18cqw),72px)] font-bold leading-none tracking-[-0.02em] tabular">
          {now.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}
        </div>
        {valid && (
          <>
            <div className="truncate text-[9.5px] font-bold uppercase leading-[18px] tracking-[0.12em] text-dim">{lbl || (diff >= 0 ? "Started" : "Starts")} {hm(at!)}</div>
            <div className="flex min-w-0">
              <Readout className={cn("text-[12px]", diff >= 0 ? "text-muted" : -diff < 120 ? "text-accent" : "text-fg")}>
                {diff >= 0 ? `+${span(diff)}` : `−${span(diff)}`}
              </Readout>
            </div>
          </>
        )}
        {!valid && lbl && <div className="truncate text-[9.5px] font-bold uppercase leading-[18px] tracking-[0.12em] text-dim">{lbl}</div>}
      </div>
    </Frame>
  );
}
