import { useEffect, useMemo, useState } from "react";
import { Pipette } from "lucide-react";
import { api, type FieldSpec } from "@/lib/api";
import { useLog } from "@/lib/log";
import { Button, Card, CardBody, CardHeader, Segmented, Select, Slider, Switch, Empty } from "@/components/ui";

const groups = [
  { id: "image", label: "Image" },
  { id: "wb", label: "White balance" },
  { id: "exposure", label: "Exposure" },
  { id: "focus", label: "Focus" },
  { id: "nr", label: "Noise" },
];

/** Advanced picture settings, rendered from the server's own field list.
 * Only fields the camera actually reports are shown — never invented. */
export function ImageSettings({ onWbPick }: { onWbPick?: () => void }) {
  const [specs, setSpecs] = useState<FieldSpec[]>([]);
  const [vals, setVals] = useState<Record<string, number | string | boolean> | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [group, setGroup] = useState("image");
  const { log } = useLog();

  const load = async () => {
    try {
      const [s, v] = await Promise.all([
        api<FieldSpec[]>("/api/camera/image/fields"),
        api<Record<string, number | string | boolean>>("/api/camera/image"),
      ]);
      setSpecs(s);
      setVals(v);
      setErr(null);
    } catch (e) {
      setErr((e as Error).message);
    }
  };
  useEffect(() => {
    load();
  }, []);

  const present = useMemo(() => specs.filter((s) => vals && s.name in vals), [specs, vals]);
  const shownGroups = groups.filter((g) => g.id === "wb" || present.some((s) => s.group === g.id));
  const apply = async (s: FieldSpec, v: number | string | boolean) => {
    setVals((o) => ({ ...(o ?? {}), [s.name]: v }));
    try {
      const back = await api<Record<string, number | string | boolean>>("/api/camera/image", "POST", { [s.name]: v });
      setVals(back);
      log("Picture", `${s.label} → ${String(v)}`);
    } catch (e) {
      log("Picture", `${s.label} not changed — ${(e as Error).message}`, false);
      load();
    }
  };

  return (
    <Card>
      <CardHeader title="Picture" meta="advanced">
        <Button size="sm" variant="ghost" onClick={load}>Refresh</Button>
      </CardHeader>
      <CardBody className="space-y-4">
        {err ? <Empty>Camera picture settings unavailable — {err}</Empty> : !vals ? <Empty>Reading camera…</Empty> : (
          <>
            <Segmented value={group} onChange={setGroup} options={shownGroups.map((g) => ({ value: g.id, label: g.label }))} className="max-w-full overflow-x-auto" />
            {group === "wb" && (
              <div className="rounded-xl border border-line bg-panel-2 p-3">
                <div className="flex items-center gap-3">
                  <div className="flex-1 text-sm">
                    <div className="font-semibold">Pick a white point</div>
                    <div className="text-xs text-muted">Tap something white in the picture. The camera aims there, balances, and comes straight back.</div>
                  </div>
                  <Button variant="accent" onClick={onWbPick}><Pipette size={16} /> Pick</Button>
                </div>
              </div>
            )}
            <div className="grid grid-cols-[repeat(auto-fill,minmax(220px,1fr))] gap-x-6 gap-y-4">
              {present.filter((s) => s.group === group).map((s) => (
                <Field key={s.name} spec={s} value={vals[s.name]} onCommit={(v) => apply(s, v)} />
              ))}
            </div>
            {group === "wb" && !present.some((s) => s.group === "wb") && (
              <p className="text-xs text-dim">This camera doesn't report white-balance settings over its config API; the white-point pick works over VISCA.</p>
            )}
          </>
        )}
      </CardBody>
    </Card>
  );
}

function Field({ spec, value, onCommit }: { spec: FieldSpec; value: number | string | boolean; onCommit: (v: number | string | boolean) => void }) {
  const [local, setLocal] = useState(value);
  useEffect(() => setLocal(value), [value]);
  if (spec.kind === "bool") {
    return (
      <label className="flex items-center justify-between gap-3 text-sm">
        <span className="text-muted">{spec.label}</span>
        <Switch checked={!!local} onChange={(v) => onCommit(v)} label={spec.label} />
      </label>
    );
  }
  if (spec.kind === "enum") {
    return (
      <label className="block text-sm">
        <span className="mb-1 block text-muted">{spec.label}</span>
        <Select value={String(local)} onChange={(v) => onCommit(v)} options={(spec.values ?? []).map((v) => ({ value: v, label: v }))} />
      </label>
    );
  }
  return (
    <div className="text-sm">
      <div className="mb-0.5 flex justify-between"><span className="text-muted">{spec.label}</span><span className="font-mono tabular">{String(local)}</span></div>
      <Slider value={Number(local)} min={spec.min ?? 0} max={spec.max ?? 100} onChange={(v) => setLocal(v)} onCommit={(v) => onCommit(v)} label={spec.label} />
    </div>
  );
}
