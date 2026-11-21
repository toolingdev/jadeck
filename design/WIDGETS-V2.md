# Add-on widgets v2

An add-on can bring any number of widgets. Each one is either drawn by
jadeck from one of the shared **shapes** below, bound to the add-on's state
(consistent look, WIDGETS.md rules for free), or **drawn by the add-on** in a
sandboxed frame for anything the shapes can't do.

The protocol goes to `apiVersion` 2. The host speaks 1 and 2. A v1 add-on is
unchanged. A host that meets a widget kind it doesn't know leaves that widget
out (it logs a warning) instead of rejecting the add-on.

## Manifest: `WidgetDef`

```jsonc
{
  "kind": "image",               // core | status | list | chat | (v2) image | buttons | input | strip | text | clock | sections | slider | frame
  "type": "live_slide",          // the add-on's widget id
  "label": "Live slide",
  "description": "...",
  "bind": { ... },               // per kind, below; values are state keys unless noted
  "size":  { "w": 6, "h": 6 },   // optional default grid size (desktop); host clamps
  "minPx": { "w": 240, "h": 160 },
  "entry": "ui/live.html",       // frame only
  "sample": { ... }              // state for the preview, as today (required)
}
```

## Shapes (`bind`)

State keys hold plain JSON. An "action" is the id of one of the add-on's
actions. The host sends it to `POST /api/connections/{id}/actions/{action}`
with the params shown.

| kind | bind | draws |
|---|---|---|
| `image` | `src` (key holding a resource path or `data:` URL), `caption?`, `badge?` (short text, e.g. LIVE), `tone?` (key: `pgm`/`pvw`/`none`), `tag?` (key or text: a short number/label on a black pill top-left), `action?` (tap), `actionParams?` (static object) | a picture that keeps its aspect ratio; caption on a dark gradient at the bottom, badge pill top-right, 3px tally ring with a soft glow |
| `buttons` | `items` (key: `[{id,label,sub?,tone?,active?,disabled?,params?}]`; `sub` is an optional second line in small caps, e.g. `"0.5 s"`, `"ON AIR"`; `tone` `pgm` = red (on air / muted), `pvw` = green (next), `accent` = the widget's primary action; `active` = selected (amber)) or static `buttons` (same shape, in `bind`), `action` (default for every button; a button's `params` is sent; an item may override with `action`), `columns?` | grid of equal raised buttons (WIDGETS.md: equal choices look equal); a big box is filled: columns from the button count and the box's shape, rows 48–160px, labels grow a little |
| `input` | `action`, `param` (name of the param the text goes into), `placeholder?`, `choices?` (key or static `[{value,label}]`), `choiceParam?`, `button?` (label) | text box + optional chips + a send button |
| `strip` | `items` (key: `[{id,src,label?,active?,next?}]`), `action`, `param?` (default `index`) | a row of thumbnails that scrolls sideways; tap one to go there |
| `sections` | `sections` (key: `[{id,title,meta?,open?,items:[{id,title,icon?,sub?,active?}]}]`), `action`, `param?` (default `id`), `current?` | a list folded into sections (`open` = starts unfolded; the section holding the current row always does); the current row is amber with its `sub` line |
| `text` | `title?`, `body` (key: plain text or a small safe HTML subset: p, br, b, i, ul, ol, li) | notes, readouts |
| `clock` | `at?` (key: ISO start time to count from/to), `label?` | wall clock + count up/down to `at` |
| `slider` | `value` (key 0..1 or min..max), `min?`, `max?`, `action`, `param?` (default `value`), `label?`, `unit?` (readout shows the value with this unit, e.g. `dB`, instead of a percent), `ticks?` (a count of even marks, or a scale `[{value,label?}]`; a labelled mark at 0 is picked out) | a fader (recessed track, metallic cap, monospace readout; T-bar, volume); sends on release and while dragging (throttled) |

Every shape shows the same connection / loading / error states as today.

## Resources (pictures from the add-on)

New call method `addon.resource {path}` → `{contentType, data}` (data is
base64; at most 4 MB). The host route is
`GET /api/connections/{id}/res/{path...}`. It needs the same permission as
reading that connection's state. Responses are cached for 2 s. In state,
add-ons refer to a picture as `"res:slides/<item>/<n>.png?w=640"`, and the
host rewrites that to the route.

SDK: a connection may implement

```go
type Resourcer interface {
    Resource(ctx context.Context, path string) (contentType string, data []byte, err error)
}
```

## Frame widgets (add-on draws)

- The package may hold a `ui/` folder (html/js/css/svg/png/woff2, at most
  2 MB in all). Each file is covered by SHA256SUMS like everything else.
- Host route `GET /api/addons/{id}/ui/{path...}`, served with
  `Content-Security-Policy: default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self'; connect-src 'none'; frame-ancestors 'self'`
  and `X-Content-Type-Options: nosniff`.
