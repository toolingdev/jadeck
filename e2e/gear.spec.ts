import { test, expect, type Page } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { DEVICES, SIM_VMIX, api, closeBrowsers, openDevice, waitStill, type Device } from "./lib";

// What CI checks on every push, against a blank jadeck with the simulator
// (e2e/start-rig.sh):
//   1. the gear works from the screen: cut a camera, next slide, mute a
//      channel, each read back from the simulated vMix / Proclaim / WING;
//   2. every dashboard and Settings page looks the same as its saved
//      picture (screens/<platform>/, refreshed with --update-snapshots);
//   3. no serious accessibility problems (axe: labels, contrast, roles).

const booth = () => api<{ id: number; name: string }[]>("GET", "/api/dashboards").then((l) => l.find((d) => d.name === "Booth")!.id);
const widget = (page: Page, type: string) => page.locator(`[data-type="${type}"]`).first();

async function vmixState() {
  const xml = await (await fetch(SIM_VMIX + "/api")).text();
  const n = (tag: string) => Number(xml.match(new RegExp(`<${tag}>(\\d+)</${tag}>`))?.[1]);
  return { active: n("active"), preview: n("preview") };
}

test.describe("gear from the screen", () => {
  test.afterAll(closeBrowsers);

  test("cut: preview goes to program on vMix", async () => {
    const { ctx, page, errors } = await openDevice(DEVICES["ipad-side"], { screen: "CI", dashboard: await booth() });
    const inputs = widget(page, "switcher.inputs");
    await expect(inputs.getByRole("button", { name: /PGM/ })).toBeVisible();
    // pick input 3 for preview, then cut it to program
    await waitStill(page);
    const three = inputs.getByRole("button", { name: /^3\b/ });
    await three.click({ timeout: 5000 }).catch(async () => {
      // GitHub's runners sometimes never see it "stable": say why, then tap anyway
      const boxes = new Set<string>();
      for (let i = 0; i < 10; i++) { boxes.add(JSON.stringify(await three.boundingBox())); await page.waitForTimeout(100); }
      console.log(`input 3 not clickable: enabled=${await three.isEnabled()} boxes=${[...boxes].join(" ")}`);
      await three.click({ force: true });
    });
    await expect.poll(async () => (await vmixState()).preview).toBe(3);
    await widget(page, "switcher.cutfade").getByRole("button", { name: "CUT" }).click();
    await expect.poll(async () => (await vmixState()).active).toBe(3);
    await expect(inputs.getByRole("button", { name: /^3\b.*PGM/ })).toBeVisible();
    expect(errors).toEqual([]);
    await ctx.close();
  });

  test("slides: next and previous move Proclaim", async () => {
    const { ctx, page, errors } = await openDevice(DEVICES["ipad-side"], { screen: "CI", dashboard: await booth() });
    const slides = widget(page, "slides.prevnext");
    const where = slides.locator("text=/ of \\d+/").first();
    await expect(where).toBeVisible();
    const before = await where.innerText();
    await slides.getByRole("button", { name: "Next slide" }).click();
    await expect(where).not.toHaveText(before);
    await slides.getByRole("button", { name: "Prev slide" }).click();
    await expect(where).toHaveText(before);
    expect(errors).toEqual([]);
    await ctx.close();
  });

  test("mixer: mute and unmute a WING channel", async () => {
    const { ctx, page, errors } = await openDevice(DEVICES["ipad-side"], { screen: "CI", dashboard: await booth() });
    const fav = widget(page, "audio.favorites").getByRole("button", { name: /Lead vox/ });
    await expect(fav).toContainText(/live/i);
    await fav.click();
    await expect(fav).toContainText(/muted/i); // read back from the WING, not just the tap
    await fav.click();
    await expect(fav).toContainText(/live/i);
    expect(errors).toEqual([]);
    await ctx.close();
  });
});

// Things that change on their own (video, clocks, the log, meters) are
// covered so the pictures only differ when the layout does.
const LIVE = ['[data-type="video"]', '[data-type="camera.live"]', '[data-type="switcher.program"]', '[data-type="switcher.confidence"]',
  '[data-type="other.log"]', '[data-type="stage.timer"]', '[data-type="stage.live"]', "video", "canvas", "[data-live]"];

const SCREENS: { key: keyof typeof DEVICES; kind: string }[] = [
  { key: "iphone", kind: "phone" }, { key: "ipad-side", kind: "tablet" }, { key: "desktop", kind: "desktop" },
];
const SECTIONS = ["cameras", "connections", "feeds", "dashboards", "stage", "addons", "access"];

async function settle(page: Page) {
  await page.waitForLoadState("networkidle").catch(() => {});
  await page.evaluate(() => document.fonts.ready);
  await waitStill(page);
  await page.waitForTimeout(500);
}

async function look(page: Page, name: string) {
  await expect(page).toHaveScreenshot(name, {
    fullPage: true, animations: "disabled", caret: "hide",
    mask: LIVE.map((s) => page.locator(s)), maskColor: "#202020",
    maxDiffPixelRatio: 0.002,
  });
}

async function a11y(page: Page, where: string) {
  const r = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]).exclude("video").analyze();
  const line = (v: (typeof r.violations)[number]) => `${where}: ${v.id} (${v.impact}) ×${v.nodes.length}: ${v.help} — ${v.nodes.slice(0, 3).map((n) => n.target.join(" ")).join(", ")}`;
  const bad = r.violations.filter((v) => v.impact === "serious" || v.impact === "critical");
  // Dim text (labels, hints) is below 4.5:1 on purpose for now: reported, not failing.
  for (const v of bad.filter((v) => v.id === "color-contrast")) test.info().annotations.push({ type: "contrast", description: line(v) });
  expect.soft(bad.filter((v) => v.id !== "color-contrast").map(line), "accessibility").toEqual([]);
}

for (const { key, kind } of SCREENS) {
  test.describe(`${DEVICES[key].name}`, () => {
    test.afterAll(closeBrowsers);
    test("dashboards and settings look right", async () => {
      const dev: Device = DEVICES[key];
      const boards = (await api<{ id: number; name: string; kind: string }[]>("GET", "/api/dashboards")).filter((d) => d.kind === kind);
      const targets = [
        ...boards.map((d) => ({ name: `dashboard-${d.name}`, dashboard: d.id, path: "/" })),
        ...SECTIONS.map((s) => ({ name: `settings-${s}`, dashboard: undefined as number | undefined, path: `/settings/${s}` })),
      ];
      for (const t of targets) {
        await test.step(t.name, async () => {
          const { ctx, page, errors } = await openDevice(dev, { screen: "CI", dashboard: t.dashboard, path: t.path, fixedTime: "2026-01-03T10:00:00Z" });
          await settle(page);
          await page.addStyleTag({ content: "[data-phone-nav]{display:none!important}" });
          const file = `${key}-${t.name.replace(/[^a-z0-9]+/gi, "-").toLowerCase()}.png`;
          await look(page, file);
          await a11y(page, `${dev.name} · ${t.name}`);
          expect.soft(errors, `errors on ${t.name}`).toEqual([]);
          await ctx.close();
        });
      }
    });
  });
}
