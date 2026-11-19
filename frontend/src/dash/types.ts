export type Screen = "phone" | "tablet" | "desktop";
export const SCREENS: Screen[] = ["phone", "tablet", "desktop"];
export const COLS: Record<Screen, number> = { phone: 4, tablet: 12, desktop: 12 };
export const ROW_H = 40;
export const SCREEN_LABEL: Record<Screen, string> = { phone: "Phone", tablet: "Tablet", desktop: "Desktop" };

export type Widget = { id: string; type: string; settings: Record<string, unknown> };
export type Item = { i: string; x: number; y: number; w: number; h: number };
/** A dashboard is made for one kind of screen and has one layout. */
export type Dashboard = { id: number; name: string; kind: Screen; sort: number; widgets: Widget[]; layout: Item[]; updatedAt: string };
export type Defaults = Partial<Record<Screen, number>>;

export function screenFor(width: number): Screen {
  return width < 640 ? "phone" : width < 1280 ? "tablet" : "desktop";
}

export function newId(type: string) {
  return `${type.split(".").pop()}-${Math.random().toString(36).slice(2, 7)}`;
}

export function bottomOf(items: Item[]) {
  return items.reduce((m, it) => Math.max(m, it.y + it.h), 0);
}

/** Stack a layout in reading order at `cols` wide (a big dashboard on a phone). */
export function stacked(items: Item[], cols: number): Item[] {
  let y = 0;
  return [...items].sort((a, b) => a.y - b.y || a.x - b.x).map((it) => {
    const out = { i: it.i, x: 0, y, w: cols, h: it.h };
    y += it.h;
    return out;
  });
}
