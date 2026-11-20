import { getScreenName } from "./stage";

// Browser errors go to the controller's log, so a page that breaks on the
// pulpit iPad shows up where someone helping remotely can read it. Same
// error at most once a minute; never blocks the page.

const recent = new Map<string, number>();

export function reportClientError(msg: string, stack = "", level: "error" | "warn" = "error") {
  const key = msg.slice(0, 200);
  const now = Date.now();
  if ((recent.get(key) ?? 0) > now - 60_000) return;
  recent.set(key, now);
  try {
    const body = JSON.stringify({ level, msg, stack, page: location.pathname + location.search, screen: getScreenName(), agent: navigator.userAgent });
    if (navigator.sendBeacon?.(new URL("/api/diag/client", location.href), new Blob([body], { type: "application/json" }))) return;
    fetch("/api/diag/client", { method: "POST", headers: { "Content-Type": "application/json" }, body, keepalive: true }).catch(() => {});
  } catch {
    /* never let reporting break the page */
  }
}

export function installErrorReporting() {
  window.addEventListener("error", (e) => {
    const err = e.error as Error | undefined;
    reportClientError(err?.message || e.message || "script error", err?.stack || `${e.filename}:${e.lineno}:${e.colno}`);
  });
  window.addEventListener("unhandledrejection", (e) => {
    const r = e.reason as Error | string | undefined;
    const msg = typeof r === "string" ? r : r?.message || "unhandled promise rejection";
    if (/the controller is not answering/.test(msg)) return; // offline is shown on screen; not a bug
    reportClientError(msg, typeof r === "object" ? r?.stack ?? "" : "");
  });
}
