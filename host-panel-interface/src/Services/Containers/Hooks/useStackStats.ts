import { useCallback, useEffect, useRef, useState } from "react";
import { socket } from "../../../Utils/socket";
import type { ContainerStats } from "../Models/stats.model";

const MAX_HISTORY = 120; // ~4 min at 2s interval
/** A bit more than the API cooldown between two statsStart (3s). */
const STATS_RETRY_DELAY_MS = 3500;
const MAX_STATS_RETRIES = 3;

/** Subscriber currently owning the stats stream of the shared socket. */
let currentOwner: symbol | null = null;

export interface StatsSnapshot {
  current: ContainerStats[];
  history: Map<string, ContainerStats[]>;
}

/**
 * Hook that subscribes to live container stats via WebSocket.
 * Returns the latest snapshot and a rolling history per container.
 */
export default function useStackStats(containerIDs: string[], active: boolean) {
  const [snapshot, setSnapshot] = useState<StatsSnapshot>({
    current: [],
    history: new Map(),
  });
  const historyRef = useRef<Map<string, ContainerStats[]>>(new Map());

  const handleStatsData = useCallback((data: unknown) => {
    const stats = data as ContainerStats[];
    if (!Array.isArray(stats)) return;

    const history = historyRef.current;
    for (const s of stats) {
      const prev = history.get(s.id) ?? [];
      const arr = [...prev, s].slice(-MAX_HISTORY);
      history.set(s.id, arr);
    }

    setSnapshot({ current: stats, history: new Map(history) });
  }, []);

  const containerIDsKey = containerIDs.join(",");

  useEffect(() => {
    if (!active || containerIDs.length === 0) return;

    // Reset history on new subscription
    historyRef.current = new Map();
    setSnapshot({ current: [], history: new Map() });

    // The connection carries a single stats stream: the latest subscriber
    // owns it. Only the owner forwards data, retries and stops the stream.
    const me = Symbol("stats-subscriber");
    currentOwner = me;
    let retries = 0;
    let retryTimer: ReturnType<typeof setTimeout> | undefined;
    const start = () => socket.emit("statsStart", { ids: containerIDs });

    const handleData = (data: unknown) => {
      if (currentOwner === me) handleStatsData(data);
    };
    // The API refuses a statsStart less than 3s after the previous one on the
    // same connection (React StrictMode mounting the effect twice, a fast
    // navigation between pages): the owner retries once the cooldown passed.
    const handleStatsError = (message: unknown) => {
      if (
        currentOwner === me &&
        retries < MAX_STATS_RETRIES &&
        typeof message === "string" &&
        message.startsWith("Too many stats requests")
      ) {
        retries++;
        clearTimeout(retryTimer);
        retryTimer = setTimeout(() => {
          if (currentOwner === me) start();
        }, STATS_RETRY_DELAY_MS);
      }
    };

    socket.on("statsData", handleData);
    socket.on("statsError", handleStatsError);
    start();

    return () => {
      clearTimeout(retryTimer);
      // Only the owner stops the stream: another subscriber may own it now.
      if (currentOwner === me) {
        socket.emit("statsStop");
        currentOwner = null;
      }
      socket.off("statsData", handleData);
      socket.off("statsError", handleStatsError);
    };
  }, [active, containerIDsKey, handleStatsData]); // eslint-disable-line react-hooks/exhaustive-deps

  return snapshot;
}
