# e2e: whole-team tests on every screen size

Two Playwright tests that drive a running jadeck the way a team would on a
Saturday, from many devices at once. jadeck is only one person's machine;
these stand in for the rest of the team.

| Test | What it does |
|------|--------------|
| `service.spec.ts` | Seven screens at once: Booth (desktop), Speaker (iPad landscape), Worship lead, Singer 1, Singer 2, Keys, Drums (iPhones). The booth sends cues on the Pulpit and Worship channels; each person answers from their own screen; the booth sees who answered and who hasn't; "One answer is enough", the text box, Clear, and the stream going off air and back on. |
| `sweep.spec.ts` | Every dashboard made for each kind of screen plus every Settings section, on iPhone SE, iPhone 15 (upright and sideways), iPad (both ways), a 1280×720 laptop and a 1920×1080 desktop. Each dashboard is also opened in edit mode with a widget's settings showing. Every page is checked for: page wider than the screen, widgets overlapping, things off the edge, text cut off without "…", scrollbars inside widgets, squeezed text, widget content cut off, tap targets under 28px on touch screens, buttons hidden under the phone's bottom tab, and errors in the page. |

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

## What it can't tell you

- Live video: the headless browsers don't play WebRTC, so video tiles show their placeholder.
- Real hardware: screens are emulated sizes and engines, not a physical iPhone or iPad (Safari's quirks on a real device, the notch, the keyboard pushing the page).
- Whether something *looks* right. The checks catch layouts that break; the screenshots are there for a person to glance at.
