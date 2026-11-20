import { test, expect, type Page } from "@playwright/test";
import { DEVICES, SIM_VMIX, api, closeBrowsers, dashboardId, openDevice, shot, writeJSON } from "./lib";

// A Saturday in miniature: the booth cues the speaker (Pulpit channel) and
// the worship team (Worship channel: leader, two singers, two musicians),
// everyone answers from their own device, the stream goes on and off.

type Role = { who: string; device: keyof typeof DEVICES; dashboard: string; page?: Page; errors?: string[] };

const steps: { step: string; ok: boolean; note?: string; shots: string[] }[] = [];
async function step(name: string, fn: () => Promise<string[] | void>) {
  try {
    const shots = (await fn()) ?? [];
    steps.push({ step: name, ok: true, shots });
  } catch (e) {
    steps.push({ step: name, ok: false, note: (e as Error).message.split("\n")[0], shots: [] });
    throw e;
  }
}

test("service flow: booth, speaker, worship team", async () => {
  // setup: a booth dashboard with a Cue sender per channel
  const old = (await api<{ id: number; name: string }[]>("GET", "/api/dashboards")).find((d) => d.name === "E2E Booth");
  if (old) await api("DELETE", `/api/dashboards/${old.id}`);
  const booth = await api<{ id: number }>("POST", "/api/dashboards", {
    name: "E2E Booth", kind: "desktop",
    widgets: [
      { id: "air", type: "stage.live", settings: {} },
      { id: "cue-pulpit", type: "stage.cue", settings: { channels: ["pulpit"], incoming: false, outgoing: true, quick: ["Wrap up", "5 min left"], textBox: true } },
      { id: "cue-worship", type: "stage.cue", settings: { channels: ["worship"], incoming: false, outgoing: true, quick: ["One more chorus", "Mic check", "Speak up"], textBox: true } },
    ],
    layout: [{ i: "air", x: 0, y: 0, w: 12, h: 2 }, { i: "cue-pulpit", x: 0, y: 2, w: 6, h: 10 }, { i: "cue-worship", x: 6, y: 2, w: 6, h: 10 }],
  });
  const chans = await api<{ channels: { id: string; name: string; color: string }[] }>("GET", "/api/stage");
  const worship = chans.channels.find((c) => c.id === "worship")!;
  await api("PUT", "/api/stage/channels/worship", { name: worship.name, color: worship.color, oneAnswer: false });
  await api("DELETE", "/api/stage/channels/pulpit/messages");
  await api("DELETE", "/api/stage/channels/worship/messages");
  await fetch(`${SIM_VMIX}/api/?Function=StartStreaming`);

  const stagePhone = await dashboardId("Stage — phone");
  const stageIpad = await dashboardId("Stage — iPad");
  const roles: Role[] = [
    { who: "Booth", device: "desktop", dashboard: "E2E Booth" },
    { who: "Speaker", device: "ipad-side", dashboard: "Stage — iPad" },
    { who: "Worship lead", device: "iphone", dashboard: "Stage — phone" },
    { who: "Singer 1", device: "iphone-se", dashboard: "Stage — phone" },
    { who: "Singer 2", device: "iphone", dashboard: "Stage — phone" },
    { who: "Keys", device: "iphone", dashboard: "Stage — phone" },
    { who: "Drums", device: "iphone-se", dashboard: "Stage — phone" },
  ];
  const ids: Record<string, number> = { "E2E Booth": booth.id, "Stage — phone": stagePhone, "Stage — iPad": stageIpad };
  for (const r of roles) {
    const d = await openDevice(DEVICES[r.device], { screen: r.who, dashboard: ids[r.dashboard] });
    r.page = d.page;
    r.errors = d.errors;
  }
  const by = (w: string) => roles.find((r) => r.who === w)!.page!;
  const team = roles.filter((r) => r.dashboard === "Stage — phone");
  const cueOf = (p: Page) => p.locator('[data-type="stage.cue"]').first();
  const boothCue = (ch: string) => by("Booth").locator(`[data-wid="cue-${ch}"]`);
  const file = (r: Role, n: string) => `flow/${n}-${r.who.replace(/\s+/g, "-").toLowerCase()}.png`;

  await step("Everyone's screen loads and shows WE ARE LIVE", async () => {
    for (const r of roles) await expect(r.page!.locator('[data-type="stage.live"]').first()).toContainText("LIVE", { timeout: 15_000 });
    return [await shot(by("Booth"), "flow/01-booth.png"), await shot(by("Speaker"), "flow/01-speaker.png"), await shot(by("Singer 1"), "flow/01-singer-1.png")];
  });

  await step("Booth cues the speaker: only the speaker sees it", async () => {
    await boothCue("pulpit").getByRole("button", { name: "Wrap up" }).click();
    await expect(cueOf(by("Speaker"))).toContainText("Wrap up");
    for (const r of team) await expect(cueOf(r.page!)).not.toContainText("Wrap up");
    return [await shot(by("Speaker"), "flow/02-speaker-gets-cue.png"), await shot(by("Keys"), "flow/02-keys-does-not.png")];
  });

  await step("Speaker answers 👍 Got it: it clears on the iPad, the booth sees it", async () => {
    await cueOf(by("Speaker")).getByRole("button", { name: "👍 Got it" }).click();
    await expect(cueOf(by("Speaker"))).not.toContainText("Wrap up");
    await expect(boothCue("pulpit")).toContainText("✓ Speaker: 👍 Got it");
    return [await shot(by("Speaker"), "flow/03-speaker-answered.png"), await shot(by("Booth"), "flow/03-booth-sees-answer.png")];
  });

  await step("Booth cues the worship team: all five phones show it", async () => {
    await boothCue("worship").getByRole("button", { name: "One more chorus" }).click();
    for (const r of team) await expect(cueOf(r.page!)).toContainText("One more chorus");
    await expect(cueOf(by("Speaker"))).not.toContainText("One more chorus");
    return [await shot(by("Worship lead"), "flow/04-worship-lead.png"), await shot(by("Drums"), "flow/04-drums.png")];
  });

  await step("Two answer, three haven't: the booth shows who's who", async () => {
    await cueOf(by("Worship lead")).getByRole("button", { name: "Ready" }).click();
    await cueOf(by("Singer 1")).getByRole("button", { name: "👍 Got it" }).click();
    await expect(cueOf(by("Worship lead"))).not.toContainText("One more chorus");
    await expect(cueOf(by("Singer 1"))).not.toContainText("One more chorus");
    for (const w of ["Singer 2", "Keys", "Drums"]) await expect(cueOf(by(w))).toContainText("One more chorus");
    const b = boothCue("worship");
    await expect(b).toContainText("✓ Worship lead: Ready");
    await expect(b).toContainText("✓ Singer 1: 👍 Got it");
    for (const w of ["Singer 2", "Keys", "Drums"]) await expect(b).toContainText(`${w}: not answered yet`);
    return [await shot(by("Booth"), "flow/05-booth-partial.png"), await shot(by("Keys"), "flow/05-keys-still-has-it.png")];
  });

  await step("A new cue replaces the old one on the phones that hadn't answered", async () => {
    await boothCue("worship").getByRole("button", { name: "Speak up" }).click();
    for (const r of team) await expect(cueOf(r.page!)).toContainText("Speak up");
    await expect(boothCue("worship")).toContainText("“Speak up”");
  });

  await step("Booth types a cue in the text box", async () => {
    const box = boothCue("pulpit").getByPlaceholder("Type a cue…");
    await box.fill("Pray for the offering next");
    await box.press("Enter");
    await expect(cueOf(by("Speaker"))).toContainText("Pray for the offering next");
    return [await shot(by("Speaker"), "flow/07-speaker-typed-cue.png")];
  });

  await step("“One answer is enough”: the first reply clears it for the whole team", async () => {
    await api("PUT", "/api/stage/channels/worship", { name: worship.name, color: worship.color, oneAnswer: true });
    await boothCue("worship").getByRole("button", { name: "Mic check" }).click();
    for (const r of team) await expect(cueOf(r.page!)).toContainText("Mic check");
    await cueOf(by("Drums")).getByRole("button", { name: "👍 Got it" }).click();
    for (const r of team) await expect(cueOf(r.page!)).not.toContainText("Mic check");
    await expect(boothCue("worship")).toContainText("answered");
    await api("PUT", "/api/stage/channels/worship", { name: worship.name, color: worship.color, oneAnswer: false });
    return [await shot(by("Booth"), "flow/08-booth-one-answer.png")];
  });

  await step("Booth clears: nothing left waiting anywhere", async () => {
    await boothCue("pulpit").getByRole("button", { name: "Clear" }).click();
    await boothCue("worship").getByRole("button", { name: "Clear" }).click();
    await expect(cueOf(by("Speaker"))).not.toContainText("Pray for the offering");
    for (const r of team) await expect(cueOf(r.page!)).not.toContainText("Speak up");
  });

  await step("Stream stops: every screen says OFF AIR; starts: WE ARE LIVE", async () => {
    await fetch(`${SIM_VMIX}/api/?Function=StopStreaming`);
    for (const r of roles) await expect(r.page!.locator('[data-type="stage.live"]').first()).toContainText("OFF AIR");
    const s = [await shot(by("Singer 2"), "flow/10-off-air.png")];
    await fetch(`${SIM_VMIX}/api/?Function=StartStreaming`);
    for (const r of roles) await expect(r.page!.locator('[data-type="stage.live"]').first()).toContainText("LIVE");
    return s;
  });

  await step("No errors in any device's page", async () => {
    const all = roles.flatMap((r) => (r.errors ?? []).map((e) => `${r.who}: ${e}`));
    expect(all, all.join("\n")).toEqual([]);
  });

  await api("DELETE", `/api/dashboards/${booth.id}`);
  await closeBrowsers();
});

test.afterAll(() => writeJSON("flow.json", steps));
