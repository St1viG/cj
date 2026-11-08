import type { Timestamp } from "@bufbuild/protobuf/wkt";
import { type Node, NodeStatus } from "@gen/kondukter/api/v1/types_pb";
import { type QueryClient, useQueryClient } from "@tanstack/react-query";
import { useApi } from "../api/ApiProvider";
import { keys } from "../api/keys";
import { Confirm } from "../components/Confirm";
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
function freeOf(n: Node, pick: "vcpu" | "ramMib" | "diskGib", format: (v: number | bigint) => string): string {
  const total = n.capacity?.total;
  const free = n.capacity?.free;
  return total && free ? `${format(free[pick])} of ${format(total[pick])}` : "—";
}

export function lastSeen(ts: Timestamp | undefined, now: number) {
  if (!ts) return "never";
  return <span title={formatTimestamp(ts)}>{formatAge(ts, now)} ago</span>;
}

/** The columns by name; a page picks the ones it shows. `now` is for "last seen". */
export function nodeColumns(now: number) {
  return {
    hostname: { header: "Hostname", cell: (n: Node) => <span title={n.id}>{nodeName(n)}</span> },
    site: { header: "Site", cell: (n: Node) => n.siteName },
    partner: { header: "Partner", cell: (n: Node) => n.partnerName },
    status: { header: "Status", cell: nodeStatusText },
    arch: { header: "Arch", cell: (n: Node) => n.arch || "—" },
    driver: { header: "Driver", cell: (n: Node) => n.driver || "—" },
    agentVersion: { header: "Agent version", cell: (n: Node) => n.agentVersion || "—" },
    vcpuFree: { header: "vCPU free", cell: (n: Node) => freeOf(n, "vcpu", String) },
    ramFree: { header: "RAM free", cell: (n: Node) => freeOf(n, "ramMib", formatMiB) },
    diskFree: { header: "Disk free", cell: (n: Node) => freeOf(n, "diskGib", formatGiB) },
    accelerators: { header: "Accelerators", cell: acceleratorsText },
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
