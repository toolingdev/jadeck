# e2e: whole-team tests on every screen size

Two Playwright tests that drive a running jadeck the way a team would on a
Saturday, from many devices at once. jadeck is only one person's machine;
these stand in for the rest of the team.

| Test | What it does |
|------|--------------|
| `gear.spec.ts` | What CI checks on every push. The gear from the screen: pick input 3 and Cut (read back from the simulated vMix), Next / Prev slide (Proclaim), mute and unmute a WING channel. Every dashboard and Settings page on an iPhone, an iPad (landscape) and a desktop compared with its saved picture in `screens/<os>/` (video, clocks, the log and meters are covered). Accessibility with axe: any serious problem fails, except low-contrast dim text, which is listed but doesn't fail yet. |
| `service.spec.ts` | Seven screens at once: Booth (desktop), Speaker (iPad landscape), Worship lead, Singer 1, Singer 2, Keys, Drums (iPhones). The booth sends cues on the Pulpit and Worship channels; each person answers from their own screen; the booth sees who answered and who hasn't; "One answer is enough", the text box, Clear, and the stream going off air and back on. |
| `editmode.spec.ts` | Every widget on every dashboard, on every screen size, measured after Done and again in edit mode (nothing selected, then a widget's settings open). Fails on any size difference over 2px: edit mode must show exactly what you get. Rules in `../WIDGETS.md`. |
| `sweep.spec.ts` | Every dashboard made for each kind of screen plus every Settings section, on iPhone SE, iPhone 15 (upright and sideways), iPad (both ways), a 1280×720 laptop and a 1920×1080 desktop. Each dashboard is also opened in edit mode with a widget's settings showing. Every page is checked for: page wider than the screen, widgets overlapping, things off the edge, text cut off without "…", scrollbars inside widgets, squeezed text, widget content cut off, tap targets under 28px on touch screens, buttons hidden under the phone's bottom tab, and errors in the page. |
| `sizes.spec.ts` | Every widget type (built-in and every add-on widget the running jadeck has, with fewest / most strips, buttons and long labels) alone on a board at its minimum, minimum width × very tall, full width × minimum height, the whole board and a middle size, on a 390×844 phone, a 1180×820 iPad and a 1920×1080 desktop. Inside the widget (and inside add-on frames) it checks: no scrollbars, no text under 10px, no text running out of its box or cut without "…", no two controls overlapping, no control under 28×28 on touch screens, nothing outside the box, no control cut off. Screenshots and `report.json` go to `out/sizes/` (gitignored). `SIZES_ONLY=<regex>` picks variants; run it with `--workers 8`. |

iPhone and iPad run in WebKit (Safari's engine), laptop and desktop in Chromium.

## Run it

1. Start jadeck, then the simulator with its setup (cameras, switcher, slides, mixer and starter dashboards):

   ```bash
   ptzsim --cameras 4 --setup http://127.0.0.1:8080
   ```

2. Run the tests (first time: `npm install && npx playwright install chromium webkit`):

   ```bash
   cd e2e && npm test
   ```

`npm test` runs both and writes `report/index.html`: every step with screenshots, every problem found, and a picture of every page on every screen. On a Mac the screenshots are embedded so the one file can be sent and opened anywhere.

`npm run flow` or `npm run sweep` runs one. `JADECK_URL` and `SIM_VMIX` point at other addresses.

## In CI (every push)

The `gui` job in `.github/workflows/ci.yml` builds jadeck with the standard add-ons, starts it blank with the simulator (`start-rig.sh`), then runs every test here in Chromium and WebKit. In CI any finding from the sweep or the size check fails the run. When something fails, the run's **gui-failures** download has the pictures, the diffs and jadeck's log.

Saved pictures are per OS (fonts differ): CI compares against `screens/linux/`. After a change that's meant to look different: Actions → ci → Run workflow, tick **Retake every saved screenshot**, then commit the **screens-linux** download into `e2e/screens/linux/`. A page with no saved picture yet gets one taken (also in that download).

On a Mac, the same rig: build `ptz` and `ptzsim`, then `e2e/start-rig.sh <ptz> <ptzsim>`. Mac pictures go to `screens/darwin/` (not committed).

## What it can't tell you

- Live video: the headless browsers don't play WebRTC, so video tiles show their placeholder.
- Real hardware: screens are emulated sizes and engines, not a physical iPhone or iPad (Safari's quirks on a real device, the notch, the keyboard pushing the page).
- Whether something *looks* right. The checks catch layouts that break; the screenshots are there for a person to glance at.
