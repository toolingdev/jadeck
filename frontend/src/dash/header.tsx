import type { ReactNode } from "react";
import { cn } from "@/lib/cn";
import { useShared } from "@/lib/hooks";
import { useCam } from "@/lib/camera";
import { CamChip } from "@/components/CamChip";
import type { CameraInfo, StreamConfig } from "@/lib/api";

// The source header every widget wears (design/widget-source-headers-mock.png,
// WIDGETS.md §6): Title · ● SOURCE on the left, live context on the right.
// Green dot = connected, grey = offline (the source then reads "… · OFFLINE"
// and the widget's controls dim). Height is the same 18px line the old
// titles used, so nothing below it moves; on boxes shorter than ~84px the
// header folds away (index.css, .wframe) and the controls keep the room.

export type HeadProps = {
  /** What the widget is: "Slides", "Inputs", "Move"… */
  title?: ReactNode;
  /** What it's tied to: the connection, camera, feed or channel. */
  source?: ReactNode;
  /** true = connected (green dot), false = offline (grey, "· OFFLINE"),
   *  undefined = no dot (nothing to be connected to). */
  online?: boolean;
  /** Live context, right-aligned and muted: "John 11:28–44 · 3 of 7". */
  right?: ReactNode;
};

/** Chrome for widgets that aren't already a card, with the source header:
 *  Title · ● SOURCE · context on the right. `online` false greys the dot,
 *  says OFFLINE and dims the controls.
 *
 *  The outer box is a size container (index.css .wframe): on a box too
 *  short for the header it folds away. `bleed`: a widget that is one big
 *  control (a button, the on-air pill, a tally light) fills the whole box
 *  then, with no card around it, the way it did before headers.
 *  `flush`: no body padding (the content pads itself). */
export function Frame({ title, source, online, children, className, right, bleed, flush }: HeadProps & {
  children: ReactNode; className?: string; bleed?: boolean; flush?: boolean;
}) {
  const head = title !== undefined || !!source || (right !== undefined && right !== null && right !== false);
  return (
    <div className="wframe h-full">
      <div className={cn("wframe-card card-surface flex h-full flex-col overflow-hidden rounded-[var(--radius-card)] border border-line", online === false && "wframe-off", className)}>
        {head && <WidgetHeader title={title} source={source} online={online} right={right} />}
        <div className={cn("min-h-0 flex-1", !head ? "p-3" : flush ? "wframe-flush" : "wframe-body px-3 pb-3", bleed && "wframe-bleed")}>{children}</div>
      </div>
    </div>
  );
}

export function WidgetHeader({ title, source, online, right }: HeadProps) {
  return (
    <div className="wframe-head flex shrink-0 items-center gap-2 px-3 pt-2.5 pb-1.5">
      <div className="flex min-w-0 items-center gap-2">
        {title !== undefined && (
          <span className="min-w-[2.5rem] truncate text-[12px] font-bold leading-[18px] tracking-[-0.005em] text-fg [&:has(.no-drag)]:overflow-visible">{title}</span>
        )}
        {/* a string is drawn as ● SOURCE; a node (e.g. <CamSource />) as is */}
        {source === undefined || source === null || typeof source === "string" ? <Source source={source ?? undefined} online={online} /> : source}
      </div>
      {right !== undefined && right !== null && right !== false && <div className="ml-auto flex min-w-0 shrink-0 items-center gap-2">{right}</div>}
    </div>
  );
}

/** ● SOURCE: the dot and the small caps, on their own (also used where a
 *  widget draws its own header row). */
export function Source({ source, online }: { source?: ReactNode; online?: boolean }) {
  if ((source === undefined || source === null || source === "") && online === undefined) return null;
  return (
    <span className="flex min-w-0 shrink-[4] items-center gap-1.5" data-source>
      {online !== undefined && (
        <span aria-hidden className={cn("h-1.5 w-1.5 shrink-0 rounded-full", online ? "bg-good shadow-[0_0_4px_color-mix(in_srgb,var(--color-good)_60%,transparent)]" : "bg-dim")} />
      )}
      {source !== undefined && source !== null && source !== "" && (
        <span className="min-w-0 truncate text-[10px] font-semibold uppercase leading-[18px] tracking-[1px] text-muted">
          {source}{online === false && " · offline"}
        </span>
      )}
    </span>
  );
}

/** Right-hand context text: muted, one line, cut with "…". */
export function Ctx({ children, className }: { children: ReactNode; className?: string }) {
  if (children === undefined || children === null || children === "") return null;
  return <span className={cn("max-w-[14rem] truncate text-[11px] leading-[18px] text-muted tabular", className)}>{children}</span>;
}

// ------------------------------------------------------------ sources

/** A camera widget's source: the camera's name; online from its status. */
export function useCamHead(pin?: string): { source?: string; online?: boolean } {
  const ctx = useCam();
  const id = pin ?? ctx;
  const { data } = useShared<CameraInfo[]>("/api/cameras", 10000);
  const cam = data?.find((c) => c.id === id) ?? data?.find((c) => c.main);
  if (!cam) return {};
  return { source: cam.name, online: cam.status ? cam.status.connected : undefined };
}

/** A camera widget's source with the camera picker: with two or more
 *  cameras the amber chip (tap to switch) stands in for the name. */
export function CamSource({ pin }: { pin?: string }) {
  const { source, online } = useCamHead(pin);
  const { data } = useShared<{ id: string }[]>("/api/cameras", 10000);
  // The chip shows in edit mode too (not tappable there): edit mode must
  // look exactly like the dashboard after Done, header line count included.
  if ((data?.length ?? 0) >= 2) {
    return (
      <span className="flex min-w-0 items-center gap-1.5">
        {online !== undefined && <Source online={online} />}
        <CamChip />
      </span>
    );
  }
  return <Source source={source} online={online} />;
}

/** A video feed's source: "CAMERA" for a camera, else who sends it and how
 *  ("NDI · LIVESTREAM" from an add-on, "SRT" / "RTSP" pulled, "PUSH"). */
export function feedSource(feed: string, cfg?: StreamConfig | null): string {
  if (feed.startsWith("cam:")) return "Camera";
  const f = cfg?.feeds?.find((x) => x.id === feed);
  if (!f) return "";
  if (f.via) return f.via;
  if (f.source === "pull") return f.proto || "Pull";
  return "Push";
}
