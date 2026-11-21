import { ImageOff } from "lucide-react";
import { cn } from "@/lib/cn";
import { Frame } from "../widgets";
import { Badge, bound, ConnChip, isObj, NumTag, resUrl, str, TallyRing, useAddonAction, type ShapeProps } from "./common";

// kind "image": a picture from the add-on (a live slide, a thumbnail) that
// keeps its aspect ratio, with a caption, a badge and a tally ring.
// bind: src, caption?, badge?, tone? (pgm | pvw | none), tag? (short number
// or text, top-left), action?, actionParams?
// Look (WIDGETS.md §6): rounded 10px, caption on a dark gradient at the
// bottom, tag on a black pill top-left, badge pill top-right, 3px tally
// ring with a soft glow.

export function ImageShape({ def, state, conn, preview, editing }: ShapeProps) {
  const b = def.bind ?? {};
  const src = resUrl(bound(state, b, "src"), conn);
  const caption = str(bound(state, b, "caption"));
  const badge = str(bound(state, b, "badge"));
  const tone = str(bound(state, b, "tone"), "none");
  const tag = str(bound(state, b, "tag")).slice(0, 6);
  const action = str(b.action);
  const { run, busy } = useAddonAction(conn, preview);
  const tap = action && !editing ? () => run(action, isObj(b.actionParams) ? b.actionParams : {}, caption ? `${def.label}: ${caption}` : def.label) : undefined;
  const pic = (
    <div className="relative h-full w-full">
    <div className="relative flex h-full w-full items-center justify-center overflow-hidden rounded-[var(--radius-pic)] border border-line bg-black">
      {src ? <img src={src} alt={caption || def.label} draggable={false} className="h-full w-full object-contain" />
        : <span className="flex flex-col items-center gap-1 text-[11px] text-dim"><ImageOff size={20} />No picture</span>}
      {tag && <NumTag>{tag}</NumTag>}
      {badge && <span className="pointer-events-none absolute right-2 top-2 z-[2] flex"><Badge text={badge} tone={tone === "none" ? "pgm" : tone} /></span>}
      {caption && (
        <span className="pic-shade pointer-events-none absolute inset-x-0 bottom-0 z-[1] flex h-12 items-end px-2.5 pb-2">
          <span className="min-w-0 truncate text-[13px] font-bold leading-4 text-[#f4f6f9] [text-shadow:0_1px_2px_rgb(0_0_0/0.6)]">{caption}</span>
        </span>
      )}
    </div>
    {/* Outside the clipped picture so the glow shows. */}
    <TallyRing tone={tone} />
    </div>
  );
  return (
    <Frame title={def.label} right={<ConnChip conn={conn} />}>
      <div className="h-full">
        {tap ? (
          <button type="button" disabled={!!busy} onClick={tap} aria-label={caption || def.label}
            className={cn("block h-full w-full rounded-[var(--radius-pic)] transition active:scale-[0.99] disabled:opacity-70")}>{pic}</button>
        ) : pic}
      </div>
    </Frame>
  );
}
