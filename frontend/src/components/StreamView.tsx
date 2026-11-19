import { useEffect, useRef, useState, type ReactNode } from "react";
import { Maximize2, Minimize2, RefreshCw, VideoOff } from "lucide-react";
import { useStream } from "@/lib/stream";
import { cn } from "@/lib/cn";
import { Badge, Button } from "@/components/ui";

const tierLabel = { connecting: "Connecting…", webrtc: "WebRTC · Live", hls: "HLS · Live", snapshot: "Snapshot preview", nosignal: "No signal" } as const;

export function StreamView({ whep, hls, snapshot, title, noSignalText, overlay, className, fullscreen = true, aspect = "aspect-video", onMedia }: {
  whep?: string; hls?: string; snapshot?: string; title?: ReactNode; noSignalText?: string;
  overlay?: ReactNode; className?: string; fullscreen?: boolean; aspect?: string;
  onMedia?: (el: HTMLVideoElement | HTMLImageElement | null) => void;
}) {
  const { videoRef, tier, snapUrl, retry } = useStream({ whep, hls, snapshot });
  const imgRef = useRef<HTMLImageElement>(null);
  const boxRef = useRef<HTMLDivElement>(null);
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
          {noSignalText && <p className="max-w-xs text-xs text-dim">{noSignalText}</p>}
          <Button size="sm" variant="outline" onClick={retry}><RefreshCw size={14} /> Retry</Button>
        </div>
      )}
      <div className="pointer-events-none absolute left-2 top-2 flex items-center gap-2">
        {title && <Badge tone="neutral" className="bg-black/60">{title}</Badge>}
        <Badge tone={live ? "pgm" : tier === "snapshot" ? "warn" : "neutral"} dot={live} className={live ? "" : "bg-black/60"}>
          {tierLabel[tier]}
        </Badge>
      </div>
      {fullscreen && (
        <button
          onClick={toggleFs} aria-label={fs ? "Exit full screen" : "Full screen"}
          className="absolute right-2 top-2 rounded-lg bg-black/60 p-2 text-fg opacity-80 transition hover:opacity-100"
        >
          {fs ? <Minimize2 size={16} /> : <Maximize2 size={16} />}
        </button>
      )}
      {overlay}
    </div>
  );
}
