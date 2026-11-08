import { timestampMs } from "@bufbuild/protobuf/wkt";
import { Code, ConnectError, createRouterTransport, type Transport } from "@connectrpc/connect";
import { AdminService } from "@gen/kondukter/api/v1/admin_pb";
import { AuthService } from "@gen/kondukter/api/v1/auth_pb";
import type { Instance } from "@gen/kondukter/api/v1/instance_pb";
import type { Node, NodeStatus } from "@gen/kondukter/api/v1/types_pb";
import { isTerminalPhase } from "../hooks/useWatchInstance";
import { principalOf } from "./fakeServer";

type Patch<T> = Partial<Omit<T, "$typeName" | "$unknown">>;

const createdMs = (i: Instance) => (i.createdAt ? timestampMs(i.createdAt) : 0);

export interface FakeStageOptions {
  nodes?: Node[];
  instances?: Instance[];
  /** More unfinished instances than one page holds. */
  moreInstances?: boolean;
}

/**
 * The operator's view of the fleet for the stage page: AdminService
 * ListNodes and ListInstances over nodes and instances a test changes between
 * polls, as a drill would. Signed in as the demo operator. `calls` records
 * the requests; `fail` makes a method fail until the test clears it.
 */
export function fakeStage(opts: FakeStageOptions = {}) {
  const nodes = new Map<string, Node>((opts.nodes ?? []).map((n) => [n.id, n]));
  const instances = new Map<string, Instance>((opts.instances ?? []).map((i) => [i.id, i]));
  const calls = {
    listNodes: [] as NodeStatus[][],
    listInstances: [] as Array<{ includeTerminal: boolean; pageSize: number }>,
  };
  const fail: Partial<Record<keyof typeof calls, ConnectError>> = {};

  const transport: Transport = createRouterTransport(({ service }) => {
    service(AuthService, {
      getMe: () => ({ principal: principalOf("operator") }),
      logout: () => ({}),
    });
    service(AdminService, {
      listNodes: (req) => {
        calls.listNodes.push([...req.statuses]);
        if (fail.listNodes) throw fail.listNodes;
        return { nodes: [...nodes.values()].filter((n) => req.statuses.length === 0 || req.statuses.includes(n.status)) };
      },
      listInstances: (req) => {
        calls.listInstances.push({ includeTerminal: req.includeTerminal, pageSize: req.pageSize });
        if (fail.listInstances) throw fail.listInstances;
        // The server's order: newest first.
        const rows = [...instances.values()]
          .filter((i) => req.includeTerminal || !isTerminalPhase(i.phase))
          .sort((a, b) => createdMs(b) - createdMs(a));
        return { instances: rows, nextPageToken: opts.moreInstances ? "more" : "" };
      },
    });
  });

  function must<T>(map: Map<string, T>, id: string, what: string): T {
    const v = map.get(id);
    if (!v) throw new ConnectError(`${what} ${id} not found`, Code.NotFound);
    return v;
  }

  return {
    transport,
    calls,
    fail,
    /** The node as the server now has it, as after a heartbeat is lost or an operator acts. */
    setNode: (id: string, patch: Patch<Node>) => nodes.set(id, { ...must(nodes, id, "node"), ...patch }),
    setInstance: (id: string, patch: Patch<Instance>) => instances.set(id, { ...must(instances, id, "instance"), ...patch }),
    addInstance: (i: Instance) => instances.set(i.id, i),
    removeInstance: (id: string) => instances.delete(id),
  };
}

export type FakeStage = ReturnType<typeof fakeStage>;
