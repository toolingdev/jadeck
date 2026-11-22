import { test, expect } from "@playwright/test";
import { DEVICES, api, closeBrowsers, openDevice, shot, writeJSON } from "./lib";

// Every dashboard and every Settings section on every screen size: a
// screenshot each, plus checks for the things that go wonky — the page
// wider than the screen, widgets on top of each other, text cut off
// without "…", a widget with its own scrollbar, errors in the page.

type Finding = { device: string; page: string; kind: string; detail: string };

const SECTIONS = ["cameras", "connections", "feeds", "dashboards", "stage", "addons", "access", "system"];

// what the page looks like to a person, measured in the page itself
const checks = () => {
  const out: { kind: string; detail: string }[] = [];
  const W = document.documentElement.clientWidth;
  const label = (el: Element) => {
    const w = el.closest("[data-wid]");
    const t = (el.textContent || "").trim().replace(/\s+/g, " ").slice(0, 40);
    return `${w ? `[${w.getAttribute("data-type")}] ` : ""}${el.tagName.toLowerCase()} “${t}”`;
  };
  const visible = (el: Element) => {
    const s = getComputedStyle(el);
    const r = el.getBoundingClientRect();
    return s.visibility !== "hidden" && s.display !== "none" && r.width > 0 && r.height > 0;
  };
  if (document.documentElement.scrollWidth > W + 1) out.push({ kind: "page wider than screen", detail: `${document.documentElement.scrollWidth}px on a ${W}px screen` });

  // widgets overlapping
  const boxes = [...document.querySelectorAll("[data-wid]")].map((e) => ({ id: e.getAttribute("data-type"), r: e.getBoundingClientRect() }));
  for (let i = 0; i < boxes.length; i++) for (let j = i + 1; j < boxes.length; j++) {
    const a = boxes[i].r, b = boxes[j].r;
    const x = Math.min(a.right, b.right) - Math.max(a.left, b.left), y = Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top);
    if (x > 4 && y > 4) out.push({ kind: "widgets overlap", detail: `${boxes[i].id} and ${boxes[j].id} (${Math.round(x)}×${Math.round(y)}px)` });
  }

  for (const el of document.querySelectorAll("main *")) {
    if (!visible(el)) continue;
    const s = getComputedStyle(el);
    const r = el.getBoundingClientRect();
    // off the right edge, not inside something that scrolls sideways on purpose
    if (r.right > W + 2 && r.width < W * 3) {
      let p = el.parentElement, scroller = false;
      while (p && p !== document.body) {
        if (/(auto|scroll)/.test(getComputedStyle(p).overflowX)) { scroller = true; break; }
        p = p.parentElement;
      }
      if (!scroller) out.push({ kind: "off the screen", detail: `${label(el)} ends at ${Math.round(r.right)}px (screen ${W}px)` });
    }
    // text cut off with no "…" to say so
    const leaf = [...el.childNodes].some((n) => n.nodeType === 3 && n.textContent!.trim());
    if (leaf && el.scrollWidth > el.clientWidth + 2 && s.overflowX === "hidden" && s.textOverflow !== "ellipsis") out.push({ kind: "text cut off", detail: label(el) });
    // a widget with its own scrollbar (they should fit what's in them)
    if (el.closest("[data-wid]") && /(auto|scroll)/.test(s.overflowY) && el.scrollHeight > el.clientHeight + 4 && el.clientHeight > 0) {
      out.push({ kind: "scrollbar inside a widget", detail: `${label(el.closest("[data-wid]")!)}: ${el.scrollHeight}px of content in ${el.clientHeight}px` });
    }
  }
  // text squeezed to nothing (a flex item with no room)
  for (const el of document.querySelectorAll("main span, main p, main div, main button, main label")) {
    if (!visible(el)) continue;
    const own = [...el.childNodes].filter((n) => n.nodeType === 3).map((n) => n.textContent!.trim()).join("");
    const r = el.getBoundingClientRect();
    if (own.length > 2 && r.width < 14) out.push({ kind: "text squeezed to nothing", detail: `${label(el)} is ${Math.round(r.width)}px wide` });
  }
  // a widget whose content is cut off at the bottom
  for (const w of document.querySelectorAll("[data-wid] > *")) {
    const inner = w.firstElementChild as HTMLElement | null;
    if (inner && inner.scrollHeight > inner.clientHeight + 6 && getComputedStyle(inner).overflowY === "hidden") {
      out.push({ kind: "widget content cut off", detail: `${label(w.closest("[data-wid]")!)}: ${inner.scrollHeight}px of content in ${inner.clientHeight}px` });
    }
  }
  // touch screens: buttons big enough to hit
  if (matchMedia("(pointer: coarse)").matches) {
    for (const b of document.querySelectorAll("main button, main a, main [role=button], main select, main input")) {
      if (!visible(b)) continue;
      const r = b.getBoundingClientRect();
      if ((r.height < 28 || r.width < 28) && (b as HTMLElement).innerText?.trim().length !== 0 && !(b as HTMLInputElement).type?.match(/checkbox|radio/)) {
        out.push({ kind: "small tap target", detail: `${label(b)} is ${Math.round(r.width)}×${Math.round(r.height)}px` });
      }
    }
  }
  // the last buttons on the page, scrolled to: not hidden under the bottom tab
  window.scrollTo(0, document.documentElement.scrollHeight);
  const btns = [...document.querySelectorAll("main button")].filter(visible).slice(-4);
  for (const b of btns) {
    const r = b.getBoundingClientRect();
    if (r.bottom <= 0 || r.top >= innerHeight) continue;
    const hit = document.elementFromPoint(r.left + r.width / 2, Math.min(r.bottom - 2, innerHeight - 1));
    if (hit && !b.contains(hit) && hit.closest("[data-phone-nav]")) out.push({ kind: "button under the bottom tab", detail: label(b) });
  }
  window.scrollTo(0, 0);
  // the same widget can report many children: keep one line per detail
  return [...new Map(out.map((o) => [o.kind + o.detail, o])).values()];
};

