# Stage screens

A screen for whoever is up front: the speaker's iPad at the pulpit, the
worship leader's phone. The booth sends a short message ("5 min left",
"Speak up"); it appears big on their screen; they tap a reply ("👍 Got
it", "+2 min") and the booth sees it. Proclaim's confidence TV is not
touched; this sits beside it.

There is no special "receiver" mode. It is four dashboard widgets, in the
**Stage** group of the widget library, linked by **channels**.

## The widgets

| Widget | What it does |
|---|---|
| **Message sender** | Text box and/or quick buttons. Sends to one or more channels. With the text box off and "sends on tap" on, it is a row of reply buttons. Quick messages and timer buttons are edited in the widget's settings (one per line, reorder with the arrows). |
| **Message display** | Shows a channel. *Big*: the latest message as large as the box allows, down to a readable minimum; past that the person swipes to read the rest (a fade and "more" hint show there is more). *Conversation*: both directions, for the booth. |
| **Stage timer** | A channel's countdown, the same second on every screen (the server's clock). Started from a sender's timer buttons ("Sermon 30", "Worship 20:00"), the agent, or the timer's own pause / +1 / stop buttons when "controls" is on. |
| **Live status** | WE ARE LIVE while vMix is streaming, and/or this camera's tally: red = on program, green = on preview. The camera's vMix input is found by name, or picked in the settings. |

**Live view** (Camera group) can also be pinned to one camera and show a
red/green tally border, so several Live views make a multiview.

## Channels

A channel is a name and a color. Every message widget on the same channel
is linked, both ways, on any dashboard and any device. "Pulpit" and
"Worship" come ready. Manage them in **Settings → Stage channels**, or with
"+ New channel…" in any message widget's settings. Deleting a channel
leaves its widgets saying "pick a channel".

Each device has a **screen name** (Settings → Stage channels, "This
screen's name"). It labels the messages it sends and the "Seen ✓" it
gives. A device guesses one (iPad, iPhone, Mac…) until you set it.

## Ready-made dashboards

New in this version, added once (delete them and they stay deleted, and
they never become a device's default by themselves):

- **Stage — iPad**: live status, Pulpit timer, the Pulpit message big,
  the stream, the camera with tally, reply buttons.
- **Stage — phone**: the same for the Worship channel.

New installs also get a Message sender and a Pulpit conversation on the
Booth dashboard. On an existing install, add them to Booth yourself (Edit
→ Add widget → Stage).

## Setting up the pulpit iPad

1. Open the controller on the iPad, Dashboard → pick **Stage — iPad**.
2. Settings → Stage channels → set **This screen's name** (e.g. "Pulpit
   iPad").
3. Back on the dashboard, press **Full screen**. The header and tab bar go
   away (and the browser's bars too, where the browser allows it), Edit is
   hidden, and the screen stays awake (iPadOS 16.4 or later; on older iPads
   set Auto-Lock to Never). To leave, **tap the faint ✕ in the top-right
   corner twice**, or press Esc on a laptop. iPhone Safari can't hide its
   own bars: Share → Add to Home Screen and open jadeck from there.
4. For a hard lock, also use iPad Guided Access (outside this app).

## Behaviour worth knowing

- Messages are kept for **today only**, 280 characters each.
- A screen that reloads, sleeps or reconnects shows the current message
  at once. When it loses the controller it greys out and says *offline*,
  so an old message isn't read as new.
- Arrival is a pulsing border, **never a sound** (pulpit mics) and never a
  white flash (cameras are rolling).
- The big display hides the screen's own messages, so a reply doesn't
  cover the booth's message.
- Who can send follows the normal access rules: this PC, the venue
  network and Tailscale without a key; anyone else needs a key with the
  **Stage messages** permission. Creating, renaming and deleting channels
  is trusted-network only.
- Agents (MCP): `stage_status`, `stage_send`, `stage_timer`.

## API

```
GET    /api/stage                          channels with today's messages, timers, who's online
GET    /api/stage/events?screen=&channels= live updates (Server-Sent Events)
POST   /api/stage/channels                 {name, color}
PUT    /api/stage/channels/{id}            {name, color}
DELETE /api/stage/channels/{id}
POST   /api/stage/channels/{id}/messages   {text, from}
DELETE /api/stage/channels/{id}/messages   clear the screen
POST   /api/stage/channels/{id}/seen       {id, screen}
POST   /api/stage/channels/{id}/timer      {op: start|pause|resume|add|stop, label, seconds}
```
