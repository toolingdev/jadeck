// Turns report/flow.json + report/sweep.json into one page you can open
// anywhere: report/index.html. On a Mac the screenshots are shrunk with
// `sips` and embedded, so the file stands on its own (send it, open it on
// a phone). Elsewhere it links to the full-size PNGs next to it.
import fs from "node:fs";
import path from "node:path";
import { execFileSync } from "node:child_process";

const R = path.resolve(import.meta.dirname, "report");
const read = (f) => (fs.existsSync(path.join(R, f)) ? JSON.parse(fs.readFileSync(path.join(R, f), "utf8")) : null);
const flow = read("flow.json") ?? [];
const sweep = read("sweep.json") ?? { pages: [], findings: [] };
const esc = (s) => String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);

const canShrink = process.platform === "darwin";
const img = (rel, w = 360) => {
  const src = path.join(R, rel);
  if (!fs.existsSync(src)) return "";
  if (canShrink) {
    const out = path.join(R, "thumbs", rel.replace(/\.png$/, ".jpg"));
    fs.mkdirSync(path.dirname(out), { recursive: true });
    if (!fs.existsSync(out)) execFileSync("sips", ["-s", "format", "jpeg", "-s", "formatOptions", "70", "--resampleWidth", String(w), src, "--out", out], { stdio: "ignore" });
    return `<img loading="lazy" src="data:image/jpeg;base64,${fs.readFileSync(out).toString("base64")}" alt="${esc(rel)}">`;
  }
  return `<a href="${esc(rel)}"><img loading="lazy" src="${esc(rel)}" alt="${esc(rel)}"></a>`;
};

const passed = flow.filter((s) => s.ok).length;
const devices = [...new Set(sweep.pages.map((p) => p.device))];
const when = new Date().toLocaleString("en-US", { dateStyle: "medium", timeStyle: "short" });

let html = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>jadeck test report</title><style>
:root{color-scheme:dark;--bg:#0d0f12;--card:#161a20;--line:#262c35;--text:#e6e8eb;--dim:#8b93a0;--ok:#3ecf8e;--bad:#f2555a;--acc:#f5a524}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--text);font:15px/1.5 system-ui,-apple-system,sans-serif;padding:24px 16px 64px}
main{max-width:1200px;margin:auto}h1{font-size:24px;margin:0 0 4px}h2{font-size:18px;margin:40px 0 12px;border-bottom:1px solid var(--line);padding-bottom:6px}h3{font-size:15px;margin:24px 0 8px;color:var(--acc)}
.dim{color:var(--dim)}.tiles{display:flex;flex-wrap:wrap;gap:12px;margin:16px 0}.tile{background:var(--card);border:1px solid var(--line);border-radius:10px;padding:12px 16px;min-width:140px}
.tile b{display:block;font-size:26px}.ok{color:var(--ok)}.bad{color:var(--bad)}
.step{background:var(--card);border:1px solid var(--line);border-radius:10px;padding:12px 16px;margin:10px 0}.step p{margin:0 0 8px;font-weight:600}
.shots{display:flex;gap:10px;overflow-x:auto;padding-bottom:4px}.shots figure{margin:0;flex:0 0 auto}.shots img{height:260px;width:auto;border:1px solid var(--line);border-radius:6px;display:block}
figcaption{font-size:12px;color:var(--dim);max-width:240px;margin-top:4px}
.grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(180px,1fr));gap:12px}.grid figure{margin:0;background:var(--card);border:1px solid var(--line);border-radius:8px;padding:6px}
.grid img{width:100%;max-height:340px;object-fit:cover;object-position:top;border-radius:4px;display:block}
table{border-collapse:collapse;width:100%;font-size:13px}td,th{border-bottom:1px solid var(--line);padding:6px;text-align:left;vertical-align:top}
</style></head><body><main>
<h1>jadeck test report</h1><div class="dim">${esc(when)} · run against the simulator (4 cameras, vMix, Proclaim, WING)</div>
<div class="tiles">
<div class="tile"><b class="${passed === flow.length && flow.length ? "ok" : "bad"}">${passed}/${flow.length}</b>service steps passed</div>
<div class="tile"><b>${sweep.pages.length}</b>pages checked</div>
<div class="tile"><b>${devices.length}</b>screen sizes</div>
<div class="tile"><b class="${sweep.findings.length ? "bad" : "ok"}">${sweep.findings.length}</b>layout problems</div>
</div>
<h2>Service flow: booth, speaker, worship team</h2>
<p class="dim">Seven devices at once: Booth (desktop), Speaker (iPad landscape), Worship lead, Singer 1, Singer 2, Keys, Drums (iPhones, two of them the small SE).</p>`;
for (const s of flow) {
  html += `<div class="step"><p><span class="${s.ok ? "ok" : "bad"}">${s.ok ? "✓" : "✗"}</span> ${esc(s.step)}</p>${s.note ? `<div class="bad">${esc(s.note)}</div>` : ""}`;
  if (s.shots.length) html += `<div class="shots">${s.shots.map((f) => `<figure>${img(f, 480)}<figcaption>${esc(path.basename(f, ".png").replace(/^\d+-/, "").replace(/-/g, " "))}</figcaption></figure>`).join("")}</div>`;
  html += `</div>`;
}
html += `<h2>Layout problems</h2>`;
html += sweep.findings.length
  ? `<table><tr><th>Screen</th><th>Page</th><th>Problem</th><th>Detail</th></tr>${sweep.findings.map((f) => `<tr><td>${esc(f.device)}</td><td>${esc(f.page)}</td><td class="bad">${esc(f.kind)}</td><td>${esc(f.detail)}</td></tr>`).join("")}</table>`
  : `<p class="ok">None. Checked on every page: page wider than the screen, widgets on top of each other, things off the edge, text cut off without “…”, scrollbars inside widgets, squeezed text, widget content cut off, tap targets under 28px on touch screens, buttons hidden under the phone's bottom tab, errors in the page.</p>`;
html += `<h2>Every page on every screen</h2>`;
for (const d of devices) {
  html += `<h3>${esc(d)}</h3><div class="grid">`;
  for (const p of sweep.pages.filter((x) => x.device === d)) html += `<figure>${img(p.shot)}<figcaption>${esc(p.page)}${p.problems ? ` · <span class="bad">${p.problems} problem${p.problems > 1 ? "s" : ""}</span>` : ""}</figcaption></figure>`;
  html += `</div>`;
}
html += `</main></body></html>`;
fs.writeFileSync(path.join(R, "index.html"), html);
console.log(`report/index.html (${(html.length / 1e6).toFixed(1)} MB)`);
