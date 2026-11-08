import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { timestampDate, timestampFromDate } from "@bufbuild/protobuf/wkt";
import { Code, ConnectError, createRouterTransport, type Transport } from "@connectrpc/connect";
import { AuthService } from "@gen/kondukter/api/v1/auth_pb";
import {
  type GetUtilizationRequest,
  NodeStatusCountSchema,
  NodeUtilizationSchema,
  PartnerService,
  type Site,
  SiteSchema,
} from "@gen/kondukter/api/v1/partner_pb";
import {
  AcceleratorHealth,
  NodeAcceleratorSchema,
  NodeCapacitySchema,
  type Node,
  NodeSchema,
  NodeStatus,
  type ResourceAmounts,
  ResourceAmountsSchema,
} from "@gen/kondukter/api/v1/types_pb";
import { type AccountName, accounts, principalOf } from "./fakeServer";

const at = (iso: string) => timestampFromDate(new Date(iso));
const amounts = (vcpu: number, ramMib: number, diskGib: number) =>
  create(ResourceAmountsSchema, { vcpu, ramMib: BigInt(ramMib), diskGib: BigInt(diskGib) });
const minus = (a: ResourceAmounts, b: ResourceAmounts) =>
  amounts(a.vcpu - b.vcpu, Number(a.ramMib - b.ramMib), Number(a.diskGib - b.diskGib));

export function site(id: string, name: string, init: MessageInitShape<typeof SiteSchema> = {}): Site {
  return create(SiteSchema, {
    id,
    partnerId: "partner-demo-dc",
    name,
    region: name,
    countryCode: "NL",
    networkMode: "port-map",
    createdAt: at("2026-11-01T10:00:00Z"),
    ...init,
  });
}

/**
 * A node shaped like the demo's `kdk-sim` nodes: 8 vCPU, 16 GiB, 200 GiB, of
 * which `allocated` (vCPU) is held, with 2 GiB RAM and 20 GiB disk per vCPU
 * pair as the demo's cpu-2x2 shape does.
 */
export function node(id: string, hostname: string, init: MessageInitShape<typeof NodeSchema> & { allocatedVcpu?: number } = {}): Node {
  const { allocatedVcpu = 0, ...rest } = init;
  const total = amounts(8, 16384, 200);
  const reserved = amounts(0, 0, 0);
  const allocated = amounts(allocatedVcpu, allocatedVcpu * 1024, allocatedVcpu * 10);
  return create(NodeSchema, {
    id,
    siteId: "site-demo",
    siteName: "demo",
    partnerId: "partner-demo-dc",
    partnerName: "demo-dc",
    hostname,
    status: NodeStatus.READY,
    arch: "amd64",
    os: "linux",
    driver: "mock",
    agentVersion: "0.1.0",
    capacity: create(NodeCapacitySchema, { total, reserved, allocated, free: minus(minus(total, reserved), allocated) }),
    activeInstanceCount: allocatedVcpu / 2,
    lastSeenAt: timestampFromDate(new Date(Date.now() - 3000)),
    desiredGeneration: 4n,
    observedGeneration: 4n,
    createdAt: at("2026-11-01T10:00:00Z"),
    ...rest,
  });
}

export function gpu(model = "GeForce GTX 1060 6GB", allocated = false) {
  return create(NodeAcceleratorSchema, { id: "gpu0", kind: "nvidia", model, memMib: 6144n, health: AcceleratorHealth.OK, allocated });
}

export interface FakeFleetOptions {
  /** Who is signed in: the demo partner (default) or operator. */
  as?: Extract<AccountName, "partner" | "operator">;
  sites?: Site[];
  nodes?: Node[];
}

/**
 * An in-memory fleet for the partner and operator pages: sites and nodes with
 * the server's drain rules, enrollment tokens and utilization. Signed in as
 * the demo partner unless `as` says otherwise. `calls` records the requests
 * the page made; `fail` makes a method fail until the test clears it.
 */
