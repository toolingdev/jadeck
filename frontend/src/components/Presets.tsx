import { useState } from "react";
import { ChevronUp, ChevronDown, Trash2, Save, Plus, Pencil, Check } from "lucide-react";
import { api, type Preset } from "@/lib/api";
import { usePoll } from "@/lib/hooks";
import { useLog } from "@/lib/log";
import { cn } from "@/lib/cn";
import { Button, Card, CardBody, CardHeader, Dialog, Empty, Input } from "@/components/ui";

export function usePresets() {
  return usePoll<Preset[]>("/api/presets", 15000);
}

export function PresetGrid({ compact, editable = true }: { compact?: boolean; editable?: boolean }) {
  const { data, reload, setData } = usePresets();
  const { log } = useLog();
  const [edit, setEdit] = useState(false);
  const [active, setActive] = useState<number | null>(null);
  const [rename, setRename] = useState<Preset | null>(null);
  const [name, setName] = useState("");
  const [confirm, setConfirm] = useState<{ p: Preset; what: "store" | "delete" } | null>(null);
  const [adding, setAdding] = useState(false);
  const list = data ?? [];

  const recall = async (p: Preset) => {
    setActive(p.id);
    try {
      await api(`/api/presets/${p.id}/recall`, "POST");
      log("Camera", `Preset “${p.name}”`);
    } catch (e) {
      log("Camera", `Preset “${p.name}” failed — ${(e as Error).message}`, false);
    }
  };
  const move = async (i: number, d: -1 | 1) => {
    const j = i + d;
    if (j < 0 || j >= list.length) return;
    const next = [...list];
    [next[i], next[j]] = [next[j], next[i]];
    setData(next);
    await api("/api/presets/reorder", "POST", { ids: next.map((p) => p.id) }).catch((e) => log("Presets", (e as Error).message, false));
    reload();
  };
  const doConfirm = async () => {
    if (!confirm) return;
    const { p, what } = confirm;
    setConfirm(null);
    try {
      if (what === "store") {
        await api(`/api/presets/${p.id}/store`, "POST");
        log("Presets", `Saved the current shot to “${p.name}”`);
      } else {
        await api(`/api/presets/${p.id}`, "DELETE");
        log("Presets", `Deleted “${p.name}”`);
      }
    } catch (e) {
      log("Presets", (e as Error).message, false);
    }
    reload();
  };
  const saveName = async () => {
    const n = name.trim();
    if (!n) return;
    try {
      if (rename) {
        await api(`/api/presets/${rename.id}`, "PATCH", { name: n });
      } else {
        const p = await api<Preset>("/api/presets", "POST", { name: n });
        await api(`/api/presets/${p.id}/store`, "POST");
        log("Presets", `Added “${n}” at the current shot`);
      }
    } catch (e) {
      log("Presets", (e as Error).message, false);
    }
    setRename(null);
    setAdding(false);
    reload();
  };

  return (
    <Card>
      <CardHeader title="Presets" meta={`${list.length} saved`}>
        {editable && (
          <Button size="sm" variant={edit ? "accent" : "ghost"} onClick={() => setEdit(!edit)}>
            {edit ? <><Check size={14} /> Done</> : <><Pencil size={14} /> Edit</>}
          </Button>
        )}
      </CardHeader>
      <CardBody>
        {list.length === 0 ? <Empty>No presets yet.</Empty> : (
          <div className={cn("grid gap-2", compact ? "grid-cols-2 sm:grid-cols-4" : "grid-cols-2 sm:grid-cols-3 xl:grid-cols-4")}>
            {list.map((p, i) =>
              edit ? (
                <div key={p.id} className="flex flex-col gap-2 rounded-xl border border-line bg-panel-2 p-2.5">
                  <div className="flex items-center gap-1">
                    <span className="min-w-0 flex-1 truncate text-sm font-semibold">{p.name}</span>
                    <span className="font-mono text-[10px] text-dim">#{p.slot}</span>
                  </div>
                  <div className="grid grid-cols-5 gap-1">
                    <Button size="sm" variant="ghost" className="px-0" onClick={() => move(i, -1)} aria-label="Move earlier"><ChevronUp size={15} /></Button>
                    <Button size="sm" variant="ghost" className="px-0" onClick={() => move(i, 1)} aria-label="Move later"><ChevronDown size={15} /></Button>
                    <Button size="sm" variant="ghost" className="px-0" onClick={() => { setRename(p); setName(p.name); }} aria-label="Rename"><Pencil size={14} /></Button>
                    <Button size="sm" variant="ghost" className="px-0" onClick={() => setConfirm({ p, what: "store" })} aria-label="Save current shot"><Save size={14} /></Button>
                    <Button size="sm" variant="ghost" className="px-0 text-bad" onClick={() => setConfirm({ p, what: "delete" })} aria-label="Delete"><Trash2 size={14} /></Button>
                  </div>
                </div>
              ) : (
                <button
                  key={p.id} onClick={() => recall(p)}
                  className={cn(
                    "group flex flex-col items-start justify-center rounded-xl border px-3.5 text-left transition active:scale-[0.98]",
                    compact ? "h-14" : "h-[72px]",
                    active === p.id ? "border-accent bg-accent/12" : "border-line bg-panel-2 hover:border-line-strong",
                  )}
                >
                  <span className="w-full truncate text-[15px] font-semibold">{p.name}</span>
                  <span className="font-mono text-[10px] text-dim">SLOT {p.slot}</span>
                </button>
              ),
            )}
            {edit && (
              <button onClick={() => { setAdding(true); setName(""); }}
                className="flex h-[72px] items-center justify-center gap-2 rounded-xl border border-dashed border-line-strong text-sm text-muted hover:text-fg">
                <Plus size={16} /> Add at current shot
              </button>
            )}
          </div>
        )}
      </CardBody>

      <Dialog open={!!rename || adding} onOpenChange={(o) => { if (!o) { setRename(null); setAdding(false); } }}
        title={rename ? "Rename preset" : "New preset"}
        description={rename ? undefined : "Saves where the camera is pointing right now."}>
        <form onSubmit={(e) => { e.preventDefault(); saveName(); }} className="flex gap-2">
          <Input autoFocus value={name} maxLength={60} onChange={(e) => setName(e.target.value)} placeholder="Preset name" />
          <Button type="submit" variant="accent">Save</Button>
        </form>
      </Dialog>
      <Dialog open={!!confirm} onOpenChange={(o) => !o && setConfirm(null)}
        title={confirm?.what === "delete" ? `Delete “${confirm?.p.name}”?` : `Save the current shot to “${confirm?.p.name}”?`}
        description={confirm?.what === "delete" ? "It leaves the list. The camera's slot is not cleared." : "This replaces the position stored in this preset."}>
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={() => setConfirm(null)}>Cancel</Button>
          <Button variant={confirm?.what === "delete" ? "danger" : "accent"} onClick={doConfirm}>{confirm?.what === "delete" ? "Delete" : "Save shot"}</Button>
        </div>
      </Dialog>
    </Card>
  );
}
