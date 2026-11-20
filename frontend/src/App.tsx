import { useEffect, useRef, useState } from "react";
import { LayoutDashboard, Settings as Cog, AlertTriangle, ChevronDown, ChevronUp, Check, Star, X } from "lucide-react";
import { api } from "@/lib/api";
import { setCamera, useCamera } from "@/lib/camera";
import { useCameras } from "@/components/CameraSetup";
import { SetupPage } from "@/pages/SetupPage";
import { cn } from "@/lib/cn";
import { useControl } from "@/lib/control";
import { useLog } from "@/lib/log";
import { Dot } from "@/components/ui";
import { UpdateBanner } from "@/components/UpdateBanner";
import { DashboardPage } from "@/pages/DashboardPage";
import { SettingsPage } from "@/pages/SettingsPage";
import { exitFullScreen, canFullScreen, standalone, useLocked, useWakeLock } from "@/lib/kiosk";
import { Boundary } from "@/components/Boundary";

// Everything you use is a dashboard (the old Camera page is the starter
// "Camera" dashboard); the other place is Settings.
type Route = "/" | "/settings" | "/setup";
const routes: { path: Route; label: string; icon: typeof Cog }[] = [
  { path: "/", label: "Dashboard", icon: LayoutDashboard },
  { path: "/settings", label: "Settings", icon: Cog },
];

function current(): Route {
  const p = location.pathname.replace(/\/+$/, "") || "/";
  if (p === "/setup") return "/setup";
  if (p === "/settings" || p.startsWith("/settings/")) return "/settings";
  return "/";
}

