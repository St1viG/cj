import { ConnectError } from "@connectrpc/connect";
import type { GetInstanceResponse, Instance, ListInstancesResponse } from "@gen/kondukter/api/v1/instance_pb";
import { InstancePhase } from "@gen/kondukter/api/v1/types_pb";
import { type QueryClient, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useApi } from "../api/ApiProvider";
import type { Clients } from "../api/clients";
import { isPermanent } from "../api/errors";
import { keys } from "../api/keys";

export type WatchStatus =
  /** No id to watch. */
  | "idle"
  | "connecting"
  | "live"
  /** The stream broke or the server restarted; trying again. */
  | "reconnecting"
  /** The instance reached a terminal phase and the server closed the stream. */
  | "ended"
  /** An error that retrying cannot fix (not found, no permission, signed out). */
  | "failed";

export interface WatchState {
  status: WatchStatus;
  /** The latest instance the stream delivered. */
  instance?: Instance;
  /** The last stream error while reconnecting or failed. */
  error?: ConnectError;
}

/** The phases after which the server closes the stream (`InstanceService.WatchInstance`). */
export function isTerminalPhase(phase: InstancePhase): boolean {
  return phase === InstancePhase.TERMINATED || phase === InstancePhase.FAILED || phase === InstancePhase.LOST;
}

/** Full-jitter exponential backoff: up to 0.5 s, 1 s, 2 s … capped at 15 s. */
export function defaultBackoff(attempt: number): number {
  return Math.random() * Math.min(15_000, 500 * 2 ** attempt);
}

function sleep(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    if (signal.aborted) return resolve();
    const done = () => {
      clearTimeout(timer);
      signal.removeEventListener("abort", done);
      resolve();
    };
    const timer = setTimeout(done, ms);
    signal.addEventListener("abort", done);
  });
}

export interface WatchLoop {
  client: Clients["instances"];
  id: string;
  signal: AbortSignal;
  onInstance: (instance: Instance) => void;
  onStatus: (status: WatchStatus, error?: ConnectError) => void;
  /** Milliseconds to wait before reconnect attempt `n` (0-based). */
  backoff?: (attempt: number) => number;
  /** Waits `ms` or until `signal` aborts; tests replace it. */
  wait?: (ms: number, signal: AbortSignal) => Promise<void>;
}

/**
 * Follows `WatchInstance` until the instance is terminal, the error is
 * permanent, or `signal` aborts. A broken stream, and one the server closes
 * while the instance is still live (it is shutting down), is reopened after a
 * jittered, growing delay that starts over whenever a message arrives.
 */
export async function runWatch(w: WatchLoop): Promise<void> {
  const backoff = w.backoff ?? defaultBackoff;
  const wait = w.wait ?? sleep;
  let attempt = 0;
  let last: Instance | undefined;
  w.onStatus("connecting");

  while (!w.signal.aborted) {
    let failure: ConnectError | undefined;
    try {
      for await (const msg of w.client.watchInstance({ id: w.id }, { signal: w.signal })) {
        if (w.signal.aborted) return;
        if (!msg.instance) continue;
        attempt = 0;
        last = msg.instance;
        w.onStatus("live");
        w.onInstance(msg.instance);
      }
      if (w.signal.aborted) return;
      if (last && isTerminalPhase(last.phase)) {
        w.onStatus("ended");
        return;
      }
    } catch (e) {
      if (w.signal.aborted) return;
      failure = ConnectError.from(e);
      if (isPermanent(failure)) {
        w.onStatus("failed", failure);
        return;
      }
    }
    w.onStatus("reconnecting", failure);
    await wait(backoff(attempt++), w.signal);
  }
}

/**
 * Puts a streamed instance into the cache, so the detail page and every list
 * show the new phase without a refetch. The timeline (events) only changes
 * with the phase, so a phase change refetches the detail.
 */
export function applyInstance(qc: QueryClient, instance: Instance): void {
  const detailKey = keys.instances.detail(instance.id);
  const previous = qc.getQueryData<GetInstanceResponse>(detailKey);
  if (previous) {
    qc.setQueryData<GetInstanceResponse>(detailKey, { ...previous, instance });
    if (previous.instance?.phase !== instance.phase) void qc.invalidateQueries({ queryKey: detailKey });
  }
  qc.setQueriesData<ListInstancesResponse>({ queryKey: keys.instances.lists }, (old) =>
    old && { ...old, instances: old.instances.map((i) => (i.id === instance.id ? instance : i)) },
  );
}

/**
 * Watches one instance for as long as the component is mounted: every
 * streamed update lands in the query cache (see `applyInstance`) and is also
 * returned, with the connection status, for pages that show the live phase.
 */
export function useWatchInstance(id: string | undefined): WatchState {
  const { instances } = useApi();
  const qc = useQueryClient();
  const [state, setState] = useState<WatchState>({ status: "idle" });

  useEffect(() => {
    if (!id) {
      setState({ status: "idle" });
      return;
    }
    const ctrl = new AbortController();
    setState({ status: "connecting" });
    void runWatch({
      client: instances,
      id,
      signal: ctrl.signal,
      onInstance: (instance) => {
        applyInstance(qc, instance);
        setState((s) => ({ ...s, instance }));
      },
      onStatus: (status, error) => setState((s) => ({ ...s, status, error })),
    });
    return () => ctrl.abort();
  }, [id, instances, qc]);

  return state;
}
