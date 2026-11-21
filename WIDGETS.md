# Widget rules

How a dashboard widget must behave so a dashboard looks the same while you
build it, after you press Done, and on every screen it's made for. New
widgets follow these; `e2e/` checks them.

## 1. Edit is exactly what you get

Edit mode shows the dashboard at the size it will have after Done. Every
widget has the same width and height in both, to the pixel.

- **One canvas.** The grid's width, columns, row height, margins and padding
  come from the same rule in both modes (`view` in `DashboardPage.tsx`).
  Nothing that exists only while editing (the dashed edge, the grid lines,
  name chips, selection rings) takes up room: use `outline`, overlays or
  absolute positioning, never `border`/`padding` on the canvas.
- **Editing panels float over the board, never beside it.** They never make
  the board narrower (design page 19, option A):
  - Phones: the widget list and a widget's settings are sheets from the
    bottom; the tapped widget scrolls up above the sheet.
  - Tablets, sideways phones, laptops: settings float on the side away from
    the tapped widget, below the toolbar so Done is always tappable.
  - Desktop: the widget list is tucked into "+ Add widget" until wanted,
    floats on the left, fades out while you drag from it, and closes once a
    widget is added. Tapping a widget closes it and opens that widget's
    settings.
- **Arranged-for-you layouts preview as they are.** A phone held sideways
  shows two columns after Done, so editing sideways shows the same two
  columns. Moving widgets waits until the phone is upright; settings still
  open from a tap.
- **A dashboard keeps its screen's width.** A phone dashboard is 420px wide
  and a tablet dashboard at most 1180px, on any bigger screen, in both
  modes. A desktop dashboard uses the full width.

Check: `cd e2e && npx playwright test editmode.spec.ts` measures every
widget, on every screen size, with nothing selected and with a widget's
settings open, and fails on any difference over 2px.

## 2. Content fits its box

- **Fixed-size pieces stay fixed.** Tiles, buttons and rows get a set height
  (or a min/max), not `1fr` that stretches to fill. A big widget shows room
  below its tiles; it never turns them into tall slabs. (The Input bank tiles
  are 56 to 72px whatever the widget's size.)
- **Equal choices look equal.** Buttons that do opposite or parallel things
  (Prev / Next, Cut / Fade) get the same size and style. Colour means state
  (red program, green preview, amber selected), not "the important one".
- **Minimum size in pixels** (`minPx`) is what the content needs to be
  usable; the grid never lets the widget get smaller. It can depend on the
  widget's settings (`minPx: (s) => …`).
- **Grow to fit, never scroll inside.** A list that can get longer (presets,
  inputs) makes the widget taller instead of showing an inner scrollbar.
  The measurement compares outer heights on both sides and ignores strips
  that scroll sideways, so it settles instead of growing every pass. No
  widget is ever taller than 40 rows (2000px); anything taller is a leftover
  and is brought back.
- **Text shrinks or ends in "…".** Never cut off mid-letter, never wraps out
  of its box. Big status text (WE ARE LIVE) scales with the box.

## 3. Every widget says what it is

- The title is the widget's job, not its connection: **Inputs** with
  "vMix (simulated)" beside it in small caps, **Presets** with the camera
  chip. A person who didn't set it up can tell what it does.
- Names come from settings (cameras, inputs, channels) and update live when
  renamed.

## 4. Touch

- Anything tappable on a touch screen is at least 32px tall (28px is the
  hard floor the sweep fails on).
- Nothing important sits under the phone's bottom tab after scrolling to
  the bottom.

## 5. Screens

Dashboards are made per kind of screen (phone, tablet, desktop). A
dashboard is never tailored for a different kind of screen than its own.
The sweep checks each dashboard on the screens of its kind: iPhone SE,
iPhone 15 upright and sideways, iPad both ways, 1280×720 laptop, 1920×1080
desktop.

## Running the checks

With jadeck and the simulator up (`ptzsim --cameras 4 --setup …`):

```bash
cd e2e && npm test
```

`report/index.html` shows every step, every problem, and a picture of every
page. See `e2e/README.md`.
