import { type Node, NodeStatus } from "@gen/kondukter/api/v1/types_pb";
import { keepPreviousData, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router";
import { useApi } from "../../api/ApiProvider";
import { keys } from "../../api/keys";
import { Confirm } from "../../components/Confirm";
import { type Column, DataTable } from "../../components/DataTable";
import { useNow } from "../../hooks/useNow";
import { useParam } from "../../hooks/useParam";
import { nodeStatusLabel } from "../../lib/format";
import { newId } from "../../lib/ids";
import { FleetMap } from "../../components/FleetMap";
import { DrainActions, FleetStats, nodeColumns, nodeName, refetchNodes, useNodeView, ViewToggle } from "../nodes";
import { SiteFilter, useSiteParam } from "../partner/sites";
import { PageTitle } from "../../components/PageTitle";

/** The statuses `?status=` can name, in lifecycle order. */
const statuses = [
  NodeStatus.ENROLLING,
  NodeStatus.READY,
  NodeStatus.DRAINING,
  NodeStatus.MAINTENANCE,
  NodeStatus.UNREACHABLE,
  NodeStatus.QUARANTINED,
  NodeStatus.RETIRED,
];

/** Desired minus observed generation: the desired-state changes the agent has not confirmed yet. */
export function generationLag(n: Node): string {
  const lag = n.desiredGeneration - n.observedGeneration;
  return lag > 0n ? `${lag} behind (desired ${n.desiredGeneration}, observed ${n.observedGeneration})` : `0 (${n.observedGeneration})`;
}

/** The server allows Quarantine from every status but quarantined and retired. */
export const canQuarantine = (s: NodeStatus) => s !== NodeStatus.QUARANTINED && s !== NodeStatus.RETIRED;

/**
 * Quarantine on any node not quarantined or retired, with an optional reason
 * (the server's is "quarantined by <email>"); Unquarantine on a quarantined
 * one. Both ask first.
 */
function QuarantineActions({ node }: { node: Node }) {
  const { admin } = useApi();
  const qc = useQueryClient();
  const name = nodeName(node);
  if (node.status === NodeStatus.QUARANTINED) {
    return (
      <Confirm
        label="Unquarantine"
        message={`Unquarantine ${name}? It becomes ready, or unreachable until its agent connects.`}
        onConfirm={async () => {
          await admin.unquarantineNode({ nodeId: node.id, idempotencyKey: newId() });
          await refetchNodes(qc);
        }}
      />
    );
  }
  if (!canQuarantine(node.status)) return null;
  return (
    <Confirm
      label="Quarantine"
      message={`Quarantine ${name}? It takes no new instances and its reports are ignored until it is unquarantined. Instances on it keep running (active: ${node.activeInstanceCount}); force-terminate them under Instances if needed.`}
      reason="Reason (optional, kept in the audit log)"
      onConfirm={async (reason) => {
        await admin.quarantineNode({ nodeId: node.id, reason, idempotencyKey: newId() });
        await refetchNodes(qc);
      }}
    />
  );
}

/** `/admin/nodes`: every partner's nodes, narrowed by `?site=` and `?status=`, with Quarantine, Drain and their undo. */
export function NodesPage() {
  const { admin } = useApi();
  const [site] = useSiteParam();
  const [status, setStatus] = useParam("status");
  const now = useNow();
  const [view, setView] = useNodeView();
  const chosen = statuses.find((s) => nodeStatusLabel(s) === status);
  const nodes = useQuery({
    queryKey: keys.fleet.adminNodes({ site, status: chosen ?? 0 }),
    queryFn: () => admin.listNodes({ siteId: site, statuses: chosen ? [chosen] : [] }),
    placeholderData: keepPreviousData,
  });

  const c = nodeColumns(now);
  const columns: Column<Node>[] = [
    c.hostname,
    c.partner,
    c.site,
    c.status,
    c.arch,
    c.driver,
    c.agentVersion,
    c.vcpuFree,
    c.ramFree,
    c.diskFree,
    c.accelerators,
    {
      header: c.instances.header,
      cell: (n) =>
        n.activeInstanceCount > 0 ? (
          <Link to={`/admin/instances?node=${n.id}`} title={`Instances on ${nodeName(n)}`}>
            {n.activeInstanceCount}
          </Link>
        ) : (
          0
        ),
    },
    { header: "Generation lag", cell: generationLag },
    c.lastSeen,
    {
      header: "Actions",
      cell: (n) => (
        <>
          <QuarantineActions node={n} /> <DrainActions node={n} />
        </>
      ),
    },
  ];

  return (
    <section>
      <PageTitle>Nodes</PageTitle>
      <FleetStats nodes={nodes.data?.nodes} />
      <div className="fleetbar">
      <SiteFilter />
      <ViewToggle view={view} onChange={setView} />
      </div>
      <div className="filterrow">
        <label>
          Status{" "}
          <select value={chosen ? status : ""} onChange={(e) => setStatus(e.target.value)}>
            <option value="">all statuses</option>
            {statuses.map((s) => (
              <option key={s} value={nodeStatusLabel(s)}>
                {nodeStatusLabel(s)}
              </option>
            ))}
          </select>
        </label>
      </div>
      {view === "map" && nodes.data ? (
        <FleetMap nodes={nodes.data.nodes} />
      ) : (
      <DataTable
        columns={columns}
        rows={nodes.data?.nodes}
        rowKey={(n) => n.id}
        loading={nodes.isPending}
        error={nodes.error}
        onRetry={() => void nodes.refetch()}
        empty={site || chosen ? "No nodes match the filter." : "No nodes enrolled yet."}
      />
      )}
      <p className="note">
        <small>
          Every partner's nodes; the table refreshes every 5 s. Quarantine stops placements and ignores the node's reports;
          Drain stops placements and lets running instances finish. Generation lag is desired minus observed generation:
          desired-state changes the agent has not confirmed yet.
        </small>
      </p>
    </section>
  );
}
