import { useEffect, useRef, useState } from "react";
import { Camera, LayoutDashboard, Settings as Cog, AlertTriangle, ChevronDown, Check, Star } from "lucide-react";
import { api } from "@/lib/api";
import { setCamera, useCamera } from "@/lib/camera";
import { useCameras } from "@/components/CameraSetup";
import { SetupPage } from "@/pages/SetupPage";
import { cn } from "@/lib/cn";
import { useControl } from "@/lib/control";
import { useLog } from "@/lib/log";
import { Dot } from "@/components/ui";
import { UpdateBanner } from "@/components/UpdateBanner";
import { CameraPage } from "@/pages/CameraPage";
import { DashboardPage } from "@/pages/DashboardPage";
import { SettingsPage } from "@/pages/SettingsPage";

type Route = "/" | "/dashboard" | "/settings" | "/setup";
const routes: { path: Route; label: string; icon: typeof Camera }[] = [
  { path: "/", label: "Camera", icon: Camera },
  { path: "/dashboard", label: "Dashboard", icon: LayoutDashboard },
  { path: "/settings", label: "Settings", icon: Cog },
];

function current(): Route {
  const p = location.pathname.replace(/\/+$/, "") || "/";
  if (p === "/setup") return "/setup";
  return (routes.find((r) => r.path === p)?.path ?? "/") as Route;
}

/** Header camera name; a switcher when there is more than one. */
function CameraSwitcher({ fallback, sub }: { fallback: string; sub: string }) {
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
          <div className="truncate text-[11px] text-dim">{sub}</div>
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

  const camName = status?.device?.devname || "Camera";
  return (
    <div className="flex min-h-full flex-col">
      <header className="sticky top-0 z-40 border-b border-line bg-bg/90 backdrop-blur supports-[padding:max(0px)]:pt-[env(safe-area-inset-top)]">
        <div className="mx-auto flex h-14 max-w-[1600px] items-center gap-3 px-3 sm:px-5">
          <div className="flex min-w-0 items-center gap-2.5">
            <Dot on={online && status?.connected} />
            <CameraSwitcher fallback={camName}
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
          <div className="ml-auto hidden items-center gap-3 font-mono text-[11px] text-muted tabular md:flex">
            <span>PAN <b className="text-fg">{status?.pan ?? "—"}</b></span>
            <span>TILT <b className="text-fg">{status?.tilt ?? "—"}</b></span>
            <span>ZOOM <b className="text-fg">{status?.zoom ?? "—"}</b></span>
            <span className={cn("rounded px-1.5 py-0.5", status?.driving ? "bg-accent/20 text-accent" : "bg-raise")}>
              {status?.driving ? "MOVING" : "STOPPED"}
            </span>
          </div>
        </div>
      </header>

      <UpdateBanner />

      <main key={cam} className="mx-auto w-full max-w-[1600px] flex-1 px-3 pb-24 pt-3 sm:px-5 sm:pb-8">
        {route === "/" && <CameraPage />}
        {route === "/dashboard" && <DashboardPage />}
        {route === "/settings" && <SettingsPage />}
        {route === "/setup" && <SetupPage onFinish={() => go("/")} />}
      </main>

      {/* Phone tab bar */}
      <nav className="fixed inset-x-0 bottom-0 z-40 flex border-t border-line bg-bg/95 pb-[env(safe-area-inset-bottom)] backdrop-blur sm:hidden">
        {routes.map((r) => (
          <button
            key={r.path} onClick={() => go(r.path)}
            className={cn("flex flex-1 flex-col items-center gap-0.5 py-2 text-[11px] font-medium", route === r.path ? "text-accent" : "text-muted")}
          >
            <r.icon size={20} /> {r.label}
          </button>
        ))}
      </nav>

      {toast && (
        <div className="fixed bottom-20 left-1/2 z-50 flex max-w-[92vw] -translate-x-1/2 items-start gap-2 rounded-xl border border-pgm/40 bg-panel-2 px-4 py-3 text-sm shadow-2xl sm:bottom-6">
          <AlertTriangle size={16} className="mt-0.5 shrink-0 text-bad" />
          <span><b className="text-fg">{toast.source}:</b> <span className="text-muted">{toast.text}</span></span>
        </div>
      )}
    </div>
  );
}
