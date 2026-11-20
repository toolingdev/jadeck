# Add-ons

The controller itself knows cameras, dashboards, video feeds, stage
messages and macros, but nothing about any particular product. An
**add-on** teaches it one product: vMix, the Behringer WING, Proclaim, and
so on.

Each add-on:

- adds **connection types**, under Settings → Connections;
- adds **widgets** to dashboards;
- adds **macro steps**;
- adds **agent tools** (MCP).

Add-ons are separate projects with their own releases. Each one runs in
its own process, so an add-on that crashes or updates never takes the
cameras down.

## Using add-ons

**Settings → Add-ons** lists what's installed. Pick one to see what it
adds. The previews there are the real widgets, drawn with sample data.

- **On / off:** turning an add-on off keeps its connections and widgets
  saved. They show that the add-on is off until you turn it back on.
- **Updates:** "Check for updates" asks each add-on's GitHub repository
  for a newer signed release. Update installs it next to the old one,
  checks that it starts, then swaps it in. Only that add-on restarts.
  Roll back restores the previous version.
- **Verified:** packages signed with the jadeck release key show
  ✓. Anything else installs only after you confirm, with a warning. An
  add-on runs as a normal program on this computer, so install only what
  you trust. Its "It can" line is what it *says* it does; the controller
  doesn't enforce that.
- **Browse:** add-ons you can download, from the catalog in this
  repository (`addons/catalog.json`). Install fetches the add-on's latest
  GitHub release.
- **Install from file:** a `.ptzaddon` package.
- **Load a folder (dev mode):** run an add-on straight from its project
  folder while you work on it.

Every release of the controller bundles the standard add-ons, so a new
install works offline:

