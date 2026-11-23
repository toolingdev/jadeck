import { useEffect, useState } from "react";
import { api } from "@/lib/api";
import { setScreenName } from "@/lib/stage";

/** Who this browser is over remote access (Cloudflare): null on the local
 *  network (or until known). A person limited to their dashboards gets
 *  only those, locked, under their own name. */
export type RemoteMe = { remote: true; email: string; name: string; everything: boolean; dashboards: number[] };

let me: RemoteMe | null | undefined;
const subs = new Set<(m: RemoteMe | null) => void>();
let started = false;

function load() {
  if (started) return;
  started = true;
  api<{ remote: boolean } & Partial<RemoteMe>>("/api/remote/me").then((r) => {
    me = r.remote ? (r as RemoteMe) : null;
    if (me && !me.everything && me.name) setScreenName(me.name); // messages say who's writing
    subs.forEach((f) => f(me ?? null));
  }).catch(() => { me = null; subs.forEach((f) => f(null)); });
}

/** undefined while asking; null on the local network. */
export function useRemote(): RemoteMe | null | undefined {
  const [m, setM] = useState(me);
  useEffect(() => {
    subs.add(setM);
    load();
    return () => { subs.delete(setM); };
  }, []);
  return m;
}

/** Limited to their own screens (not "everything"). */
export function useRemoteLimited() {
  const m = useRemote();
  return !!m && !m.everything;
}