test("screen sweep: every page on every device", async () => {
  const dashboards = await api<{ id: number; name: string; kind: string }[]>("GET", "/api/dashboards");
  const findings: Finding[] = [];
  const pages: { device: string; page: string; shot: string; problems: number }[] = [];
  for (const [key, dev] of Object.entries(DEVICES)) {
    const kind = dev.viewport.width < 640 || Math.min(dev.viewport.width, dev.viewport.height) < 500 ? "phone" : dev.viewport.width < 1280 ? "tablet" : "desktop";
    // each dashboard made for this kind of screen (the others aren't meant for it)
    const targets = [
      ...dashboards.filter((d) => d.kind === kind).map((d) => ({ name: `Dashboard · ${d.name}`, dashboard: d.id, path: "/" })),
      ...SECTIONS.map((s) => ({ name: `Settings · ${s}`, dashboard: undefined as number | undefined, path: `/settings/${s}` })),
    ];
    for (const t of targets) {
      const { ctx, page, errors } = await openDevice(dev, { screen: "Sweep", dashboard: t.dashboard, path: t.path });
      await page.waitForTimeout(2500); // live data, measured sizes, fonts
      const found = await page.evaluate(checks);
      for (const e of errors) found.push({ kind: "error in the page", detail: e });
      const file = await shot(page, `sweep/${key}/${t.name.replace(/[^a-z0-9]+/gi, "-").toLowerCase()}.png`);
      pages.push({ device: dev.name, page: t.name, shot: file, problems: found.length });
      for (const f of found) findings.push({ device: dev.name, page: t.name, ...f });
      // dashboards in edit mode too: a widget tapped, its settings open
      if (t.dashboard) {
        const edit = page.getByRole("button", { name: "Edit" }).first();
        if (await edit.isVisible().catch(() => false)) {
          await edit.click();
          await page.waitForTimeout(800);
          const first = page.locator("[data-wid]").first();
          if (await first.isVisible().catch(() => false)) {
            await first.click({ position: { x: 20, y: 20 } });
            await page.waitForTimeout(800);
          }
          const name = `${t.name} (editing)`;
          const f2 = (await page.evaluate(checks)).filter((f) => f.kind !== "widgets overlap" || !/dropping/.test(f.detail));
          const file2 = await shot(page, `sweep/${key}/${name.replace(/[^a-z0-9]+/gi, "-").toLowerCase()}.png`);
          pages.push({ device: dev.name, page: name, shot: file2, problems: f2.length });
          for (const f of f2) findings.push({ device: dev.name, page: name, ...f });
          await page.getByRole("button", { name: "Done" }).first().click().catch(() => {});
        }
      }
      await ctx.close();
    }
  }
  writeJSON("sweep.json", { pages, findings });
  await closeBrowsers();
  console.log(`${pages.length} pages, ${findings.length} findings`);
  for (const f of findings) console.log(`- ${f.device} · ${f.page}: ${f.kind} — ${f.detail}`);
  // in CI any finding fails the run; locally it's a report to look through
  if (process.env.CI) expect(findings.map((f) => `${f.device} · ${f.page}: ${f.kind} — ${f.detail}`)).toEqual([]);
});
