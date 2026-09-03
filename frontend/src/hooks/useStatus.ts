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
    // PRD-68 P5: a background tab keeps its interval but skips the request,
    // so N idle tabs don't each ping the DB every 15s. A fresh tick fires
    // when the tab becomes visible again.
    const id = setInterval(() => {
      if (typeof document !== "undefined" && document.hidden) return;
      tick();
    }, intervalMs);
    const onVisible = () => {
      if (!document.hidden) tick();
    };
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      cancelled = true;
      clearInterval(id);
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, [intervalMs]);

  return { state, detail, loaded };
}
