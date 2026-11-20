import { useEffect, useState } from "react";
import { Star, Pencil, Copy, Trash2 } from "lucide-react";
import { api } from "@/lib/api";
import { useLog } from "@/lib/log";
import { Button, Card, CardBody, CardHeader, Dialog, Select } from "@/components/ui";
import { SCREENS, SCREEN_LABEL, screenFor, type Dashboard, type Defaults, type Screen } from "@/dash/types";
import { KindBadge, KIND_ICON, NewDashboard } from "@/pages/DashboardPage";

/** Settings → Dashboards: a default for each kind of screen, and the list. */
export function DashboardSettings() {
  const [list, setList] = useState<Dashboard[] | null>(null);
  const [defaults, setDefaults] = useState<Defaults>({});
  const [copyOf, setCopyOf] = useState<number | null>(null);
  const [newOpen, setNewOpen] = useState(false);
  const [del, setDel] = useState<Dashboard | null>(null);
  const { log } = useLog();
  const load = async () => {
    try {
      const [l, d] = await Promise.all([api<Dashboard[]>("/api/dashboards"), api<Defaults>("/api/dashboards/defaults")]);
      setList(l);
      setDefaults(d);
    } catch (e) {
      log("Dashboards", (e as Error).message, false);
    }
  };
  useEffect(() => {
    load();
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  const setDefault = async (s: Screen, id: number) => {
    try {
      setDefaults(await api<Defaults>("/api/dashboards/defaults", "PUT", { [s]: id }));
      log("Dashboards", `Default for ${SCREEN_LABEL[s].toLowerCase()}s set`);
    } catch (e) {
      log("Dashboards", (e as Error).message, false);
    }
  };
  const edit = (d: Dashboard) => {
    try {
      localStorage.setItem("ptz.dashboard", JSON.stringify(d.id));
      localStorage.setItem("ptz.dashboard.editNow", String(d.id));
    } catch { /* ignore */ }
    history.pushState(null, "", "/");
    dispatchEvent(new PopStateEvent("popstate"));
  };
  const remove = async () => {
    if (!del) return;
    try {
      await api(`/api/dashboards/${del.id}`, "DELETE");
      log("Dashboards", `Deleted “${del.name}”`);
    } catch (e) {
      log("Dashboards", (e as Error).message, false);
    }
    setDel(null);
    load();
  };

  return (
    <Card>
      <CardHeader title="Dashboards" meta="one per kind of screen, any device can switch">
        <Button size="sm" onClick={() => { setCopyOf(null); setNewOpen(true); }}>New dashboard</Button>
      </CardHeader>
      <CardBody className="space-y-4">
        <div>
          <p className="mb-2 text-xs text-muted">Shown first on a device that hasn't picked one yet. Everyone can switch any time, and each device remembers its pick.</p>
          <div className="grid gap-3 sm:grid-cols-3">
            {SCREENS.map((s) => {
              const Icon = KIND_ICON[s];
              return (
                <label key={s} className="block">
                  <span className="mb-1 flex items-center gap-1.5 text-xs text-muted"><Icon size={13} /> {SCREEN_LABEL[s]}s</span>
                  <Select value={String(defaults[s] ?? "")} onChange={(v) => setDefault(s, Number(v))}
                    options={(list ?? []).map((d) => ({ value: String(d.id), label: `★ ${d.name}${d.kind !== s ? ` (${SCREEN_LABEL[d.kind].toLowerCase()})` : ""}` }))} />
                </label>
              );
            })}
          </div>
          <p className="mt-1 text-[11px] text-dim">Any dashboard can be the default for any kind — e.g. one dashboard for tablets and desktops.</p>
        </div>
        <div className="space-y-2">
          {(list ?? []).map((d) => {
            const defFor = SCREENS.filter((s) => defaults[s] === d.id);
            return (
              <div key={d.id} className="flex flex-wrap items-center gap-3 rounded-xl border border-line bg-panel-2 px-3 py-2.5">
                <span className="min-w-0 flex-1">
                  <span className="flex items-center gap-2 text-sm font-semibold">{d.name} <KindBadge kind={d.kind} /></span>
                  <span className="block text-[11px] text-dim">
                    {defFor.length > 0 && <span className="text-accent"><Star size={10} className="mr-0.5 inline fill-accent" />default for {defFor.map((s) => SCREEN_LABEL[s].toLowerCase() + "s").join(", ")} · </span>}
                    {d.widgets.length} widgets
                  </span>
                </span>
                <Button size="sm" variant="ghost" onClick={() => edit(d)}><Pencil size={13} /> Edit</Button>
                <Button size="sm" variant="ghost" onClick={() => { setCopyOf(d.id); setNewOpen(true); }}><Copy size={13} /> Duplicate</Button>
                <Button size="sm" variant="ghost" className="text-bad" onClick={() => setDel(d)} disabled={defFor.length > 0 || (list?.length ?? 0) <= 1}
                  title={defFor.length ? "It's a default — pick another default first" : undefined}><Trash2 size={13} /> Delete</Button>
              </div>
            );
          })}
        </div>
        <p className="text-[11px] text-dim">A default can't be deleted until another dashboard takes its place.</p>
      </CardBody>
      <NewDashboard open={newOpen} onClose={() => setNewOpen(false)} list={list ?? []} device={screenFor(innerWidth)} copyOf={copyOf}
        onCreated={(d) => { setNewOpen(false); load(); log("Dashboards", `Created “${d.name}”`); }} />
      <Dialog open={!!del} onOpenChange={(o) => !o && setDel(null)} title={`Delete “${del?.name}”?`} description="Everyone loses this dashboard. This can't be undone.">
        <div className="flex justify-end gap-2"><Button variant="ghost" onClick={() => setDel(null)}>Cancel</Button><Button variant="danger" onClick={remove}>Delete</Button></div>
      </Dialog>
    </Card>
  );
}
