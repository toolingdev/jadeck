import { test } from "@playwright/test";
import { DEVICES, api, closeBrowsers, openDevice, writeJSON } from "./lib";

// Edit mode must show the layout people get after Done: every widget the
// same width and height in both. Checked with nothing selected and with a
// widget's settings open, on every screen, for every dashboard made for it.

type Box = { id: string; type: string; w: number; h: number };
const boxes = () => [...document.querySelectorAll("[data-wid]")].map((e) => {
  const r = e.getBoundingClientRect();
  return { id: e.getAttribute("data-wid")!, type: e.getAttribute("data-type")!, w: Math.round(r.width), h: Math.round(r.height) };
});

test("edit mode looks like done", async () => {
  const dashboards = await api<{ id: number; name: string; kind: string }[]>("GET", "/api/dashboards");
  const diffs: { device: string; dashboard: string; when: string; widget: string; done: string; edit: string }[] = [];
  for (const [, dev] of Object.entries(DEVICES)) {
    const kind = dev.viewport.width < 640 || Math.min(dev.viewport.width, dev.viewport.height) < 500 ? "phone" : dev.viewport.width < 1280 ? "tablet" : "desktop";
    for (const d of dashboards.filter((x) => x.kind === kind)) {
      const { ctx, page } = await openDevice(dev, { screen: "Edit check", dashboard: d.id });
      await page.waitForTimeout(1500);
      const done: Box[] = await page.evaluate(boxes);
      const edit = page.getByRole("button", { name: "Edit" }).first();
      if (!(await edit.isVisible().catch(() => false))) { await ctx.close(); continue; }
      await edit.click();
      await page.waitForTimeout(800);
      const compare = async (when: string) => {
        const now: Box[] = await page.evaluate(boxes);
        for (const a of done) {
          const b = now.find((x) => x.id === a.id);
          if (b && (Math.abs(a.w - b.w) > 2 || Math.abs(a.h - b.h) > 2)) diffs.push({ device: dev.name, dashboard: d.name, when, widget: a.type, done: `${a.w}×${a.h}`, edit: `${b.w}×${b.h}` });
        }
      };
      await compare("editing");
      const first = page.locator("[data-wid]").first();
      if (await first.isVisible().catch(() => false)) {
        await first.click({ position: { x: 20, y: 20 } });
        await page.waitForTimeout(600);
        await compare("editing, settings open");
      }
      await page.getByRole("button", { name: "Done" }).first().click().catch(() => {});
      await ctx.close();
    }
  }
  writeJSON("editmode.json", diffs);
  await closeBrowsers();
  console.log(`${diffs.length} widgets change size between edit and done`);
  for (const x of diffs) console.log(`- ${x.device} · ${x.dashboard} (${x.when}): ${x.widget} ${x.done} after Done, ${x.edit} while editing`);
  if (diffs.length) throw new Error(`${diffs.length} widgets are a different size while editing than after Done`);
});
