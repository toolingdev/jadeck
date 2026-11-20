import { capture } from "@/lib/capture";
import { useEffect, useRef, useState } from "react";
import { Loader2 } from "lucide-react";
import { api } from "@/lib/api";
import { useCam, withCam } from "@/lib/camera";
import { useLog } from "@/lib/log";
import { Button } from "@/components/ui";

type Media = HTMLVideoElement | HTMLImageElement | null;

function contentRect(box: DOMRect, media: Media) {
  const w = media instanceof HTMLVideoElement ? media.videoWidth : media?.naturalWidth || 0;
  const h = media instanceof HTMLVideoElement ? media.videoHeight : media?.naturalHeight || 0;
  if (!w || !h) return { x: 0, y: 0, w: box.width, h: box.height, sw: 16, sh: 9 };
  const s = Math.min(box.width / w, box.height / h);
  return { x: (box.width - w * s) / 2, y: (box.height - h * s) / 2, w: w * s, h: h * s, sw: w, sh: h };
}

/** Pick a white point: tap where it should be white, drag with the loupe
 * to fine-tune, confirm. The camera aims there, balances, returns. */
export function WbPickOverlay({ media, onClose }: { media: Media; onClose: () => void }) {
  const layer = useRef<HTMLDivElement>(null);
  const cam = useCam();
  const loupe = useRef<HTMLCanvasElement>(null);
  const [pt, setPt] = useState<{ x: number; y: number } | null>(null); // normalized 0..1
  const [drag, setDrag] = useState(false);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("Tap something that should be pure white.");
  const { log } = useLog();

  const place = (e: React.PointerEvent) => {
    const box = layer.current!.getBoundingClientRect();
    const c = contentRect(box, media);
    const x = Math.min(1, Math.max(0, (e.clientX - box.left - c.x) / c.w));
    const y = Math.min(1, Math.max(0, (e.clientY - box.top - c.y) / c.h));
    setPt({ x, y });
  };

  useEffect(() => {
    const cv = loupe.current;
    if (!cv || !pt || !media || !drag) return;
    const ctx = cv.getContext("2d");
    if (!ctx) return;
    const c = contentRect(layer.current!.getBoundingClientRect(), media);
    const zoom = 4;
    const sw = cv.width / zoom, sh = cv.height / zoom;
    try {
      ctx.imageSmoothingEnabled = false;
      ctx.drawImage(media, pt.x * c.sw - sw / 2, pt.y * c.sh - sh / 2, sw, sh, 0, 0, cv.width, cv.height);
    } catch {
      /* media not drawable yet */
    }
    ctx.strokeStyle = "#f5a524";
    ctx.lineWidth = 2;
    ctx.beginPath();
    ctx.moveTo(cv.width / 2 - 10, cv.height / 2); ctx.lineTo(cv.width / 2 + 10, cv.height / 2);
    ctx.moveTo(cv.width / 2, cv.height / 2 - 10); ctx.lineTo(cv.width / 2, cv.height / 2 + 10);
    ctx.stroke();
  }, [pt, media, drag]);

  const confirm = async () => {
    if (!pt) return;
    setBusy(true);
    setMsg("Balancing — the camera aims at the point and comes back (about 5 s)…");
    try {
      await api(withCam("/api/camera/wb-pick", cam), "POST", pt);
      log("Picture", "White balance picked and camera returned");
      onClose();
    } catch (e) {
      setMsg(`Didn't work — ${(e as Error).message}`);
      log("Picture", `White-point pick failed — ${(e as Error).message}`, false);
    } finally {
      setBusy(false);
    }
  };

  const box = layer.current?.getBoundingClientRect();
  const c = box ? contentRect(box, media) : null;
  const px = pt && c ? c.x + pt.x * c.w : 0;
  const py = pt && c ? c.y + pt.y * c.h : 0;
  return (
    <div className="absolute inset-0 z-10">
      <div
        ref={layer}
        className="touch-hold absolute inset-0 cursor-crosshair bg-black/10"
        onPointerDown={(e) => { if (busy) return; capture(e); setDrag(true); place(e); }}
        onPointerMove={(e) => drag && place(e)}
        onPointerUp={() => setDrag(false)} onPointerCancel={() => setDrag(false)}
      >
        {pt && (
          <div className="pointer-events-none absolute h-7 w-7 -translate-x-1/2 -translate-y-1/2 rounded-full border-2 border-accent shadow-[0_0_0_2px_rgba(0,0,0,.6)]" style={{ left: px, top: py }} />
        )}
        {pt && drag && (
          <canvas ref={loupe} width={160} height={160}
            className="pointer-events-none absolute h-[120px] w-[120px] -translate-x-1/2 rounded-full border-2 border-accent bg-black shadow-2xl"
            style={{ left: Math.min(Math.max(px, 70), (box?.width ?? 0) - 70), top: Math.max(py - 150, 8) }} />
        )}
      </div>
      <div className="absolute inset-x-2 bottom-2 flex flex-wrap items-center gap-2 rounded-xl border border-line bg-panel/95 p-2.5 backdrop-blur">
        <span className="min-w-0 flex-1 text-xs text-muted">{busy && <Loader2 size={13} className="mr-1 inline animate-spin" />}{msg}</span>
        <Button size="sm" variant="ghost" onClick={onClose} disabled={busy}>Cancel</Button>
        <Button size="sm" variant="accent" onClick={confirm} disabled={!pt || busy}>Balance here</Button>
      </div>
    </div>
  );
}