/** Header camera name; a switcher when there is more than one. */
function CameraSwitcher({ fallback, sub, ok }: { fallback: string; sub: string; ok?: boolean }) {
  const { data: cams } = useCameras();
  const cam = useCamera();
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const list = cams ?? [];
  const cur = list.find((c) => c.id === cam) ?? list.find((c) => c.main);
  useEffect(() => {
    if (cams && cam && !cams.some((c) => c.id === cam)) setCamera(""); // removed: back to the main camera
  }, [cams, cam]);
  useEffect(() => {
    if (!open) return;
    const close = (e: PointerEvent) => !ref.current?.contains(e.target as Node) && setOpen(false);
    window.addEventListener("pointerdown", close);
    return () => window.removeEventListener("pointerdown", close);
  }, [open]);
  const many = list.length > 1;
  return (
    <div ref={ref} className="relative min-w-0">
      <button disabled={!many} onClick={() => setOpen((o) => !o)}
        className={cn("flex min-w-0 items-center gap-1 rounded-lg text-left leading-tight", many && "-mx-1.5 px-1.5 py-0.5 hover:bg-raise")}>
        <div className="min-w-0">
          <div className="flex items-center gap-1 truncate text-sm font-semibold">{cur?.name ?? fallback}{many && <ChevronDown size={14} className="text-muted" />}</div>
          <div className={cn("truncate text-[11px] text-dim", ok && "max-sm:hidden")}>{sub}</div>
        </div>
      </button>
      {open && (
        <div className="absolute left-0 top-full z-50 mt-1 min-w-56 rounded-xl border border-line bg-panel p-1 shadow-2xl">
          {list.map((c) => (
            <button key={c.id} onClick={() => { setCamera(c.main ? "" : c.id); setOpen(false); }}
              className="flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-sm hover:bg-raise">
              <Dot on={!!c.status?.connected} />
              <span className="min-w-0 flex-1 truncate">{c.name}</span>
              {c.main && <Star size={12} className="text-accent" />}
              {cur?.id === c.id && <Check size={14} className="text-accent" />}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

export function App() {
  const [route, setRoute] = useState<Route>(current);
  const { status, online, noCamera } = useControl();
  const { toast } = useLog();
  const cam = useCamera();

  // First run: the setup wizard opens by itself.
  useEffect(() => {
    api<{ done: boolean }>("/api/setup").then((r) => {
      if (!r.done && current() !== "/setup") {
        history.replaceState(null, "", "/setup");
        setRoute("/setup");
      }
    }).catch(() => {});
  }, []);

  useEffect(() => {
    const on = () => setRoute(current());
    window.addEventListener("popstate", on);
    return () => window.removeEventListener("popstate", on);
  }, []);
  const go = (p: Route) => {
    if (p !== route) {
      history.pushState(null, "", p);
      setRoute(p);
      window.scrollTo(0, 0);
    }
  };

  const locked = useLocked() && route === "/";
  useWakeLock(locked);

  const camName = status?.device?.devname || "Camera";
  if (locked) {
    return (
      <div className="flex min-h-full flex-col">
        <main key={cam} className="w-full flex-1 p-2 pt-[max(0.5rem,env(safe-area-inset-top))] pb-[max(0.5rem,env(safe-area-inset-bottom))]">
          <Boundary name="Dashboard" full><DashboardPage locked /></Boundary>
        </main>
        <ExitCorner />
      </div>
    );
  }
  return (
    <div className="flex min-h-full flex-col">
      <header className="sticky top-0 z-40 border-b border-line bg-bg/90 backdrop-blur supports-[padding:max(0px)]:pt-[env(safe-area-inset-top)]">
        <div className="mx-auto flex h-14 max-w-[1600px] items-center gap-3 px-3 max-sm:h-11 sm:px-5 short:h-11">
          <div className="flex min-w-0 items-center gap-2.5">
            <Dot on={online && status?.connected} />
            <CameraSwitcher fallback={camName} ok={online && !!status?.connected}
              sub={noCamera ? "no camera set up yet" : !online ? (status === null ? "connecting…" : "controller offline — retrying") : status?.connected ? (status.device?.versioninfo || "connected") : "camera not connected — retrying"} />
          </div>
          <nav className="mx-auto flex rounded-xl border border-line bg-panel p-0.5 max-sm:hidden">
            {routes.map((r) => (
              <button
                key={r.path} onClick={() => go(r.path)}
                className={cn("flex h-9 items-center gap-2 rounded-[10px] px-3.5 text-sm font-medium transition",
                  route === r.path ? "bg-raise text-fg" : "text-muted hover:text-fg")}
              >
                <r.icon size={16} /> {r.label}
              </button>
            ))}
          </nav>
          <div className="ml-auto hidden items-center gap-3 font-mono text-[11px] text-muted tabular md:flex short:hidden">
            <span>PAN <b className="inline-block w-[6ch] text-right text-fg">{status?.pan ?? "—"}</b></span>
            <span>TILT <b className="inline-block w-[6ch] text-right text-fg">{status?.tilt ?? "—"}</b></span>
            <span>ZOOM <b className="inline-block w-[6ch] text-right text-fg">{status?.zoom ?? "—"}</b></span>
            <span className={cn("inline-block w-[8ch] rounded px-1.5 py-0.5 text-center", status?.driving ? "bg-accent/20 text-accent" : "bg-raise")}>
              {status?.driving ? "MOVING" : "STOPPED"}
            </span>
          </div>
        </div>
      </header>

      <UpdateBanner />

      <main key={cam} className="mx-auto w-full max-w-[1600px] flex-1 px-3 pb-20 pt-2 sm:px-5 sm:pb-8 sm:pt-3 short:pt-1.5 short:pb-4">
        {route === "/" && <Boundary name="Dashboard" full><DashboardPage /></Boundary>}
        {route === "/settings" && <Boundary name="Settings" full><SettingsPage /></Boundary>}
        {route === "/setup" && <SetupPage onFinish={() => go("/")} />}
      </main>

      {/* Phone: no tab bar — a handle that brings the tabs up */}
      <PhoneNav route={route} go={go} />

      {toast && (
        <div className="fixed bottom-8 left-1/2 z-50 flex max-w-[92vw] -translate-x-1/2 items-start gap-2 rounded-xl border border-pgm/40 bg-panel-2 px-4 py-3 text-sm shadow-2xl sm:bottom-6">
          <AlertTriangle size={16} className="mt-0.5 shrink-0 text-bad" />
          <span><b className="text-fg">{toast.source}:</b> <span className="text-muted">{toast.text}</span></span>
        </div>
      )}
    </div>
  );
}

/** Full screen's way out: a faint ✕; tap it twice (so a bump on stage
 *  doesn't end it). On iPhone Safari, which can't hide its own bars, a
 *  one-time tip: Add to Home Screen. */
function ExitCorner() {
  const [armed, setArmed] = useState(false);
  const [tip, setTip] = useState(() => {
    if (canFullScreen() || standalone()) return false;
    try { return localStorage.getItem("ptz.fsTip") !== "1"; } catch { return false; }
  });
  useEffect(() => {
    if (!armed) return;
    const t = setTimeout(() => setArmed(false), 3000);
    return () => clearTimeout(t);
  }, [armed]);
  useEffect(() => {
    if (!tip) return;
    try { localStorage.setItem("ptz.fsTip", "1"); } catch { /* ignore */ }
    const t = setTimeout(() => setTip(false), 9000);
    return () => clearTimeout(t);
  }, [tip]);
  return (
    <>
      <div className="fixed right-2 top-[max(0.5rem,env(safe-area-inset-top))] z-50 flex items-center gap-2">
        {armed && <span className="rounded-full bg-panel-2 px-2.5 py-1 text-[11px] font-semibold text-fg shadow">Tap again to exit</span>}
        <button type="button" aria-label="Exit full screen" onClick={() => (armed ? exitFullScreen() : setArmed(true))}
          className={cn("flex h-8 w-8 items-center justify-center rounded-full border transition",
            armed ? "border-accent bg-accent text-bg" : "border-white/20 bg-black/40 text-white/50")}>
          <X size={15} />
        </button>
      </div>
      {tip && (
        <div className="fixed inset-x-3 bottom-[max(0.75rem,env(safe-area-inset-bottom))] z-50 rounded-xl border border-line bg-panel-2 px-4 py-3 text-xs text-muted shadow-2xl" onClick={() => setTip(false)}>
          <b className="text-fg">Hide Safari's bars too:</b> Share → Add to Home Screen, then open jadeck from there.
        </div>
      )}
    </>
  );
}

/** Phones: a small tab with an arrow at the bottom edge (not a bar — that
 *  reads as the iPhone's home indicator). ⌃ or a swipe up brings the tabs
 *  up; ⌄ on the open tray, a swipe down or a tap outside puts them away. */
function PhoneNav({ route, go }: { route: string; go: (p: Route) => void }) {
  const [open, setOpen] = useState(false);
  const y0 = useRef<number | null>(null);
  const swipe = (toOpen: boolean) => ({
    onPointerDown: (e: React.PointerEvent) => { y0.current = e.clientY; },
    onPointerUp: (e: React.PointerEvent) => {
      const d = y0.current === null ? 0 : e.clientY - y0.current;
      y0.current = null;
      if (toOpen ? d < -20 : d > 20) setOpen(toOpen);
    },
  });
  const tab = "flex h-7 w-16 items-center justify-center rounded-t-xl border border-b-0 border-line-strong text-fg";
  return (
    <div className="sm:hidden">
      {!open && (
        <button type="button" aria-label="Show Dashboard and Settings" onClick={() => setOpen(true)} {...swipe(true)}
          className={cn(tab, "fixed bottom-[calc(env(safe-area-inset-bottom)+0.75rem)] left-1/2 z-40 -translate-x-1/2 touch-none rounded-b-xl border-b bg-panel-2/95 shadow-lg backdrop-blur")}>
          <ChevronUp size={18} />
        </button>
      )}
      <div className={cn("fixed inset-0 z-40 bg-black/50 transition-opacity", open ? "opacity-100" : "pointer-events-none opacity-0")} onClick={() => setOpen(false)} />
      <nav {...swipe(false)}
        className={cn("fixed inset-x-0 bottom-0 z-50 touch-none transition-transform duration-200", open ? "translate-y-0" : "translate-y-[calc(100%+2rem)]")}>
        <button type="button" aria-label="Hide" onClick={() => setOpen(false)} className={cn(tab, "mx-auto -mb-px bg-panel")}>
          <ChevronDown size={18} />
        </button>
        <div className="border-t border-line bg-panel px-4 pt-3 pb-[max(1rem,env(safe-area-inset-bottom))] shadow-2xl">
          <div className="grid grid-cols-2 gap-3">
            {routes.map((r) => (
              <button key={r.path} type="button" onClick={() => { setOpen(false); go(r.path); }}
                className={cn("flex h-14 items-center justify-center gap-2 rounded-2xl border text-base font-semibold",
                  route === r.path ? "border-accent bg-accent/15 text-accent" : "border-line bg-bg text-muted")}>
                <r.icon size={20} /> {r.label}
              </button>
            ))}
          </div>
        </div>
      </nav>
    </div>
  );
}
