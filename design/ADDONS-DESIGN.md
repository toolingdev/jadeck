# Add-ons: design (draft for review)

Goal: the controller core knows cameras, dashboards, video feeds, stage
messages, macros and access, and nothing about any brand. Each product
(vMix, Behringer WING, Proclaim, ...) is an **add-on** that adds connection
types, widgets, macro steps and agent tools. Add-ons install, update and
roll back on their own, without a controller release and without
restarting the controller. Anyone (or any agent) can write one with the SDK.

A typical install uses three: **vMix**, **Behringer WING**, **Proclaim**.
Another venue installs different ones.

## What an add-on can add

| Contribution | Example (Proclaim) | Where it shows up |
|---|---|---|
| Connection type | "Proclaim": address + network-control password | Settings → Connections → Add |
| Capabilities | `slides.control`, `slides.onAir` | Generic widgets work with any add-on that has them |
| Widgets | Service items list, On-air light | Dashboard → Add widgets (grouped under the add-on) |
| Actions | next, prev, goToItem, blank | Buttons, macro steps, agent tools (one definition, three uses) |
| State | current item, on air | Widgets update live; agents can read it |
| Feed helpers | vMix: "paste these SRT settings" | Settings → Video feeds |

**Capabilities are the key idea.** The core defines a few small contracts:
`switcher.cut/fade/preview`, `switcher.tally`, `switcher.live`,
`slides.control`, `mixer.mute`, `mixer.channels`. The core's generic widgets
(Prev / Next, Cut / Fade, Mute button, tally border on video) talk to *any*
connection that has the capability. So "Prev / Next" works with Proclaim,
ProPresenter or the MIDI add-on, and an add-on only ships its own widgets
for things that are truly product-specific (Proclaim's service-item list).

## Show, don't tell: each add-on brings its own previews

Settings → Add-ons doesn't *list* what an add-on adds; it **shows** it. Each
add-on is responsible for that:

- **Widgets:** every widget ships **sample state** in the manifest, for
  example `{ "onAir": true, "item": "Amazing Grace", "slide": 3, "of": 6 }`.
  The controller draws the real widget with that data: same code, same look
  as on a dashboard, just not live. A custom (iframe) widget must also ship
  a preview image.
- **Connection type:** the form is drawn from its field list, so the preview
  is the real form with sample values and a sample "Test ✓" result.
- **Actions:** shown as ▶ macro-step chips and ✦ agent-tool chips, using the
  add-on's own labels.
- **Text is only for what can't be drawn:** a one-line description, what it
  can reach ("It can"), and what's new.

The list stays compact: one short row per add-on (name, version, ✓ if
verified, on/off; an update shows inline). The panel on the right is the
one place that shows everything, full size. Manifest limits keep it that
way: name ≤ 40 characters, description ≤ 100 (shown only in the panel). The Add widgets panel uses the same previews, so you see a widget
before you drop it. The SDK's `ptz addon pack` refuses to pack an add-on
whose widgets have no sample state.

## How it runs

```
 controller (Go, the core)                      add-on process (any language)
 ┌──────────────────────────────┐   stdio        ┌──────────────────────────┐
 │ add-on host: start, watch,   │◀── JSON-RPC ──▶│ SDK: describe(), open(),  │
 │ restart, update, roll back   │   (MCP-shaped) │ call(action), state       │
 │ connections · capabilities   │                │ talks to vMix / WING /    │
 │ widgets · macros · MCP       │                │ Proclaim on the network   │
 └──────────────────────────────┘                └──────────────────────────┘
```

- **One process per add-on**, started by the controller and talking JSON-RPC 2.0
  over stdin/stdout. The messages follow MCP's shape (initialize, tools/list,
  tools/call, notifications), plus a few extra methods: `connection/test`,
  `connection/open`, `state/changed`.
