import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import {
  type GetInstanceResponse,
  GetInstanceResponseSchema,
  type Instance,
  InstanceSchema,
  InstanceService,
  type ListInstancesResponse,
  ListInstancesResponseSchema,
} from "@gen/kondukter/api/v1/instance_pb";
import { InstancePhase } from "@gen/kondukter/api/v1/types_pb";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it } from "vitest";
import { ApiProvider } from "../api/ApiProvider";
import { createClients } from "../api/clients";
import { keys } from "../api/keys";
import { applyInstance, runWatch, useWatchInstance, type WatchStatus } from "./useWatchInstance";

function inst(id: string, phase: InstancePhase): Instance {
  return create(InstanceSchema, { id, phase });
}

type Handler = (call: number, signal: AbortSignal) => AsyncIterable<{ instance: Instance }>;

/** An in-memory InstanceService whose WatchInstance is `handler`; `calls` counts the streams opened. */
function fakeWatchServer(handler: Handler) {
  const state = { calls: 0 };
  const transport = createRouterTransport(({ service }) => {
    service(InstanceService, {
      watchInstance: (_req, ctx) => handler(++state.calls, ctx.signal),
    });
  });
  return { transport, client: createClients(transport).instances, state };
}

function recorder() {
  const statuses: WatchStatus[] = [];
  const errors: Array<ConnectError | undefined> = [];
  const phases: InstancePhase[] = [];
  return {
    statuses,
    errors,
    phases,
    onStatus: (s: WatchStatus, e?: ConnectError) => {
      statuses.push(s);
      errors.push(e);
    },
    onInstance: (i: Instance) => phases.push(i.phase),
  };
}

describe("runWatch", () => {
  it("delivers each update and ends when the instance is terminal", async () => {
    const { client, state } = fakeWatchServer(async function* () {
      yield { instance: inst("i1", InstancePhase.PROVISIONING) };
      yield { instance: inst("i1", InstancePhase.RUNNING) };
      yield { instance: inst("i1", InstancePhase.TERMINATED) };
    });
    const r = recorder();
    await runWatch({ client, id: "i1", signal: new AbortController().signal, ...r });

    expect(r.phases).toEqual([InstancePhase.PROVISIONING, InstancePhase.RUNNING, InstancePhase.TERMINATED]);
    expect(r.statuses.at(-1)).toBe("ended");
    expect(state.calls).toBe(1);
  });

  it("reopens a broken stream, and one closed while the instance lives, with a delay that restarts after a message", async () => {
    const { client, state } = fakeWatchServer(async function* (call) {
      if (call === 1) throw new ConnectError("restarting", Code.Unavailable);
      if (call === 2) {
        yield { instance: inst("i1", InstancePhase.RUNNING) };
        return; // the server shut down: not terminal, so watch again
      }
      yield { instance: inst("i1", InstancePhase.TERMINATED) };
    });
    const r = recorder();
    const delays: number[] = [];
    await runWatch({
      client,
      id: "i1",
      signal: new AbortController().signal,
      backoff: (attempt) => 100 + attempt,
      wait: async (ms) => {
        delays.push(ms);
      },
      ...r,
    });

    expect(r.statuses).toEqual(["connecting", "reconnecting", "live", "reconnecting", "live", "ended"]);
    expect(r.errors[1]?.code).toBe(Code.Unavailable);
    // Attempt 0 both times: the message on the second stream reset the count.
    expect(delays).toEqual([100, 100]);
    expect(state.calls).toBe(3);
  });

  it("grows the delay while the server stays down", async () => {
    const { client } = fakeWatchServer(async function* (call) {
      if (call <= 3) throw new ConnectError("down", Code.Unavailable);
      yield { instance: inst("i1", InstancePhase.FAILED) };
    });
    const delays: number[] = [];
    await runWatch({
      client,
      id: "i1",
      signal: new AbortController().signal,
      backoff: (attempt) => 2 ** attempt,
      wait: async (ms) => {
        delays.push(ms);
      },
      ...recorder(),
    });
    expect(delays).toEqual([1, 2, 4]);
  });

  it.each([Code.NotFound, Code.PermissionDenied, Code.Unauthenticated])("stops on %s without retrying", async (code) => {
    const { client, state } = fakeWatchServer(async function* () {
      yield* [];
      throw new ConnectError("no", code);
    });
    const r = recorder();
    await runWatch({ client, id: "i1", signal: new AbortController().signal, ...r });

    expect(r.statuses).toEqual(["connecting", "failed"]);
    expect(r.errors[1]?.code).toBe(code);
    expect(state.calls).toBe(1);
  });

  it("stops quietly when aborted", async () => {
    const { client } = fakeWatchServer(async function* (_call, signal) {
      yield { instance: inst("i1", InstancePhase.RUNNING) };
      await new Promise<void>((resolve) => signal.addEventListener("abort", () => resolve()));
    });
    const ctrl = new AbortController();
    const r = recorder();
    const done = runWatch({ client, id: "i1", signal: ctrl.signal, ...r });

    await waitFor(() => expect(r.phases).toEqual([InstancePhase.RUNNING]));
    ctrl.abort();
    await done;
    // No "reconnecting" or "failed" after the caller walked away.
    expect(r.statuses).toEqual(["connecting", "live"]);
  });

  it("does not wait out a delay once aborted", async () => {
    const { client } = fakeWatchServer(async function* () {
      yield* [];
      throw new ConnectError("down", Code.Unavailable);
    });
    const ctrl = new AbortController();
    const r = recorder();
    // Default wait, with a backoff far longer than the test.
    const done = runWatch({ client, id: "i1", signal: ctrl.signal, backoff: () => 60_000, ...r });
    await waitFor(() => expect(r.statuses).toContain("reconnecting"));
    ctrl.abort();
    await done;
  });
});

