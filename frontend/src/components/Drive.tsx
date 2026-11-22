import { capture } from "@/lib/capture";
import { useRef, useState, type ReactNode } from "react";
import { ArrowUp, ArrowDown, ArrowLeft, ArrowRight, ArrowUpLeft, ArrowUpRight, ArrowDownLeft, ArrowDownRight, Home, Square, Plus, Minus } from "lucide-react";
import { setDrive, stopDrive, getSpeed } from "@/lib/control";
import { api } from "@/lib/api";
import { useCam, withCam } from "@/lib/camera";
import { useLog } from "@/lib/log";
import { cn } from "@/lib/cn";
import { Button, Slider, Switch } from "@/components/ui";

type Vec = { pan?: number; tilt?: number; zoom?: number };

/** Press-and-hold: drive on press, one stop on release (never repeated jogs). */
export function HoldButton({ vec, children, className, label }: { vec: Vec; children: ReactNode; className?: string; label: string }) {
  const [held, setHeld] = useState(false);
  const cam = useCam();
  const start = (e: React.PointerEvent) => {
    e.preventDefault();
    capture(e);
    setHeld(true);
    setDrive({ pan: vec.pan ?? 0, tilt: vec.tilt ?? 0, zoom: vec.zoom ?? 0 }, cam);
  };
  const end = () => {
    if (!held) return;
    setHeld(false);
    stopDrive(cam);
  };
  return (
    <button
      type="button" aria-label={label}
      onPointerDown={start} onPointerUp={end} onPointerCancel={end} onLostPointerCapture={end}
      onContextMenu={(e) => e.preventDefault()}
      className={cn(
        "touch-hold flex items-center justify-center rounded-2xl border transition active:scale-[0.97]",
        held ? "btn-sel" : "btn-raised hover:brightness-125",
        className,
      )}
    >
      {children}
    </button>
  );
}

const pad: { vec: Vec; icon: ReactNode; label: string }[] = [
  { vec: { pan: -1, tilt: 1 }, icon: <ArrowUpLeft />, label: "Up left" },
  { vec: { tilt: 1 }, icon: <ArrowUp />, label: "Up" },
  { vec: { pan: 1, tilt: 1 }, icon: <ArrowUpRight />, label: "Up right" },
  { vec: { pan: -1 }, icon: <ArrowLeft />, label: "Left" },
  { vec: {}, icon: null, label: "home" },
  { vec: { pan: 1 }, icon: <ArrowRight />, label: "Right" },
  { vec: { pan: -1, tilt: -1 }, icon: <ArrowDownLeft />, label: "Down left" },
  { vec: { tilt: -1 }, icon: <ArrowDown />, label: "Down" },
  { vec: { pan: 1, tilt: -1 }, icon: <ArrowDownRight />, label: "Down right" },
];

export function useCamAction() {
  const { log } = useLog();
  const cam = useCam();
  return async (label: string, path: string, body?: unknown) => {
    try {
      await api(withCam(path, cam), "POST", body);
      log("Camera", label);
      return true;
    } catch (e) {
      log("Camera", `${label} failed — ${(e as Error).message}`, false);
      return false;
    }
  };
}

export function DirPad({ compact, home = true, className }: { compact?: boolean; home?: boolean; className?: string }) {
  const act = useCamAction();
  return (
    <div className={cn("grid grid-cols-3 gap-2", compact ? "w-[180px]" : "w-full max-w-[300px]", className)}>
      {pad.map((p) =>
        p.label === "home" && !home ? <div key="home" aria-hidden /> : p.label === "home" ? (
          <button
            key="home" type="button" aria-label="Home"
            onClick={() => act("Home", "/api/ptz/home")}
            className="btn-raised flex aspect-square items-center justify-center rounded-2xl border !text-muted transition hover:brightness-125 hover:!text-fg active:scale-[0.97]"
          >
            <Home size={compact ? 18 : 22} />
          </button>
        ) : (
          <HoldButton key={p.label} vec={p.vec} label={p.label} className="aspect-square [&>svg]:size-6">
            {p.icon}
          </HoldButton>
        ),
      )}
    </div>
  );
}

