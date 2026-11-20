import { useState } from "react";
import { Plus, Pencil, Trash2, Eraser } from "lucide-react";
import { useLog } from "@/lib/log";
import { useChannels, useScreenName, setScreenName, stageApi, type ChannelState } from "@/lib/stage";
import { Button, Card, CardBody, CardHeader, Dialog, Input } from "@/components/ui";

const COLORS = ["#F5A524", "#60A5FA", "#4ADE80", "#F472B6", "#A78BFA", "#F87171", "#2DD4BF", "#E8EBF0"];

/** Settings → Stage channels: the channels message widgets link through, and this screen's name. */
export function StageSettings() {
  const { channels, online } = useChannels();
  const me = useScreenName();
  const [name, setName] = useState(me);
  const [edit, setEdit] = useState<{ id: string | null; name: string; color: string } | null>(null);
  const [del, setDel] = useState<ChannelState | null>(null);
  const [err, setErr] = useState("");
  const { log } = useLog();

  const save = async () => {
    if (!edit) return;
    try {
      if (edit.id) await stageApi.update(edit.id, edit.name, edit.color);
      else await stageApi.create(edit.name, edit.color);
      log("Stage", `Channel “${edit.name}” saved`);
      setEdit(null);
      setErr("");
    } catch (e) {
      setErr((e as Error).message);
    }
  };
  const remove = async () => {
    if (!del) return;
    try {
      await stageApi.remove(del.id);
      log("Stage", `Channel “${del.name}” deleted`);
    } catch (e) {
      log("Stage", (e as Error).message, false);
    }
    setDel(null);
  };
  const clear = async (c: ChannelState) => {
    try {
      await stageApi.clear(c.id);
      log("Stage", `Cleared “${c.name}”`);
    } catch (e) {
      log("Stage", (e as Error).message, false);
    }
  };

  return (
    <Card>
      <CardHeader title="Stage channels" meta={online ? "message widgets on the same channel are linked" : "connecting…"}>
        <Button size="sm" variant="accent" onClick={() => { setErr(""); setEdit({ id: null, name: "", color: COLORS[channels.length % COLORS.length] }); }}>
          <Plus size={14} /> New channel
        </Button>
      </CardHeader>
      <CardBody className="space-y-2">
        <label className="flex flex-wrap items-center gap-2 rounded-xl border border-line bg-panel-2 p-3 text-sm">
          <span className="text-muted">This screen's name</span>
          <Input value={name} onChange={(e) => setName(e.target.value)} onBlur={() => setScreenName(name)}
            onKeyDown={(e) => e.key === "Enter" && setScreenName(name)} className="h-9 w-48" maxLength={40} />
          <span className="text-[11px] text-dim">Shown on messages it sends and in “Seen ✓”. Each device keeps its own.</span>
        </label>
        {channels.map((c) => (
          <div key={c.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 rounded-xl border border-line bg-panel-2 p-3">
            <div className="min-w-0 flex-1 basis-56">
              <div className="flex items-center gap-2 text-[15px] font-bold" style={{ color: c.color }}>
                <span className="h-2.5 w-2.5 rounded-full" style={{ background: c.color }} />{c.name}
              </div>
              <div className="text-[11px] text-muted">
                {c.screens.length ? `Online: ${c.screens.map((x) => x.name).join(", ")}` : "No screen online"}
                {" · "}{c.messages.length} message{c.messages.length === 1 ? "" : "s"} today{c.timer ? ` · timer “${c.timer.label || "Timer"}”` : ""}
              </div>
            </div>
            <div className="flex gap-1">
              <Button size="sm" variant="ghost" onClick={() => { setErr(""); setEdit({ id: c.id, name: c.name, color: c.color }); }}><Pencil size={13} /> Rename</Button>
              <Button size="sm" variant="ghost" onClick={() => clear(c)} disabled={!c.messages.length}><Eraser size={13} /> Clear</Button>
              <Button size="sm" variant="ghost" className="text-bad" onClick={() => setDel(c)}><Trash2 size={13} /> Delete</Button>
            </div>
          </div>
        ))}
        <p className="text-[11px] text-dim">A channel is a name and a color. Put a Message sender and a Message display on the same channel — on any dashboards, any devices — and they talk to each other. Messages are kept for today only.</p>
      </CardBody>

      <Dialog open={!!edit} onOpenChange={(o) => !o && setEdit(null)} title={edit?.id ? "Rename channel" : "New channel"}>
        {edit && (
          <form className="space-y-3" onSubmit={(e) => { e.preventDefault(); save(); }}>
            <label className="block"><span className="mb-1 block text-xs text-muted">Name</span>
              <Input autoFocus value={edit.name} maxLength={40} onChange={(e) => setEdit({ ...edit, name: e.target.value })} placeholder="e.g. Kids room" />
            </label>
            <div>
              <span className="mb-1 block text-xs text-muted">Color</span>
              <div className="flex flex-wrap gap-2">
                {COLORS.map((col) => (
                  <button key={col} type="button" onClick={() => setEdit({ ...edit, color: col })} aria-label={col}
                    className="h-8 w-8 rounded-full border-2" style={{ background: col, borderColor: edit.color === col ? "#fff" : "transparent" }} />
                ))}
              </div>
            </div>
            {err && <p className="text-xs text-bad">{err}</p>}
            <div className="flex justify-end gap-2">
              <Button type="button" variant="ghost" onClick={() => setEdit(null)}>Cancel</Button>
              <Button type="submit" variant="accent" disabled={!edit.name.trim()}>Save</Button>
            </div>
          </form>
        )}
      </Dialog>
      <Dialog open={!!del} onOpenChange={(o) => !o && setDel(null)} title={`Delete “${del?.name}”?`}
        description="Its messages and timer go too. Widgets set to it will say “pick a channel” until you choose another.">
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={() => setDel(null)}>Keep it</Button>
          <Button variant="danger" onClick={remove}>Delete</Button>
        </div>
      </Dialog>
    </Card>
  );
}
