import type { Instance } from "@gen/kondukter/api/v1/instance_pb";
import { keepPreviousData, useQuery, useQueryClient } from "@tanstack/react-query";
import { useApi } from "../../api/ApiProvider";
import { keys } from "../../api/keys";
import { Confirm } from "../../components/Confirm";
import { phaseKind, StatusChip } from "../../components/Status";
import { type Column, DataTable } from "../../components/DataTable";
import { useNow } from "../../hooks/useNow";
import { useParam } from "../../hooks/useParam";
import { isTerminalPhase } from "../../hooks/useWatchInstance";
import { canTerminate, formatAge, formatMoney, formatTimestamp, phaseLabel, shortId, withReason } from "../../lib/format";
import { newId } from "../../lib/ids";
import { PageTitle } from "../../components/PageTitle";

/** The most instances one ListInstances call returns. */
export const ADMIN_INSTANCE_PAGE_SIZE = 200;

/** Force terminate any org's instance; the reason goes into the phase reason and the audit log. */
function ForceTerminate({ instance: i }: { instance: Instance }) {
  const { admin } = useApi();
  const qc = useQueryClient();
  const what = `${i.name || shortId(i.id)} of ${i.orgName || shortId(i.orgId)}`;
  return (
    <Confirm
      label="Force terminate"
      message={`Force terminate ${what}? Its VM is deleted now and the customer cannot undo it.`}
      reason="Reason (optional, kept in the audit log)"
      disabled={!canTerminate(i.phase)}
      onConfirm={async (reason) => {
        await admin.forceTerminateInstance({ instanceId: i.id, reason, idempotencyKey: newId() });
        // The fleet root: the node's instance count changes too.
        await qc.invalidateQueries({ queryKey: keys.fleet.all });
      }}
    />
  );
}

/**
 * `/admin/instances`: every org's instances, newest first. `?all=1` adds the
 * ended ones (terminated, failed, lost); `?node=` and `?org=` narrow the list.
 */
export function InstancesPage() {
  const { admin } = useApi();
  const [all, setAll] = useParam("all");
  const [node, setNode] = useParam("node");
  const [org, setOrg] = useParam("org");
  const now = useNow();
  const includeTerminal = all === "1";
  const list = useQuery({
    queryKey: keys.fleet.adminInstances({ includeTerminal, node, org }),
    queryFn: () => admin.listInstances({ includeTerminal, nodeId: node, orgId: org, pageSize: ADMIN_INSTANCE_PAGE_SIZE }),
    placeholderData: keepPreviousData,
  });
  // Hostnames for the Node column; the same query as /admin/nodes unfiltered.
  const nodes = useQuery({
    queryKey: keys.fleet.adminNodes({ site: "", status: 0 }),
    queryFn: () => admin.listNodes({}),
  });
  const hostname = (id: string) => nodes.data?.nodes.find((n) => n.id === id)?.hostname || shortId(id);
  const rows = list.data?.instances;
  const orgName = rows?.find((i) => i.orgId === org)?.orgName;

  const columns: Column<Instance>[] = [
    { header: "Id", cell: (i) => <span className="m ink-h" title={i.id}>{shortId(i.id)}</span> },
    { header: "Name", cell: (i) => i.name || "—" },
    {
      header: "Org",
      cell: (i) => (
        <button type="button" className="sm g m" title="Show only this org" onClick={() => setOrg(i.orgId)}>
          {i.orgName || shortId(i.orgId)}
        </button>
      ),
    },
    { header: "Phase", cell: (i) => <StatusChip kind={phaseKind(i.phase)}>{withReason(phaseLabel(i.phase), i.phaseReason)}</StatusChip> },
    { header: "In phase for", cell: (i) => (isTerminalPhase(i.phase) ? "—" : formatAge(i.phaseChangedAt, now)) },
    {
      header: "Node",
      cell: (i) =>
        i.nodeId ? (
          <button type="button" className="sm g m" title="Show only this node" onClick={() => setNode(i.nodeId)}>
            {hostname(i.nodeId)}
          </button>
        ) : (
          "—"
        ),
    },
    { header: "Offer", cell: (i) => <span title={i.offerId}>{`${i.shape?.name ?? "—"}, ${formatMoney(i.pricePerHour)}/h`}</span> },
    { header: "Region", cell: (i) => i.region },
    { header: "Arch", cell: (i) => i.arch || "—" },
    { header: "Created", cell: (i) => formatTimestamp(i.createdAt) },
    { header: "Actions", cell: (i) => <ForceTerminate instance={i} /> },
  ];

  return (
    <section>
      <PageTitle>Instances</PageTitle>
      <label>
        <input type="checkbox" checked={includeTerminal} onChange={(e) => setAll(e.currentTarget.checked ? "1" : "")} /> Show
        terminated, failed and lost instances
      </label>
      {(node || org) && (
        <p className="row">
          {node && (
            <>
              Node: {hostname(node)}{" "}
              <button type="button" className="sm" onClick={() => setNode("")}>
                Any node
              </button>{" "}
            </>
          )}
          {org && (
            <>
              Org: {orgName ?? shortId(org)}{" "}
              <button type="button" className="sm" onClick={() => setOrg("")}>
                Any org
              </button>
            </>
          )}
        </p>
      )}
      <DataTable
        columns={columns}
        rows={rows}
        rowKey={(i) => i.id}
        loading={list.isPending}
        error={list.error}
        onRetry={() => void list.refetch()}
        empty={includeTerminal || node || org ? "No instances match the filter." : "No active instances."}
      />
      <p>
        <small>
          {rows && list.data?.nextPageToken ? `The newest ${rows.length} are shown; narrow by org or node to see others. ` : ""}
          Every org's instances, newest first; the table refreshes every 5 s. Select an org or a node to narrow the list.
        </small>
      </p>
    </section>
  );
}
