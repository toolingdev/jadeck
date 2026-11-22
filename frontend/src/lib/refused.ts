import { useShared } from "@/lib/hooks";

/** A sender the stream gateway turned away (GET /api/feeds → status.lastRefused). */
export type Refused = {
  at: string;
  reason: "key" | "path" | "streamid";
  /** the path it asked for, when the gateway named it */
  path?: string;
  remote?: string;
  proto?: string;
};

const clock = (at: string) => new Date(at).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });

/** One plain line for a refused sender on feed `id`. */
export function refusedText(r: Refused, id: string): string {
  const who = r.remote ? `A sender (${r.remote})` : "A sender";
  const t = clock(r.at);
  switch (r.reason) {
    case "path":
      if (r.path === id) return `The gateway turned away a sender for this feed at ${t} (it hadn't loaded the feed yet). The controller has reloaded it; the sender gets in on its next try.`;
      return `${who} tried to send to “${r.path}”, which isn't a feed here, at ${t} — copy this feed's current Stream ID into it.`;
    case "streamid":
      return `${who} sent a Stream ID the gateway couldn't read at ${t} — copy the current Stream ID into it again.`;
    default:
      return `${who} tried to connect with the wrong key at ${t} — copy the current Stream ID into it.`;
  }
}

type FeedsList = { feeds: { id: string; source: string; status?: { online?: boolean; lastRefused?: Refused } | null }[] };

/** The latest refused sender for feed `id` while it has no picture (null otherwise). */
export function useFeedRefused(id: string | undefined): Refused | null {
  const { data } = useShared<FeedsList>(id ? "/api/feeds" : null, 5000);
  const f = data?.feeds?.find((x) => x.id === id);
  if (!f || f.source !== "push" || f.status?.online) return null;
  return f.status?.lastRefused ?? null;
}