| Add-on | What it adds | Project |
|---|---|---|
| vMix | Switcher: inputs, preview, cut, fade, tally, live/recording | [jadeck-addon-vmix](https://github.com/toolingdev/jadeck-addon-vmix) |
| Behringer WING | Mixer: channel mutes, read back (X32/M32 experimental) | [jadeck-addon-behringer-wing](https://github.com/toolingdev/jadeck-addon-behringer-wing) |
| Proclaim | Slides over Proclaim's network control (no MIDI): prev/next, blank, go to a service item; On air and Service items widgets | [jadeck-addon-proclaim](https://github.com/toolingdev/jadeck-addon-proclaim) |
| MIDI notes | Generic slides: one MIDI note per action | [jadeck-addon-midi](https://github.com/toolingdev/jadeck-addon-midi) |
| HTTP API | Generic slides: one web request per action | [jadeck-addon-http-api](https://github.com/toolingdev/jadeck-addon-http-api) |
| Simulated devices | A practice switcher, slides and mixer | [jadeck-addon-simulated](https://github.com/toolingdev/jadeck-addon-simulated) |

More are under **Browse**, for example:

| Add-on | What it adds | Project |
|---|---|---|
| AI controller | An Assistant widget: tell the booth what to do in plain words ("in 5 minutes cut to camera 2") with your own AI service and key | [jadeck-addon-ai-controller](https://github.com/toolingdev/jadeck-addon-ai-controller) |

## The AI controller

The AI controller is a **controller add-on**: instead of teaching the
controller a product, it *uses* the controller, through the same agent
tools an MCP client gets.

- **Your service, your key:** Anthropic (Claude), OpenAI, Google Gemini,
  Ollama on your own network, or any OpenAI-compatible service. What you
  type, and the status it reads, go to that service.
- **"It may":** the connection's permission list. The assistant gets a
  token with only those permissions, held in memory and replaced on every
  restart. It can never manage add-ons or apply updates.
- **Ask before acting** (on by default): reading status runs at once;
  anything that changes something waits for **Do it** on the widget.
- **Later:** "in 5 minutes…" or "at 10:35…" becomes a scheduled action.

## Scheduled actions (Coming up)

Macro steps, or a saved macro, set to run at a time. The **Coming up**
widget shows them on every screen with a countdown; anyone can **Run now**
or **Cancel**. They're saved, so they survive a restart; one whose time
passed while the controller was off is marked missed, not run late.
Each step is checked against the permissions of whoever scheduled it.

## Connections

**Settings → Connections** holds any number of each kind. Two Proclaim
PCs are two connections.

- The **first connection** of a kind is the default.
- A widget can pick another in its settings. The **Connection** field
  appears when there's more than one to choose from.
- **Cameras on a switcher:** a switcher connection has a small table of
  which camera is on which input. Tally (red live, green next) on every
  dashboard uses it, so renaming a camera never breaks tally. A camera
  left on "Match by name" pairs with the input whose title names it.
- **Passwords** are write-only: the screen never shows them back.
  Leave the field blank to keep the saved one.
- **Migration:** a `devices.json` from before add-ons is turned into
  connections on first start. The old file is kept as
  `devices.json.v1.bak`.

## Writing an add-on

An add-on is a small Go program built with the SDK
(`github.com/toolingdev/jadeck/sdk/addon`):

```bash
go run github.com/toolingdev/jadeck/sdk/addon/cmd/ptz-addon@latest new acme --name "Acme Switcher" --module github.com/you/jadeck-addon-acme
cd acme
go mod tidy
go test ./...
go run github.com/toolingdev/jadeck/sdk/addon/cmd/ptz-addon build
```

`build` produces:

- a program for Windows, Mac and Linux (x64 and ARM);
- `addon.json`;
- `dist/acme.ptzaddon`, signed when `PTZ_SIGNING_KEY` (or `--key`) is
  set.

The scaffold includes a GitHub workflow that builds and attaches the
package on every `v*` tag.

### The parts

```go
addon.Serve(addon.Spec{
	Manifest: addon.Manifest{ /* id, name ≤40, version, description ≤100, reaches, repo,
	                             connectionTypes, actions, widgets */ },
	Open:    map[string]addon.OpenFunc{"acme": open}, // connection type → opener
	Options: map[string]func() []addon.Option{},       // live choices for select fields
})
```

**A connection** is whatever `open` returns. It implements one or more
capability interfaces. Each capability brings built-in widgets, macro
steps and agent tools that work with any connection that has it:

| Capability | Interface | Built-in widgets |
|---|---|---|
| `switcher` | `State`, `Preview`, `Cut`, `Fade` | Cut / Fade, Input bank, Live status, tally borders |
| `slides` | `Info`, `Actions`, `Do(action)` | Prev / Next, Slide button |
| `mixer` | `Probe`, `MaxChannel`, `GetMute`, `SetMute` (always read back) | Mute button, Favorites |
| `assistant` | `AddonAction` (`chat`, `approve`, `reject`) | none; declare a `chat` widget |

**Product-specific extras** come from two optional interfaces:

- `AddonState(ctx)` returns a map for the add-on's own widgets.
- `AddonAction(ctx, name, params)` runs `Manifest.Actions`.

Every action is three things at once: a button, a macro step (when
`MacroStep` is set) and an agent tool named `<addon>_<action>`.

**Widgets** are declared, not coded:

- `kind: "list"` binds a state list to an action, e.g. Proclaim's service
  items.
- `kind: "status"` is a light with a title, e.g. On air.
- `kind: "chat"` is a conversation box bound to `chat` / `approve` /
  `reject` actions (the AI controller's Assistant).
- `kind: "core"` points at a built-in widget your connection powers.

Every widget needs `sample` data. That's what the Add-ons screen draws as
its preview, and `build` refuses a widget without it.

### The protocol

JSON-RPC 2.0, one JSON object per line, over the add-on's stdin and
stdout. Its stderr goes to the controller's log. The full list is in
`sdk/addon/protocol.go`:

- `initialize`, `open`, `close`, `call`, `options`, `shutdown` (controller
  → add-on);
- `log`, `changed` (add-on → controller).

The SDK does all of this; any language that can read and write lines
can implement it.

### Controller add-ons

Set `Controller: true` in the manifest and give the connection type a
`scopes` field named `permissions`. When a connection opens, its config
gets `_controller`: the controller's address and a token limited to the
ticked permissions. Read it with `addon.ControllerFrom(config)` and call
the controller's `/mcp` or REST API with it. A controller add-on adds no
agent tools of its own.

### Testing

`addontest.Start(t, spec())` runs your add-on over an in-memory pipe the
way the controller does. Open connections and call methods in plain Go
tests; no controller is needed.

### For agents (MCP)

A key with the **Manage add-ons** scope can:

- `addon_dev_load`: load a project folder in dev mode;
- `addon_restart`: restart an add-on after a change;
- `addon_logs`: read its log;
- `addon_list`: see what's installed;
- run any add-on action tool.

An agent can write an add-on, try it and fix it without anyone opening
the Settings screen.