- **Why separate processes?**
  - An add-on that crashes or hangs can't take down the cameras. The host
    restarts it, and only its widgets show "reconnecting".
  - Updating an add-on restarts only that add-on, never the controller.
  - Add-ons can be written in Go, TypeScript, Python... (Go's own `plugin`
    package doesn't work on Windows, which rules it out.)
- **Agents get every add-on action automatically.** The controller re-exports
  each add-on's actions as tools on its own MCP endpoint
  (`proclaim_next`, `vmix_cut`, ...). Scoped API keys still apply, so a
  "slides only" key can't cut vMix.

## Widgets without writing UI

Most add-on widgets are **declarative**: the manifest says "a button grid of
these actions", "a list bound to state `items`, tap runs `goToItem`", "a light
bound to `onAir`". The core draws them in the house style, with the measured
min-sizes, dark theme, phone/iPad layout and lock screen. No add-on ships
React code for the common case, and widgets look the same whoever wrote them.

For truly custom UI (later), an add-on can ship a small web bundle that
runs sandboxed in an iframe. It talks to the controller through the SDK's
postMessage bridge and can't touch anything else.

## Package, trust, updates

- A **`.ptzaddon`** file is a zip with:
  - `addon.json`: id, name, version, publisher, API version, contributions,
    the network it needs;
  - the add-on program for each OS (Windows, Mac, Linux; x64 and ARM);
  - README, CHANGELOG, icon;
  - a signature.
- **Verified** add-ons (ours) are signed with the same release key as the
  controller. Others install with a clear "not verified" warning.
- **Honest limit:** a native add-on process can't be fully sandboxed. The
  "It can" list shows what the add-on declares, and only signature and trust
  back that up. A WebAssembly runtime could sandbox community add-ons later.
- **Updates** come per add-on from its release feed. Each update:
  - checks the signature;
  - installs side by side and swaps in one step;
  - health-checks the add-on and rolls back automatically if it fails to
    start.
  
  Updates are never applied automatically during service hours.
- **Separate projects, bundled copies:** every add-on is its own project
  and repository (ptz-addon-vmix, -behringer-wing, -proclaim, -midi,
  -http-api, -simulated) depending only on the SDK module. A controller
  release embeds each one's latest signed package (this platform's
  program only) and installs it on first start, so a fresh install works
  offline; newer versions from the add-on's own releases take over.

## SDK

- **Go SDK first** (our own add-ons use it): `addon.Run(addon.Spec{...})`
  with typed connection fields, actions, state and capabilities.
- **TypeScript SDK next**, same protocol.
- **Template repo**, plus these commands:
  - `ptz addon new`, `ptz addon dev ./folder` (live reload, logs in the
    console), `ptz addon pack`;
  - a **test harness** that plays the controller side, so an add-on can be
    tested without a venue.
- **Protocol spec** is versioned (`apiVersion: 1`); the host supports old
  versions for a long time.
- **MCP for agents building add-ons** (trusted keys only):
  - `addon_list` and `addon_logs`;
  - `addon_dev_load` (load a folder in dev mode) and `addon_reload`;
  - `addon_call` (run an action and see the result);
  - `addon_install` (needs the trusted scope).
  
  An agent can write an add-on, load it, try it against a simulated device
  and read its logs, all over MCP.

## Moving today's code over

| Today (inside the binary) | Becomes |
|---|---|
| `internal/devices/vmix` | **vMix** add-on (+ SRT setup helper) |
| `internal/devices/osc` (WING preset) | **Behringer WING** add-on (X32/M32 experimental) |
| `internal/devices/midi` | **MIDI notes** generic add-on |
| `internal/devices/httppres` | **HTTP API** generic add-on |
| `internal/devices/sim` | **Simulated devices** add-on |
| (new) | **Proclaim** add-on, using Proclaim's network control API (to confirm from Faithlife's docs before building) |

Existing setups migrate on update: today's single switcher, slides and
mixer become one connection each, the favorites move into the mixer
connection, and dashboards keep working.

## Suggested phases

1. **Foundation.** Add-on host and protocol, Go SDK, capabilities. Port vMix,
   WING, MIDI, HTTP and Simulated. Settings → Add-ons and Connections (any
   number of each). Migration.
2. **Proclaim add-on.** Then Video feeds (generic sources), the
   Dashboard-only top bar and the single "Video" widget.
3. **Opening up.** Catalog and per-add-on updates, signing for third
   parties, TypeScript SDK, `ptz addon` dev tools, MCP dev tools, custom
   iframe widgets.
