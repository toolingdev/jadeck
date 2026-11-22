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

## 6. Look: professional, same dark style

Reference renders: `design/wing-polished-mock.png` and
`design/vmix-polished-mock.png` (sources `design/*-polished.svg`). Built-in
and add-on widgets, both the shared shapes and frames, follow these:

- **Cards:** a subtle vertical gradient (`#181C22` → `#111418`), a 1px
  border (`line`), radius `--radius-card`.
- **Source header** (`design/widget-source-headers-mock.png`, one
  component for built-in and add-on widgets: `Frame` / `WidgetHeader` in
  `frontend/src/dash/header.tsx`). Every widget says what it is tied to:
  - left: the title (12px bold), a 6px status dot (green connected, grey
    offline) and the SOURCE in 10px semibold caps, 1px letter-spacing,
    muted: the connection (VMIX, PROCLAIM, WING, an add-on connection's
    name), the camera's name, a video feed's sender (CAMERA, the add-on
    connection, SRT / RTSP pulled, PUSH) or the stage channel;
  - right, optional: live context, muted (`John 11:28–44 · 3 of 7`,
    `PGM 1 · PVW 2`, `streaming`);
  - offline: the dot goes grey, the source reads `PROCLAIM · OFFLINE` and
    the controls dim;
  - on a box shorter than ~84px the header folds away so the controls
    keep their room; a widget that is one big control (a button, the
    on-air pill, a tally light) then fills the whole box again.
  - Widgets tied to nothing (clock, countdown, a text note) have none.
- **Buttons:** a raised look, made of a gentle top-to-bottom gradient
  (`#262C35` → `#1D2229`), a 1px border and a 1px top highlight at 6%
  white. Labels are 13.5px bold, with an optional second line in 9px
  letter-spaced caps at 60% (e.g. "0.5 s", "ON AIR").
  - **Colours mean state only:**
    - red filled = on air / program / recording / muted;
    - green = preview / next;
    - amber outline + tint = selected or the primary action in this
      widget;
    - flat and dim = disabled.
  - **Size:** buttons in a group are the same size, at least 48px tall
    on touch screens.
- **Pictures:** rounded 10px. A dark gradient at the bottom carries the
  caption. A number tag sits top-left on a black pill. PGM/PVW pills sit
  top-right. The tally border is 3px with a soft glow of the same colour.
- **Readouts:**
  - numbers (dB, times, timecode) are monospace in a recessed pill (`#07080A`
    with a 1px border);
  - small labels are 9–10px letter-spaced caps in the dim colour.
- **Meters:** segmented LEDs (about 24–30 segments): green up to 70%,
  amber up to 90%, red above, with a peak-hold segment. Two columns for
  stereo, labelled L/R. Unlit segments are `#1C2128`.
- **Faders:** a recessed 6px track and a dB scale with 0 dB picked out.
  The cap is metallic (light gradient, centre line, grip lines, soft
  shadow).
- **Knobs:** a 270° ring. The value arc is in the colour for its job: amber
  for gain, blue for filters, grey for frequency. Bipolar values fill out
  from 12 o'clock. The body has a radial gradient, a white pointer, the
  value below in bold and a small caps label under it.
- **Status dots:** 12px, red when live. A pulse is for on air only.
- **Motion:** 120–160 ms ease on state changes. Meters animate with
  transforms or canvas, never by changing layout. Respect
  `prefers-reduced-motion`.

- **Risky actions are held, not tapped.** Start and stop stream or
  recording, and the like, use press-and-hold with a fill (default 5 s).
  See "Hold to confirm" in `design/WIDGETS-V2.md`.
