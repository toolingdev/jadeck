import { useState } from "react";
import { BookOpen, ChevronDown, ChevronRight, Clock, Image, Megaphone, Music, Play, Presentation, Text, type LucideIcon } from "lucide-react";
import { cn } from "@/lib/cn";
import { Frame } from "../widgets";
import { bound, ConnChip, objs, str, useAddonAction, type ShapeProps } from "./common";

// kind "sections": a list folded into sections (a rundown). The current row
// is amber and shows its `sub` line. Tap a row to go there; tap a section
// title to fold it. The section holding the current row starts open.
// bind: sections (state key: [{id, title, meta?, open?, items: [{id, title, icon?, sub?, active?}]}]),
// action, param? (default "id"; gets the row's id), current? (state key: the current row's id)

const ICONS: Record<string, LucideIcon> = {
  song: Music, music: Music, audio: Music, video: Play, play: Play, image: Image, picture: Image,
  slide: Presentation, slides: Presentation, presentation: Presentation, text: Text, note: Text,
  scripture: BookOpen, bible: BookOpen, book: BookOpen, announcement: Megaphone, timer: Clock, clock: Clock,
};

// Icons sit in a small tile so the titles line up (22px wide, same as before).
function RowIcon({ icon, on }: { icon: string; on: boolean }) {
  const I = ICONS[icon.toLowerCase()];
  const tile = cn("flex h-[18px] w-[18px] shrink-0 items-center justify-center rounded-[5px] border",
    on ? "border-accent/40 bg-accent/15 text-accent" : "border-line bg-black/25 text-muted");
  if (I) return <span className={tile}><I size={11} strokeWidth={2.25} /></span>;
  // A glyph or emoji the add-on chose (♪, ✝); longer words aren't shown.
  return [...icon].length <= 2 && icon ? <span className={cn(tile, "text-[11px] leading-none")}>{icon}</span> : <span className="w-[18px] shrink-0" />;
}

export function SectionsShape({ def, state, conn, preview, editing }: ShapeProps) {
  const b = def.bind ?? {};
  const sections = objs(bound(state, b, "sections"));
  const cur = str(bound(state, b, "current"));
  const action = str(b.action), param = str(b.param, "id");
  const { run, busy } = useAddonAction(conn, preview);
  const [folded, setFolded] = useState<Record<string, boolean>>({});
  const isCur = (it: Record<string, unknown>) => it.active === true || (!!cur && str(it.id) === cur);
  return (
    <Frame title={def.label} right={<ConnChip conn={conn} />}>
      {sections.length === 0 ? <p className="text-xs text-dim">Nothing in the list.</p> : (
        // Grows to fit (no inner scrollbar); folding keeps it short.
        <div className="h-full space-y-1 overflow-y-auto">
          {sections.map((sec, si) => {
            const sid = str(sec.id, String(si));
            const items = objs(sec.items);
            const holdsCur = items.some(isCur);
            // Until tapped: the add-on's `open`, and the section holding the current row always.
            const open = folded[sid] === undefined ? holdsCur || (typeof sec.open === "boolean" ? sec.open : sections.length === 1) : !folded[sid];
            return (
              <div key={sid}>
                <button type="button" onClick={() => setFolded((f) => ({ ...f, [sid]: open }))} aria-expanded={open}
                  className={cn("flex h-8 w-full items-center gap-1.5 rounded-lg px-1 text-left text-[9.5px] font-bold uppercase tracking-[0.12em] transition-colors",
                    holdsCur ? "text-muted" : "text-dim hover:text-muted")}>
                  {open ? <ChevronDown size={12} className="shrink-0" /> : <ChevronRight size={12} className="shrink-0" />}
                  <span className="min-w-0 truncate">{str(sec.title, sid)}</span>
                  {str(sec.meta) && <span className="shrink-0 font-normal before:mr-1 before:content-['·']">{str(sec.meta)}</span>}
                </button>
                {open && (
                  <div className="space-y-1">
                    {items.map((it, ii) => {
                      const id = str(it.id, `${sid}.${ii}`);
                      const on = isCur(it);
                      const sub = str(it.sub);
                      const title = str(it.title, id);
                      return (
                        <button key={id} type="button" disabled={!action || !!busy || editing} title={title}
                          onClick={() => run(action, { [param]: id }, `${def.label}: ${title}`, id)}
                          className={cn("relative flex min-h-9 w-full flex-col justify-center overflow-hidden rounded-lg border px-2.5 py-1.5 text-left text-sm transition active:scale-[0.99] disabled:active:scale-100",
                            on ? "btn-sel" : "border-transparent bg-white/[0.025] hover:border-line-strong hover:bg-white/[0.04]", busy === id && "opacity-60")}>
                          {/* the current row: an amber bar at the left edge */}
                          {on && <span aria-hidden className="absolute inset-y-1.5 left-0 w-[3px] rounded-r bg-accent" />}
                          <span className="flex w-full min-w-0 items-center gap-1">
                            <RowIcon icon={str(it.icon)} on={on} />
                            <span className={cn("min-w-0 flex-1 truncate pl-1", on ? "font-bold" : "text-fg/90")}>{title}</span>
                          </span>
                          {on && sub && <span className="block w-full truncate pl-[24px] text-[11px] text-accent/75">{sub}</span>}
                        </button>
                      );
                    })}
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}
    </Frame>
  );
}