- The dashboard renders `<iframe sandbox="allow-scripts" src=...>`. With no
  `allow-same-origin` the frame has an opaque origin, so it has no cookies,
  no API access and no storage. The bridge is the only way out.
- Bridge (postMessage, versioned):
  - host → frame:
    `{t:"init", v:1, state, settings, theme:{...css vars}, size:{w,h}, editing, touch}`;
    then `{t:"state", state}` / `{t:"size", size}` / `{t:"theme"}` on change.
  - frame → host:
    - `{t:"action", id, name, params}` → `{t:"result", id, ok, error?, result?}`
    - `{t:"resource", id, path}` → `{t:"result", id, ok, blob}`
    - `{t:"log", text}`
    - `{t:"ready"}`
- The host checks every message: `event.source` is that frame, `name` is
  one of the add-on's declared actions, and at most 10 actions per second.
  Actions go through the normal API, so the user's permissions and the audit
  log apply.
- While the board is in edit mode the frame gets `editing:true` and sits
  under a transparent cover, so drags and taps go to the editor, not the
  widget.
- WIDGETS.md still applies. The frame must fill its box without scrolling,
  use the theme variables, and keep tap targets at 32 px or more. editmode
  and the sweep measure frames like any widget.

## Compatibility

- SDK `APIVersion = 2`. `Validate` accepts the new kinds when `apiVersion
  >= 2`, requires `entry` for frames, and requires `bind.action` to name a
  declared action.
- The host accepts manifests with apiVersion 1 or 2.
- The store shows "needs jadeck 0.5" for apiVersion 2 add-ons on older
  controllers.

## Settled while building (both sides match)

- **Frame entry:** `entry` is written `ui/<file>.html` in the manifest. The
  route path is relative to the package's `ui/` folder
  (`/api/addons/{id}/ui/<file>.html`).
- **`init` can arrive again.** It is sent after every `{t:"ready"}` and
  again when editing or the settings change. Theme updates carry their
  values: `{t:"theme", theme}`.
- **Results:**
  - actions: `{t:"result", id, ok, result?|error?}`
  - resources: `{t:"result", id, ok, blob?|error?}`
  - The action route answers `{ok, result}`.
- **Frame pages must declare `color-scheme: dark`.** Otherwise the browser
  paints white behind the frame.
- **Strip param:** with the default param `index`, the 0-based position is
  sent. With any other param name, the item's `id` is sent.
- **Input chips:** without `choiceParam`, a chip sends its value as the text
  right away. With `choiceParam`, chips pick a value that is sent along with
  the text.
- **Pictures:** `src` must be `data:image/...` or `res:...` (the host
  rewrites `res:`). In the Settings preview there is no connection, so
  samples should use `data:` URLs.
- **Section `icon`:** a known word (song, video, image, slide, scripture,
  text, announcement, timer…) or a 1–2 character glyph (♪ ✝).
- **Limits:** frames get 10 actions/s, 30 resources/s and 5 logs/s.
- **Card sizes:** a manifest `size` is clamped to 1–12 × 1–40 grid cells.
  The phone width is `min(4, ceil(w/3))`. `minPx` is clamped to 40–1200 ×
  28–2000.
- **Building:** `npm run build` writes into `web/dist`, which git tracks. To
  check a build without touching it, use `npx tsc -b && npx vite build
  --outDir <tmp>`.

v2.1 (per-widget settings and fast data), still `apiVersion` 2; older
controllers and add-ons ignore the new fields:

- **Field keys:** unique per widget, a known type, and not `connection`,
  `addon` or `widget` (the controller's own widget settings). `multiple`
  is only for `select`. `select` and `order` need `options` or
  `optionsFrom`. These checks run for every apiVersion; connection-type
  fields are not type-checked (unchanged).
- **Values:** `select` + `multiple` and `order` save a string array.
  Multi-select keeps the options' order; `order` keeps the picked order.
  A saved value the options no longer have stays listed as "(not found)"
  so it can be removed.
- **`$settings.<key>` binds:** only a whole bind value (anywhere in
  `bind`, nested lists and objects too) is replaced, by the setting's
  value of any type. The result is then read by the usual bind rules: a
  string names a state key (or is text, for labels), an array is a static
  list, a number a static value. Validate rejects a reference to a key the
  widget doesn't declare. Actions can't come from settings.
- **Defaults:** a new widget is saved with its fields' defaults. Older
  widgets, frames (`init.settings`), binds and the add-on all fill in
  missing values from the defaults.
- **Options:** `GET /api/connections/options/{addon}/{name}?conn=<id>`.
  The host asks that connection's `Optioner` (`options {name, conn}` for a
  process). An empty answer, an error, or a connection of another add-on
  falls back to `Spec.Options` (the old behaviour). In the Inspector the
  connection is the widget's own, else the add-on's first one. A widget's
  single `select` with `optionsFrom` is a dropdown of the live list (the
  connection form keeps its free-text box).
