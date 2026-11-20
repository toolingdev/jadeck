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

export function screenFor(width: number, height = Infinity): Screen {
  // A phone on its side is still a phone: its short side is under 500px.
  if (width < 640 || Math.min(width, height) < 500) return "phone";
  return width < 1280 ? "tablet" : "desktop";
}

export function newId(type: string) {
  return `${type.split(".").pop()}-${Math.random().toString(36).slice(2, 7)}`;
}

export function bottomOf(items: Item[]) {
  return items.reduce((m, it) => Math.max(m, it.y + it.h), 0);
}

/** Stack a layout in reading order at `cols` wide (a big dashboard on a phone). */
/** Two columns (a phone on its side): each widget, in reading order,
 *  goes to whichever column is shorter. */
export function twoUp(items: Item[], half: number): Item[] {
  const y = [0, 0];
  return [...items].sort((a, b) => a.y - b.y || a.x - b.x).map((it) => {
    const c = y[0] <= y[1] ? 0 : 1;
    const out = { i: it.i, x: c * half, y: y[c], w: half, h: it.h };
    y[c] += it.h;
    return out;
  });
}

export function stacked(items: Item[], cols: number): Item[] {
  let y = 0;
  return [...items].sort((a, b) => a.y - b.y || a.x - b.x).map((it) => {
    const out = { i: it.i, x: 0, y, w: cols, h: it.h };
    y += it.h;
    return out;
  });
}
