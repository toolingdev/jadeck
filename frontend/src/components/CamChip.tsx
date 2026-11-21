import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Check, ChevronDown, Video } from "lucide-react";
import { useShared } from "@/lib/hooks";
import { useCam, useCamPick } from "@/lib/camera";
import { cn } from "@/lib/cn";

/** The camera a widget controls, as a chip; tap to switch. This screen
 *  remembers the pick; the widget's Camera setting is where it starts.
 *  Hidden when there's only one camera. */
export function CamChip({ className }: { className?: string }) {
  const cam = useCam();
  const pick = useCamPick();
  const { data } = useShared<{ id: string; name: string; main?: boolean }[]>("/api/cameras", 10000);
  const [open, setOpen] = useState<DOMRect | null>(null);
  const ref = useRef<HTMLDivElement>(null);
  const menu = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const close = (e: PointerEvent) => !ref.current?.contains(e.target as Node) && !menu.current?.contains(e.target as Node) && setOpen(null);
    window.addEventListener("pointerdown", close);
    return () => window.removeEventListener("pointerdown", close);
  }, [open]);
  const cams = data ?? [];
  if (cams.length < 2) return null;
  const cur = cams.find((c) => c.id === cam) ?? cams.find((c) => c.main);
  return (
    <div ref={ref} className={cn("no-drag relative", className)}>
      <button type="button" disabled={!pick} onClick={(e) => setOpen(open ? null : e.currentTarget.getBoundingClientRect())}
        className="btn-sel flex max-w-[11rem] items-center gap-1.5 rounded-lg border px-2 py-1 text-[12px] font-bold transition hover:brightness-125">
        <Video size={13} className="shrink-0" />
        <span className="truncate">{cur?.name ?? "Camera"}</span>
        {pick && <ChevronDown size={13} className="shrink-0" />}
      </button>
      {/* in a portal: a short widget would clip it */}
      {open && pick && createPortal(
        <div ref={menu} style={{ left: open.left, top: open.bottom + 4 }} className="fixed z-[70] min-w-44 rounded-xl border border-line bg-panel-2 p-1 shadow-2xl">
          {cams.map((c) => {
            const id = c.main ? "" : c.id;
            const on = c.id === cur?.id;
            return (
              <button key={c.id} type="button" onClick={() => { setOpen(null); pick(id); }}
                className={cn("flex w-full items-center gap-2 whitespace-nowrap rounded-lg px-2.5 py-2 text-left text-sm", on ? "text-accent" : "text-fg hover:bg-raise")}>
                <span className="w-3">{on && <Check size={13} />}</span>{c.name}{c.main && <span className="text-dim">(main)</span>}
              </button>
            );
          })}
        </div>, document.body,
      )}
    </div>
  );
}