const ARROWS: Record<string, string> = {
  "0,1": "↑", "1,1": "↗", "1,0": "→", "1,-1": "↘", "0,-1": "↓", "-1,-1": "↙", "-1,0": "←", "-1,1": "↖",
};

/** Joystick: push farther, go faster, up to the top speed. Dead zone in
 * the middle; snaps back (and the camera stops) on release. Home is a
 * separate button, so the centre is never a button. */
export function Joystick({ speed }: { speed: number }) {
  const zone = useRef<HTMLDivElement>(null);
  const [knob, setKnob] = useState({ x: 0, y: 0, active: false });
  const [dir, setDir] = useState({ pan: 0, tilt: 0, speed: 0 });
  const held = useRef(false); // immediate, unlike state: moves right after the press count
  const cam = useCam();
  const apply = (e: React.PointerEvent) => {
    const r = zone.current!.getBoundingClientRect();
    const rad = r.width / 2;
    const travel = 0.62; // knob centre travels to 62% of the radius
    let dx = (e.clientX - (r.left + rad)) / (rad * travel);
    let dy = (e.clientY - (r.top + rad)) / (rad * travel);
    const m = Math.hypot(dx, dy);
    if (m > 1) {
      dx /= m;
      dy /= m;
    }
    const mag = Math.min(1, m);
    setKnob({ x: dx, y: dy, active: true });
    const dead = 0.12;
    if (mag < dead) {
      setDir({ pan: 0, tilt: 0, speed: 0 });
      return setDrive({ pan: 0, tilt: 0, speed }, cam);
    }
    const ang = Math.atan2(-dy, dx); // up = +
    const pan = Math.abs(Math.cos(ang)) > 0.38 ? Math.sign(Math.cos(ang)) : 0;
    const tilt = Math.abs(Math.sin(ang)) > 0.38 ? Math.sign(Math.sin(ang)) : 0;
    const scaled = Math.max(5, Math.round(speed * ((mag - dead) / (1 - dead))));
    setDir({ pan, tilt, speed: scaled });
    setDrive({ pan, tilt, speed: scaled }, cam);
  };
  const end = () => {
    held.current = false;
    setKnob({ x: 0, y: 0, active: false });
    setDir({ pan: 0, tilt: 0, speed: 0 });
    stopDrive(cam);
    setDrive({ speed }, cam);
  };
  const moving = dir.pan !== 0 || dir.tilt !== 0;
  const lit = (p: number, t: number) => moving && ((p !== 0 && dir.pan === p) || (t !== 0 && dir.tilt === t));
  const tri = (on: boolean) => (on ? "var(--color-accent)" : "var(--color-dim)");
  // knob centre in percent of the zone
  const kx = 50 + knob.x * 31, ky = 50 + knob.y * 31;
  return (
    <div
      ref={zone}
      onPointerDown={(e) => {
        capture(e);
        held.current = true;
        apply(e);
      }}
      onPointerMove={(e) => held.current && apply(e)}
      onPointerUp={end} onPointerCancel={end} onLostPointerCapture={end}
      className="touch-hold relative aspect-square w-full max-w-[280px] cursor-grab active:cursor-grabbing"
      role="slider" aria-label="Joystick: drag to move the camera"
      aria-valuetext={moving ? `${ARROWS[`${dir.pan},${dir.tilt}`]} speed ${dir.speed}` : "centred"}
    >
      {/* base */}
      <div
        className={cn("absolute inset-0 rounded-full border transition-colors", moving ? "border-accent/35" : "border-line-strong")}
        style={{ background: "radial-gradient(circle at 50% 45%, #222831 0%, #14171c 72%)", boxShadow: "inset 0 6px 18px rgba(0,0,0,.6)" }}
      />
      <svg viewBox="0 0 240 240" className="pointer-events-none absolute inset-0 h-full w-full">
        <circle cx="120" cy="120" r="108" fill="none" stroke="var(--color-line)" />
        <circle cx="120" cy="120" r="60" fill="none" stroke="var(--color-line-strong)" strokeDasharray="3 6" />
        <line x1="120" y1="24" x2="120" y2="216" stroke="var(--color-line)" />
        <line x1="24" y1="120" x2="216" y2="120" stroke="var(--color-line)" />
        <path d="M114 16 L120 8 L126 16 Z" fill={tri(lit(0, 1))} />
        <path d="M114 224 L120 232 L126 224 Z" fill={tri(lit(0, -1))} />
        <path d="M16 114 L8 120 L16 126 Z" fill={tri(lit(-1, 0))} />
        <path d="M224 114 L232 120 L224 126 Z" fill={tri(lit(1, 0))} />
        {[[-1, 1], [1, 1], [-1, -1], [1, -1]].map(([p, t]) => {
          const on = moving && dir.pan === p && dir.tilt === t;
          return <circle key={`${p}${t}`} cx={120 + p * 72} cy={120 - t * 72} r={on ? 4 : 2} fill={on ? "var(--color-accent)" : "#434b57"} />;
        })}
        <circle cx="120" cy="120" r="14" fill="#0e1014" stroke="var(--color-line)" />
        {knob.active && (
          <line x1="120" y1="120" x2={(kx / 100) * 240} y2={(ky / 100) * 240} stroke="var(--color-accent)" strokeWidth="3" strokeLinecap="round" opacity={moving ? 0.55 : 0} />
        )}
      </svg>
      {/* live chip */}
      {moving && (
        <div className="pointer-events-none absolute left-[3%] top-[3%] rounded-full border border-accent/40 bg-accent/12 px-2.5 py-1 font-mono text-[10px] font-bold text-accent tabular">
          {ARROWS[`${dir.pan},${dir.tilt}`]} speed {dir.speed}
        </div>
      )}
      {/* knob */}
      <div
        className={cn("pointer-events-none absolute h-[37%] w-[37%] -translate-x-1/2 -translate-y-1/2 rounded-full border-2", moving ? "border-accent" : "border-[#3a424e]")}
        style={{
          left: `${kx}%`, top: `${ky}%`,
          transition: knob.active ? "border-color 120ms" : "left 160ms cubic-bezier(.2,.9,.3,1.2), top 160ms cubic-bezier(.2,.9,.3,1.2), border-color 160ms",
          background: moving ? "linear-gradient(180deg,#33302a,#1a1a18)" : "linear-gradient(180deg,#2e3540,#171b21)",
          boxShadow: moving
            ? "0 0 22px rgba(245,165,36,.35), 0 10px 20px rgba(0,0,0,.7)"
            : "0 10px 20px rgba(0,0,0,.7)",
        }}
      >
        <div className={cn("absolute inset-[25%] rounded-full border", moving ? "border-accent/60" : "border-[#434b57]")}
          style={{ background: moving ? "radial-gradient(circle,#2a2418,#3a3020)" : "radial-gradient(circle,#1b1f26,#262c35)" }} />
        <div className="absolute left-[21%] right-[21%] top-[9%] h-[14%] rounded-full bg-white/[0.07]" />
      </div>
    </div>
  );
}

