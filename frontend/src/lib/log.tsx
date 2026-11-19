import { createContext, useCallback, useContext, useState, type ReactNode } from "react";

// "What just happened": every button press lands here in plain words,
// plus a transient toast for errors.

export type LogEntry = { id: number; at: Date; source: string; text: string; ok: boolean };
type Ctx = { entries: LogEntry[]; log: (source: string, text: string, ok?: boolean) => void; toast: LogEntry | null };

const LogCtx = createContext<Ctx>({ entries: [], log: () => {}, toast: null });
let seq = 0;

export function LogProvider({ children }: { children: ReactNode }) {
  const [entries, setEntries] = useState<LogEntry[]>([]);
  const [toast, setToast] = useState<LogEntry | null>(null);
  const log = useCallback((source: string, text: string, ok = true) => {
    const e = { id: ++seq, at: new Date(), source, text, ok };
    setEntries((l) => [e, ...l].slice(0, 100));
    if (!ok) {
      setToast(e);
      window.setTimeout(() => setToast((t) => (t?.id === e.id ? null : t)), 5000);
    }
  }, []);
  return <LogCtx.Provider value={{ entries, log, toast }}>{children}</LogCtx.Provider>;
}

export const useLog = () => useContext(LogCtx);
