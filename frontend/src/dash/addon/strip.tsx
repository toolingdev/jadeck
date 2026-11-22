import { useEffect, useRef } from "react";
import { cn } from "@/lib/cn";
import { Frame } from "../header";
import { bound, connHead, NumTag, objs, resUrl, str, TallyRing, useAddonAction, type ShapeProps } from "./common";

// kind "strip": a row of thumbnails that scrolls sideways (the one place a
// widget scrolls); tap one to go there. Red = live, green = next.
// bind: items (state key: [{id, src, label?, active?, next?}]), action, param? (default "index")
// The param gets the item's position in the list (0-based) when param is
// "index", else the item's id.
// Look (WIDGETS.md §6): like the image shape: number tag top-left, caption
// on a dark gradient, 3px tally ring with a soft glow.

export function StripShape({ def, state, conn, preview, editing }: ShapeProps) {
  const b = def.bind ?? {};
  const items = objs(bound(state, b, "items"));
  const action = str(b.action), param = str(b.param, "index");
  const { run, busy } = useAddonAction(conn, preview);
  const row = useRef<HTMLDivElement>(null);
  const live = items.findIndex((it) => it.active === true);
  useEffect(() => {
    // Keep the live picture in view (scroll the strip only, never the page).
    const el = row.current, tile = el?.children[live] as HTMLElement | undefined;
    if (!el || !tile) return;
    const left = tile.offsetLeft - el.offsetLeft, right = left + tile.offsetWidth;
    if (left < el.scrollLeft || right > el.scrollLeft + el.clientWidth) el.scrollTo({ left: Math.max(0, left - 8), behavior: "smooth" });
  }, [live, items.length]);
  return (
    <Frame title={def.label} {...connHead(conn)}>
      {items.length === 0 ? <p className="text-xs text-dim">Nothing to show.</p> : (
        <div ref={row} data-strip className="flex h-full gap-2.5 overflow-x-auto overflow-y-hidden overscroll-x-contain px-1 pt-1 [container-type:size] [scrollbar-width:thin]">
          {items.map((it, i) => {
            const id = str(it.id, String(i));
            const lbl = str(it.label);
            const src = resUrl(it.src, conn);
            const on = it.active === true, next = !on && it.next === true;
            return (
              <button key={id} type="button" disabled={!action || !!busy || editing} title={lbl || `#${i + 1}`}
                onClick={() => run(action, { [param]: param === "index" ? i : id }, `${def.label}: ${lbl || `#${i + 1}`}`, id)}
                // Height follows the box (room for the glow below); width keeps 16:9.
                className="relative block aspect-video h-[calc(100cqh-6px)] min-h-8 shrink-0 text-left transition active:scale-[0.98] disabled:active:scale-100">
                <span className={cn("relative block h-full w-full overflow-hidden rounded-lg border border-line bg-black", busy === id && "opacity-60")}>
                  {src && <img src={src} alt={lbl} draggable={false} loading="lazy" className="h-full w-full object-contain" />}
                  <NumTag>{i + 1}</NumTag>
                  {lbl && lbl !== String(i + 1) && (
                    <span className="pic-shade pointer-events-none absolute inset-x-0 bottom-0 flex h-9 items-end px-2 pb-1.5">
                      <span className="min-w-0 truncate text-[11.5px] font-bold leading-4 text-[#f4f6f9] [text-shadow:0_1px_2px_rgb(0_0_0/0.6)]">{lbl}</span>
                    </span>
                  )}
                </span>
                <TallyRing tone={on ? "pgm" : next ? "pvw" : ""} radius="8px" />
              </button>
            );
          })}
        </div>
      )}
    </Frame>
  );
}