export function ZoomStop({ vertical = true, home = false }: { vertical?: boolean; home?: boolean }) {
  const act = useCamAction();
  const cam = useCam();
  const h = home ? "h-14" : "h-16";
  return (
    <div className={cn("flex gap-2", vertical ? "flex-col" : "flex-row")}>
      <HoldButton vec={{ zoom: 1 }} label="Tele, zoom in" className={cn(h, "w-16 flex-col text-[10px] font-semibold")}><Plus size={20} />TELE</HoldButton>
      {home && <HoldButton vec={{ zoom: -1 }} label="Wide, zoom out" className={cn(h, "w-16 flex-col text-[10px] font-semibold")}><Minus size={20} />WIDE</HoldButton>}
      <button
        type="button" aria-label="Stop" onClick={() => { stopDrive(cam); act("Stop", "/api/ptz/stop"); }}
        className={cn(h, "btn-pgm flex w-16 items-center justify-center rounded-2xl border transition hover:brightness-110 active:scale-[0.97]")}
      >
        <Square size={20} fill="currentColor" />
      </button>
      {home ? (
        <button
          type="button" aria-label="Home" onClick={() => act("Home", "/api/ptz/home")}
          className="btn-raised flex h-14 w-16 flex-col items-center justify-center gap-0.5 rounded-2xl border text-[10px] font-bold tracking-[0.1em] !text-muted transition hover:brightness-125 hover:!text-fg active:scale-[0.97]"
        >
          <Home size={18} className="text-fg" />HOME
        </button>
      ) : (
        <HoldButton vec={{ zoom: -1 }} label="Wide, zoom out" className="h-16 w-16 flex-col text-[10px] font-semibold"><Minus size={20} />WIDE</HoldButton>
      )}
    </div>
  );
}

