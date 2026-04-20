import { useEffect, useState } from "react";
import { getStatus } from "../api";
import type { StatusResponse } from "../types";

export type HealthState = "ok" | "degraded" | "down" | "unknown";

export interface UseStatusResult {
  state: HealthState;
  detail: StatusResponse | null;
  /** True the very first time we get a response (success or failure). */
  loaded: boolean;
}

// Polls /api/v1/status at the given interval. A fetch failure maps to
// "down" — we treat it the same as the server reporting itself down so
// the UI always has a concrete indicator.
export function useStatus(intervalMs = 15000): UseStatusResult {
  const [state, setState] = useState<HealthState>("unknown");
  const [detail, setDetail] = useState<StatusResponse | null>(null);
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    let cancelled = false;

    async function tick() {
      try {
        const s = await getStatus();
        if (cancelled) return;
        setDetail(s);
        setState(s.status);
      } catch {
        if (cancelled) return;
        setDetail(null);
        setState("down");
      } finally {
        if (!cancelled) setLoaded(true);
      }
    }

    tick();
    const id = setInterval(tick, intervalMs);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [intervalMs]);

  return { state, detail, loaded };
}