describe("applyInstance", () => {
  function seeded() {
    const qc = new QueryClient();
    qc.setQueryData<GetInstanceResponse>(
      keys.instances.detail("i1"),
      create(GetInstanceResponseSchema, { instance: inst("i1", InstancePhase.PENDING) }),
    );
    qc.setQueryData<ListInstancesResponse>(
      keys.instances.list({ includeTerminal: false }),
      create(ListInstancesResponseSchema, {
        instances: [inst("i0", InstancePhase.RUNNING), inst("i1", InstancePhase.PENDING)],
      }),
    );
    return qc;
  }

  it("replaces the instance in the detail and in every list, and refetches the timeline on a phase change", () => {
    const qc = seeded();
    applyInstance(qc, inst("i1", InstancePhase.RUNNING));

    expect(qc.getQueryData<GetInstanceResponse>(keys.instances.detail("i1"))?.instance?.phase).toBe(InstancePhase.RUNNING);
    const list = qc.getQueryData<ListInstancesResponse>(keys.instances.list({ includeTerminal: false }));
    expect(list?.instances.map((i) => [i.id, i.phase])).toEqual([
      ["i0", InstancePhase.RUNNING],
      ["i1", InstancePhase.RUNNING],
    ]);
    expect(qc.getQueryState(keys.instances.detail("i1"))?.isInvalidated).toBe(true);
  });

  it("keeps the timeline when only other fields changed", () => {
    const qc = seeded();
    applyInstance(qc, inst("i1", InstancePhase.PENDING));
    expect(qc.getQueryState(keys.instances.detail("i1"))?.isInvalidated).toBe(false);
  });

  it("leaves a cache without that instance alone", () => {
    const qc = new QueryClient();
    applyInstance(qc, inst("i9", InstancePhase.RUNNING));
    expect(qc.getQueryCache().getAll()).toHaveLength(0);
  });
});

describe("useWatchInstance", () => {
  function wrapperFor(transport: ReturnType<typeof createRouterTransport>, qc: QueryClient) {
    return ({ children }: { children: ReactNode }) => (
      <ApiProvider transport={transport}>
        <QueryClientProvider client={qc}>{children}</QueryClientProvider>
      </ApiProvider>
    );
  }

  it("follows the instance, feeds the cache, and reports the end", async () => {
    let release!: () => void;
    const gate = new Promise<void>((r) => {
      release = r;
    });
    const { transport } = fakeWatchServer(async function* () {
      yield { instance: inst("i1", InstancePhase.PROVISIONING) };
      await gate;
      yield { instance: inst("i1", InstancePhase.TERMINATED) };
    });
    const qc = new QueryClient();
    qc.setQueryData(keys.instances.detail("i1"), create(GetInstanceResponseSchema, { instance: inst("i1", InstancePhase.PENDING) }));

    const { result } = renderHook(() => useWatchInstance("i1"), { wrapper: wrapperFor(transport, qc) });
    expect(result.current.status).toBe("connecting");

    await waitFor(() => expect(result.current.status).toBe("live"));
    expect(result.current.instance?.phase).toBe(InstancePhase.PROVISIONING);
    expect(qc.getQueryData<GetInstanceResponse>(keys.instances.detail("i1"))?.instance?.phase).toBe(InstancePhase.PROVISIONING);

    release();
    await waitFor(() => expect(result.current.status).toBe("ended"));
    expect(result.current.instance?.phase).toBe(InstancePhase.TERMINATED);
  });

  it("reports a failure it cannot fix", async () => {
    const { transport } = fakeWatchServer(async function* () {
      yield* [];
      throw new ConnectError("instance not found", Code.NotFound);
    });
    const { result } = renderHook(() => useWatchInstance("missing"), { wrapper: wrapperFor(transport, new QueryClient()) });
    await waitFor(() => expect(result.current.status).toBe("failed"));
    expect(result.current.error?.rawMessage).toBe("instance not found");
  });

  it("does nothing without an id", () => {
    const { transport, state } = fakeWatchServer(async function* () {
      yield* [];
    });
    const { result } = renderHook(() => useWatchInstance(undefined), { wrapper: wrapperFor(transport, new QueryClient()) });
    expect(result.current.status).toBe("idle");
    expect(state.calls).toBe(0);
  });

  it("closes the stream on unmount", async () => {
    let aborted = false;
    const { transport } = fakeWatchServer(async function* (_call, signal) {
      yield { instance: inst("i1", InstancePhase.RUNNING) };
      await new Promise<void>((resolve) =>
        signal.addEventListener("abort", () => {
          aborted = true;
          resolve();
        }),
      );
    });
    const { result, unmount } = renderHook(() => useWatchInstance("i1"), { wrapper: wrapperFor(transport, new QueryClient()) });
    await waitFor(() => expect(result.current.status).toBe("live"));
    unmount();
    await waitFor(() => expect(aborted).toBe(true));
  });
});