export const FINE_SPEED = 15;

/** Speed slider plus Fine: Fine caps the top speed for small framing
 * corrections without leaving the current mode. */
export function SpeedControl({ speed, onChange, fine, onFine, label = "Speed" }: {
  speed: number; onChange: (v: number) => void; fine: boolean; onFine: (v: boolean) => void; label?: string;
}) {
  const eff = fine ? Math.min(FINE_SPEED, speed) : speed;
  const cam = useCam();
  return (
    <div>
      <div className="mb-1 flex items-center gap-2 text-xs">
        <span className="smallcaps">{label}</span>
        <span className={cn("recess rounded-md border px-1.5 font-mono font-bold leading-[16px] tabular", fine ? "text-accent" : "text-fg")}>{fine ? `${eff} · fine` : speed}</span>
        <label className="ml-auto flex cursor-pointer items-center gap-2">
          <span className={cn("font-semibold", fine ? "text-fg" : "text-muted")}>Fine</span>
          <Switch checked={fine} onChange={(v) => { onFine(v); setDrive({ speed: v ? Math.min(FINE_SPEED, speed) : speed }, cam); }} label="Fine movement" />
        </label>
      </div>
      <Slider value={eff} min={5} max={fine ? FINE_SPEED : 100}
        onChange={(v) => { if (!fine) onChange(v); setDrive({ speed: v }, cam); }} label="Movement speed" />
      <div className="flex justify-between text-[10px] font-bold uppercase tracking-[0.12em] text-dim"><span>Precise</span><span>{fine ? "Fine max" : "Fast"}</span></div>
    </div>
  );
}

/** Focus: auto / manual, hold near/far, one-push. */
export function FocusControl({ auto }: { auto: boolean | null }) {
  const act = useCamAction();
  const cam = useCam();
  const focus = withCam("/api/focus", cam);
  const hold = (action: "near" | "far") => ({
    onPointerDown: (e: React.PointerEvent) => {
      capture(e);
      api(focus, "POST", { action }).catch(() => {});
    },
    onPointerUp: () => api(focus, "POST", { action: "stop" }).catch(() => {}),
    onPointerCancel: () => api(focus, "POST", { action: "stop" }).catch(() => {}),
  });
  return (
    <div className="grid grid-cols-4 gap-2">
      <Button variant={auto ? "accent" : "default"} onClick={() => act(auto ? "Manual focus" : "Auto focus", "/api/focus", { action: auto ? "manual" : "auto" })}>
        {auto ? "AF" : "MF"}
      </Button>
      <Button className="touch-hold" disabled={!!auto} {...hold("near")}>Near</Button>
      <Button className="touch-hold" disabled={!!auto} {...hold("far")}>Far</Button>
      <Button onClick={() => act("One-push focus", "/api/focus", { action: "onepush" })}>Push</Button>
    </div>
  );
}

export { getSpeed };