export function fakeFleet(opts: FakeFleetOptions = {}) {
  const as = opts.as ?? "partner";
  const email = accounts[as].email;
  const sites = opts.sites ?? [site("site-demo", "demo")];
  const nodes = new Map<string, Node>((opts.nodes ?? []).map((n) => [n.id, n]));
  let tokens = 0;

  const calls = {
    listSites: 0,
    listNodes: [] as string[],
    createEnrollmentToken: [] as Array<{ siteId: string; idempotencyKey: string }>,
    drainNode: [] as Array<{ nodeId: string; reason: string; idempotencyKey: string }>,
    undrainNode: [] as string[],
    getUtilization: [] as GetUtilizationRequest[],
  };
  const fail: Partial<Record<keyof typeof calls, ConnectError>> = {};
  function maybeFail(method: keyof typeof calls) {
    const err = fail[method];
    if (err) throw err;
  }

  function getNode(id: string): Node {
    const n = nodes.get(id);
    if (!n) throw new ConnectError("node not found", Code.NotFound);
    return n;
  }
  function setNode(id: string, patch: Partial<Omit<Node, "$typeName" | "$unknown">>): Node {
    const next: Node = { ...getNode(id), ...patch };
    nodes.set(id, next);
    return next;
  }

  const transport: Transport = createRouterTransport(({ service }) => {
    service(AuthService, {
      getMe: () => ({ principal: principalOf(as) }),
      login: () => ({ principal: principalOf(as) }),
      logout: () => ({}),
    });

    service(PartnerService, {
      listSites: () => {
        calls.listSites++;
        maybeFail("listSites");
        return {
          sites: sites.map((s) => {
            const mine = [...nodes.values()].filter((n) => n.siteId === s.id);
            const statuses = [...new Set(mine.map((n) => n.status))].sort();
            return {
              ...s,
              nodeCount: mine.length,
              nodesByStatus: statuses.map((status) =>
                create(NodeStatusCountSchema, { status, count: mine.filter((n) => n.status === status).length }),
              ),
            };
          }),
        };
      },
      listNodes: (req) => {
        calls.listNodes.push(req.siteId);
        maybeFail("listNodes");
        return { nodes: [...nodes.values()].filter((n) => !req.siteId || n.siteId === req.siteId) };
      },
      createEnrollmentToken: (req) => {
        calls.createEnrollmentToken.push({ siteId: req.siteId, idempotencyKey: req.idempotencyKey });
        maybeFail("createEnrollmentToken");
        if (!sites.some((s) => s.id === req.siteId)) throw new ConnectError("site not found", Code.NotFound);
        const token = `kdk_enroll_token${++tokens}`;
        return {
          siteId: req.siteId,
          token,
          expiresAt: timestampFromDate(new Date(Date.now() + 3600_000)),
          enrollCommand: `sudo deploy/nodes/install-agent.sh --server http://kondukter.test --token ${token}`,
        };
      },
      drainNode: (req) => {
        calls.drainNode.push({ nodeId: req.nodeId, reason: req.reason, idempotencyKey: req.idempotencyKey });
        maybeFail("drainNode");
        if (getNode(req.nodeId).status !== NodeStatus.READY) {
          throw new ConnectError("node is not ready", Code.FailedPrecondition);
        }
        return { node: setNode(req.nodeId, { status: NodeStatus.DRAINING, statusReason: req.reason || `drained by ${email}` }) };
      },
      undrainNode: (req) => {
        calls.undrainNode.push(req.nodeId);
        maybeFail("undrainNode");
        const s = getNode(req.nodeId).status;
        if (s !== NodeStatus.DRAINING && s !== NodeStatus.MAINTENANCE) {
          throw new ConnectError("node is not draining or in maintenance", Code.FailedPrecondition);
        }
        return { node: setNode(req.nodeId, { status: NodeStatus.READY, statusReason: "" }) };
      },
      getUtilization: (req) => {
        calls.getUtilization.push(req);
        maybeFail("getUtilization");
        const end = req.periodEnd ?? at("2026-11-08T12:00:00Z");
        const start = req.periodStart ?? timestampFromDate(new Date(timestampDate(end).getTime() - 24 * 3600_000));
        return {
          periodStart: start,
          periodEnd: end,
          nodes: [...nodes.values()]
            .filter((n) => !req.siteId || n.siteId === req.siteId)
            .map((n) =>
              create(NodeUtilizationSchema, {
                nodeId: n.id,
                hostname: n.hostname,
                status: n.status,
                allocatable: n.capacity?.total && n.capacity.reserved && minus(n.capacity.total, n.capacity.reserved),
                allocated: n.capacity?.allocated,
                acceleratorsTotal: n.accelerators.length,
                acceleratorsAllocated: n.accelerators.filter((a) => a.allocated).length,
                activeInstanceCount: n.activeInstanceCount,
                // Only the default period (no dates) has run time in this fake.
                instanceMinutes: req.periodStart ? 0n : BigInt(n.activeInstanceCount * 90),
              }),
            ),
        };
      },
    });
  });

  return { transport, calls, fail, nodes, sites, setNode };
}

export type FakeFleet = ReturnType<typeof fakeFleet>;