- **State per widget:** `GET /api/connections/{id}/state?widget=<id>&dash=<dashboard id>`.
  `dash` is optional but the dashboard sends it, because a duplicated
  dashboard repeats widget ids. The host finds that widget (type
  `addon:<this add-on>:…`, connection this one or unset) and sends
  `addon.state {settings}`. The settings hold only the declared field keys,
  with defaults filled in, and are `{}` when nothing is set. With no such
  widget, or a widget without fields, it sends a plain `addon.state`. The
  dashboard adds `?widget=` only when the widget has fields. The host reads
  the saved settings, so the widget asks again 1.5 s after an Inspector
  change (after autosave).
- **SDK:** `SettingsStater.AddonStateFor(ctx, settings)` is called when
  settings came, or always if the connection has no `AddonState`.
  `Optioner.AddonOptions(ctx, name)` returns nil to fall back.
  `ResultResourcer.ResourceResult(ctx, path)` replaces `Resource` when
  present. `addon.Age(n)` builds a `maxAge`.
  `addontest.Harness.Options/State/ResourceResult` exercise all three.
- **maxAge:** unset keeps 2 s, 0 is never cached, and values are clamped
  to 0–3600 s (a negative one becomes 0). `Cache-Control` follows:
  `private, max-age=N` or `no-store`.
- **Coalescing:** requests for the same (connection, path) share one
  add-on call if they arrive while it is running or within 50 ms of its
  start. That holds for maxAge 0 too. Errors are shared the same way. The
  call runs on its own 5 s context, so one screen going away doesn't fail
  the others.

## v2.1: per-widget settings and fast data

### Widget settings an add-on defines
`WidgetDef.fields` is a list of `Field`, the same type connection settings
use. They are shown in the widget's settings panel (Inspector) under
"Connection", and the values are saved with the widget on the dashboard.

- New field option `multiple: true` for `select` (the value becomes a string
  array) and a new type `order` (an ordered multi-select: pick and drag to
  reorder).
- `optionsFrom` works here too. The host calls the add-on's options source
  for that widget's connection (`options {name, conn}`), so a WING widget can
  offer the live channel list: "Ch 1 · Speaker", "Aux 3 · …", "DCA 2 · …".
- Shapes may name a setting in a bind as `"$settings.<key>"`. Frames get
  every setting in `init.settings`, and again whenever one changes.
- The add-on also receives the settings: `addon.state` gets
  `{settings}` for the widget that asks (state requests carry
  `?widget=<dashboard widget id>`, and the host looks up its settings). It can
  then send only what that widget shows. This is optional; most add-ons
  ignore it.

### Fast data (meters)
- `ResourceResult.maxAge` (seconds; 0 = never cache). It replaces the fixed
  2 s cache, and leaving it unset keeps 2 s. A frame polls a fast resource
  such as `res:meters?ids=ch1,ch2&fmt=bin` up to 30 times a second (the
  frame resource limit).
- The host may answer several frames asking for the same resource path
  within 50 ms with one add-on call, so 5 screens showing meters cost one
  read.

## Hold to confirm (risky actions)

Starting or stopping a stream or a recording, and anything else that would
be bad to set off by accident, is pressed and **held**. There are no
confirm pop-ups and no second tap. Mock: `design/hold-button-mock.png`.

- **Button items (shapes):**
  - `hold` (seconds) on a button item, or in `bind.hold` for every button.
  - An optional `holdLabel` ("Keep holding…").
  - The widget setting `holdSeconds` (3–10, default 5) can override it
    through `$settings.holdSeconds`.
- **Behaviour:**
  - While held, the button fills from left to right in the action's colour
    (red for stop and record) and the second line counts down ("STOPS IN
    3 S").
  - Letting go early, or sliding off the button, drains the fill back
    (about 300 ms) and sends nothing.
  - At the end it sends the action once and vibrates on phones that can.
  - The idle second line says "HOLD 5 S" so people know without being
    told.
  - Space or Enter, held, works the same way.
  - Under reduced motion the fill still grows, without the shimmer.
- **Host:** the `buttons` shape draws held buttons with
  `frontend/src/dash/addon/hold.tsx` (idle: raised, edged in the action's
  colour, sub line "HOLD n S · <sub>"; held: `holdLabel` or "Keep holding…"
  and "STOPS IN n S" / "STARTS IN n S" / "SENDS IN n S"). Settings →
  Add-ons has a "Try it" card that sends nothing.
- **Frames:** frames use the same pattern. Each add-on's ui kit gets a
  `holdButton()` helper that follows these timings and visuals.
- **The add-on still decides:** the host never sends a held action early.
  Add-ons may also require a `confirmed: true` param for these actions as
  a second safety.
