import { useEffect, useRef, useState, type ReactNode } from "react";
import { Maximize2, Minimize2, RefreshCw, VideoOff } from "lucide-react";
import { useStream } from "@/lib/stream";
import { cn } from "@/lib/cn";
import { Badge, Button } from "@/components/ui";

const tierLabel = { connecting: "Connecting…", webrtc: "WebRTC · Live", hls: "HLS · Live", snapshot: "Snapshot preview", nosignal: "No signal", paused: "Paused — not visible" } as const;

/**
 * True while the box can be seen: the tab is showing and the box is on
 * screen. Turning off waits a little (a quick app switch or scroll past
 * doesn't drop and rebuild the stream); turning on is immediate.
 */
function useSeen(ref: React.RefObject<HTMLElement | null>) {
  const [docOn, setDocOn] = useState(() => document.visibilityState === "visible");
  const [onScreen, setOnScreen] = useState(true);
  useEffect(() => {
    let t: number | undefined;
    const on = () => {
      clearTimeout(t);
      if (document.visibilityState === "visible") setDocOn(true);
      else t = window.setTimeout(() => setDocOn(false), 15000);
    };
    document.addEventListener("visibilitychange", on);
    return () => {
      clearTimeout(t);
      document.removeEventListener("visibilitychange", on);
    };
  }, []);
  useEffect(() => {
    const el = ref.current;
    if (!el || typeof IntersectionObserver === "undefined") return;
    let t: number | undefined;
    const io = new IntersectionObserver(([e]) => {
      clearTimeout(t);
      if (e.isIntersecting) setOnScreen(true);
      else t = window.setTimeout(() => setOnScreen(false), 3000);
    });
    io.observe(el);
    return () => {
      clearTimeout(t);
      io.disconnect();
    };
  }, [ref]);
  return docOn && onScreen;
}

export function StreamView({ whep, hls, snapshot, title, noSignalText, noSignalAlert, overlay, className, fullscreen = true, aspect = "aspect-video", onMedia, badges = "top" }: {
  whep?: string; hls?: string; snapshot?: string; title?: ReactNode; noSignalText?: string;
  /** a problem worth acting on while there's no picture (e.g. a sender with the wrong key), shown in warning colors */
  noSignalAlert?: string;
  overlay?: ReactNode; className?: string; fullscreen?: boolean; aspect?: string;
  /** where the name / quality / live badges sit ("bottom" when a picker takes the top-left) */
  badges?: "top" | "bottom";
  onMedia?: (el: HTMLVideoElement | HTMLImageElement | null) => void;
}) {
  const imgRef = useRef<HTMLImageElement>(null);
  const boxRef = useRef<HTMLDivElement>(null);
  const seen = useSeen(boxRef);
  const { videoRef, tier, snapUrl, retry } = useStream({ whep, hls, snapshot, active: seen });
  const [fs, setFs] = useState(false);

  useEffect(() => {
    onMedia?.(tier === "snapshot" ? imgRef.current : tier === "webrtc" || tier === "hls" ? videoRef.current : null);
  }, [tier, onMedia, videoRef]);

  useEffect(() => {
    const on = () => setFs(document.fullscreenElement === boxRef.current);
    document.addEventListener("fullscreenchange", on);
    return () => document.removeEventListener("fullscreenchange", on);
  }, []);
  const toggleFs = async () => {
    const el = boxRef.current;
    if (!el) return;
    if (fs) {
      if (document.fullscreenElement) await document.exitFullscreen().catch(() => {});
      setFs(false);
    } else if (el.requestFullscreen) {
      await el.requestFullscreen().catch(() => setFs(true)); // iOS Safari: CSS fallback
      if (!document.fullscreenElement) setFs(true);
    } else setFs(true);
  };

  const live = tier === "webrtc" || tier === "hls";
  return (
    <div
      ref={boxRef}
      className={cn(
        "group relative overflow-hidden bg-black",
        fs ? "fixed inset-0 z-[55] rounded-none" : cn("rounded-xl", aspect),
        className,
      )}
    >
      <video ref={videoRef} muted playsInline autoPlay className={cn("absolute inset-0 h-full w-full object-contain", !live && "invisible")} />
      {tier === "snapshot" && snapUrl && (
        <img ref={imgRef} src={snapUrl} alt="" className="absolute inset-0 h-full w-full object-contain" draggable={false} />
      )}
      {tier === "nosignal" && (
        <div className="absolute inset-0 flex flex-col items-center justify-center gap-3 p-6 text-center">
          <VideoOff className="text-dim" size={28} />
          <div className="text-xs font-semibold uppercase tracking-[0.12em] text-muted">No signal</div>
          {noSignalAlert ? <p role="status" className="max-w-xs rounded-lg border border-warn/40 bg-warn/10 px-2.5 py-1.5 text-xs text-warn">{noSignalAlert}</p>
            : noSignalText && <p className="max-w-xs text-xs text-dim">{noSignalText}</p>}
          <Button size="sm" variant="outline" onClick={retry}><RefreshCw size={14} /> Retry</Button>
        </div>
      )}
      {/* a dark gradient behind the bottom badges (WIDGETS.md §6 pictures) */}
      {badges === "bottom" && <div aria-hidden className="pic-shade pointer-events-none absolute inset-x-0 bottom-0 h-14 opacity-80" />}
      <div className={cn("pointer-events-none absolute left-2 flex items-center gap-2", badges === "bottom" ? "bottom-2" : "top-2")}>
        {title && <Badge tone="neutral" className="border-white/10 bg-black/65 text-fg">{title}</Badge>}
        <Badge tone={live ? "pgm" : tier === "snapshot" ? "warn" : "neutral"} dot={live} className={live ? "" : "border-white/10 bg-black/65"}>
          {tierLabel[tier]}
        </Badge>
      </div>
      {fullscreen && (
        <button
          onClick={toggleFs} aria-label={fs ? "Exit full screen" : "Full screen"}
          className="absolute right-2 top-2 rounded-lg border border-white/10 bg-black/60 p-[7px] text-fg opacity-80 transition hover:opacity-100"
        >
          {fs ? <Minimize2 size={16} /> : <Maximize2 size={16} />}
        </button>
      )}
      {overlay}
    </div>
  );
}
