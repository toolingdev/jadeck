# Agents (MCP) and API keys

## Who needs a key

| Where the request comes from | Key? |
|---|---|
| This PC, the venue LAN, Tailscale | No. Open the page and go. |
| Anywhere else | Yes |
| Any agent over MCP (`/mcp`) | Always. Agents are scoped by their key. |

A request that sends a key is always held to that key's permissions, even
on the LAN. Keys are managed on the **Settings** page, which only works
from the trusted network; no key can create or change keys.

Change who needs no key in **Settings → System → Who can use it without
a key**: switch off "Venue network & Tailscale" to trust only this PC, or
add extra networks. It applies immediately.

## Make a key

Settings → API keys → **New key**. Name it after the agent ("Claude —
camera"), switch on only the permissions it needs, and copy the key. It is
shown once. Each key can be turned off and on, have permissions toggled,
be revoked (kept for the audit trail) or deleted. Every agent action and
every keyed change lands in the audit log.

| Permission | Allows |
|---|---|
| camera.read | status, snapshot, presets list, picture settings |
| camera.move | bounded moves, zoom, home, stop, focus, recall presets |
| camera.image | picture settings, white-point pick |
| presets.edit | save / rename / delete presets |
| switcher | preview, cut, fade |
| slides | presentation actions |
| mixer | channel mutes (verified by read-back) |
| macros.run / macros.edit | run, or create / change / delete macros |
| update.apply | check for and apply signed updates |

## Connect an agent

Claude Code:

```
claude mcp add --transport http ptz http://<main-pc>:8080/mcp --header "Authorization: Bearer ptz_…"
```

Any MCP client that speaks Streamable HTTP works: endpoint `/mcp`,
header `Authorization: Bearer <key>`.

## Tools

The agent only sees tools its key allows. Camera motion is bounded per call
(`camera_move` ±90° pan / ±60° tilt, `camera_zoom` ≤ 3 s). An agent never
gets an open-ended drive.

camera_status · camera_snapshot · presets_list · image_settings_get ·
camera_move · camera_zoom · camera_home · camera_stop · camera_focus ·
preset_recall · image_settings_set · white_balance_pick · preset_save ·
preset_rename · preset_delete · switcher_status · switcher_preview ·
switcher_cut · switcher_fade · slides_status · slides_action ·
mixer_status · mixer_mute · macros_list · macro_run · macro_save ·
macro_delete · update_check · update_apply · update_status · devices_info
