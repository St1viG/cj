import type { Timestamp } from "@bufbuild/protobuf/wkt";
import { type Node, NodeStatus } from "@gen/kondukter/api/v1/types_pb";
import { type QueryClient, useQueryClient } from "@tanstack/react-query";
import { useApi } from "../api/ApiProvider";
import { keys } from "../api/keys";
import { Confirm } from "../components/Confirm";
import { ResourceBar } from "../components/ResourceBar";
import { Stat } from "../components/Stat";
import { useParam } from "../hooks/useParam";
import { nodeKind, StatusChip } from "../components/Status";
import type { Column } from "../components/DataTable";
import { formatAge, formatGiB, formatMiB, formatTimestamp, healthLabel, nodeStatusLabel, shortId, withReason } from "../lib/format";
import { newId } from "../lib/ids";

/*
 * Node columns and actions shared by /partner/nodes and /admin/nodes. They
 * show node facts only: never who runs what on the node (SPEC P-3).
 */

export function nodeName(n: Node): string {
  return n.hostname || shortId(n.id);
}

export function nodeStatusText(n: Node): string {
  return withReason(nodeStatusLabel(n.status), n.statusReason);
}

/** "GeForce GTX 1060 6GB (ok, in use)"; "none" when the node has none. */
export function acceleratorsText(n: Node): string {
  if (n.accelerators.length === 0) return "none";
  return n.accelerators
    .map((a) => {
      const health = withReason(healthLabel(a.health), a.healthReason);
      return `${a.model || a.kind} (${health}${a.allocated ? ", in use" : ""})`;
    })
    .join("; ");
}

/** "6 of 8": free of total, as `kctl nodes` prints "6/8". Free is total minus reserved minus allocated. */
function freeOf(n: Node, pick: "vcpu" | "ramMib" | "diskGib", format: (v: number | bigint) => string) {
  const total = n.capacity?.total;
  const free = n.capacity?.free;
  if (!total || !free) return "—";
  return (
    <ResourceBar text={`${format(free[pick])} of ${format(total[pick])}`} used={Number(total[pick]) - Number(free[pick])} total={Number(total[pick])} />
  );
}

export function lastSeen(ts: Timestamp | undefined, now: number) {
  if (!ts) return "never";
  return (
    <span className="m" title={formatTimestamp(ts)}>
      {formatAge(ts, now)} ago
    </span>
  );
}

/** The columns by name; a page picks the ones it shows. `now` is for "last seen". */
export function nodeColumns(now: number) {
  return {
    hostname: {
      header: "Hostname",
      cell: (n: Node) => (
        <span className="m ink-h" title={n.id}>
          {nodeName(n)}
        </span>
      ),
    },
    site: { header: "Site", cell: (n: Node) => n.siteName },
    partner: { header: "Partner", cell: (n: Node) => n.partnerName },
    status: { header: "Status", cell: (n: Node) => <StatusChip kind={nodeKind(n.status)}>{nodeStatusText(n)}</StatusChip> },
    arch: { header: "Arch", cell: (n: Node) => n.arch || "—", className: "m" },
    driver: { header: "Driver", cell: (n: Node) => n.driver || "—", className: "m" },
    agentVersion: { header: "Agent version", cell: (n: Node) => n.agentVersion || "—", className: "m" },
    vcpuFree: { header: "vCPU free", cell: (n: Node) => freeOf(n, "vcpu", String) },
    ramFree: { header: "RAM free", cell: (n: Node) => freeOf(n, "ramMib", formatMiB) },
    diskFree: { header: "Disk free", cell: (n: Node) => freeOf(n, "diskGib", formatGiB) },
    accelerators: {
      header: "Accelerators",
      cell: (n: Node) => (n.accelerators.length === 0 ? "none" : <span className="chip gpu">{acceleratorsText(n)}</span>),
    },
    instances: { header: "Active instances", cell: (n: Node) => n.activeInstanceCount },
    lastSeen: { header: "Last seen", cell: (n: Node) => lastSeen(n.lastSeenAt, now) },
  } satisfies Record<string, Column<Node>>;
}

/** Refetches every view of nodes after a status change, and waits for them. */
export function refetchNodes(qc: QueryClient) {
  return qc.invalidateQueries({ queryKey: keys.fleet.all });
}

/** The node transitions the server allows for Drain and Undrain; it has the last word. */
export const canDrain = (s: NodeStatus) => s === NodeStatus.READY;
export const canUndrain = (s: NodeStatus) => s === NodeStatus.DRAINING || s === NodeStatus.MAINTENANCE;

/**
 * Drain on a READY node, Undrain on a DRAINING or MAINTENANCE one. Both ask
 * first; Drain takes an optional reason, which the server stores as the
 * status reason and in the audit log ("drained by <email>" when empty).
 */
export function DrainActions({ node }: { node: Node }) {
  const { partner } = useApi();
  const qc = useQueryClient();
  const name = nodeName(node);
  return (
    <>
      {canDrain(node.status) && (
        <Confirm
          label="Drain"
          message={`Drain ${name}? No new instances are placed on it; running ones keep running.`}
          reason="Reason (optional, kept in the audit log)"
          onConfirm={async (reason) => {
            await partner.drainNode({ nodeId: node.id, reason, idempotencyKey: newId() });
            await refetchNodes(qc);
          }}
        />
      )}
      {canUndrain(node.status) && (
        <Confirm
          label="Undrain"
          message={`Undrain ${name}? It becomes ready and takes new instances again.`}
          onConfirm={async () => {
            await partner.undrainNode({ nodeId: node.id, idempotencyKey: newId() });
            await refetchNodes(qc);
          }}
        />
      )}
    </>
  );
}

/** `?view=map` shows the fleet map instead of the table; the table is the default. */
export function useNodeView(): ["table" | "map", (v: "table" | "map") => void] {
  const [view, setView] = useParam("view");
  return [view === "map" ? "map" : "table", (v) => setView(v === "map" ? "map" : "")];
}

/** The Table / Map switch above a node list. */
export function ViewToggle({ view, onChange }: { view: "table" | "map"; onChange: (v: "table" | "map") => void }) {
  return (
    <div className="seg" role="group" aria-label="Node view">
      <button type="button" aria-pressed={view === "table"} onClick={() => onChange("table")}>
        Table
      </button>
      <button type="button" aria-pressed={view === "map"} onClick={() => onChange("map")}>
        Map
      </button>
    </div>
  );
}

/** Three telemetry tiles over a node list: how many nodes, how many ready, and the GPUs in use. */
export function FleetStats({ nodes }: { nodes: readonly Node[] | undefined }) {
  if (!nodes) return null;
  const gpus = nodes.flatMap((n) => n.accelerators);
  return (
    <div className="g3">
      <Stat label="Nodes" value={nodes.length} />
      <Stat label="Ready" value={nodes.filter((n) => n.status === NodeStatus.READY).length} unit={`of ${nodes.length}`} />
      <Stat label="GPUs in use" value={gpus.filter((g) => g.allocated).length} unit={`of ${gpus.length}`} />
    </div>
  );
}
