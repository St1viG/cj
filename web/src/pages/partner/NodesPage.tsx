import type { Node } from "@gen/kondukter/api/v1/types_pb";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { Link } from "react-router";
import { useApi } from "../../api/ApiProvider";
import { keys } from "../../api/keys";
import { type Column, DataTable } from "../../components/DataTable";
import { useNow } from "../../hooks/useNow";
import { FleetMap } from "../../components/FleetMap";
import { DrainActions, FleetStats, nodeColumns, useNodeView, ViewToggle } from "../nodes";
import { SiteFilter, useSiteParam } from "./sites";
import { PageTitle } from "../../components/PageTitle";

/** `/partner/nodes`: the partner's nodes, all or one site's (`?site=`), with Drain and Undrain. */
export function NodesPage() {
  const { partner } = useApi();
  const [site] = useSiteParam();
  const now = useNow();
  const [view, setView] = useNodeView();
  const nodes = useQuery({
    queryKey: keys.fleet.partnerNodes(site),
    queryFn: () => partner.listNodes({ siteId: site }),
    placeholderData: keepPreviousData,
  });

  const c = nodeColumns(now);
  // Node facts only: the partner never sees who runs what (SPEC P-3).
  const columns: Column<Node>[] = [
    c.hostname,
    c.site,
    c.status,
    c.arch,
    c.driver,
    c.agentVersion,
    c.vcpuFree,
    c.ramFree,
    c.diskFree,
    c.accelerators,
    c.instances,
    c.lastSeen,
    { header: "Actions", cell: (n) => <DrainActions node={n} /> },
  ];

  return (
    <section>
      <PageTitle>Nodes</PageTitle>
      <FleetStats nodes={nodes.data?.nodes} />
      <div className="fleetbar">
        <SiteFilter />
        <ViewToggle view={view} onChange={setView} />
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
        empty={
          <>
            No nodes {site ? "at this site" : "yet"}. <Link to="/partner/enroll">Enroll a node</Link>.
          </>
        }
      />
      )}
      <p className="note">
        <small>
          Free is total minus reserved minus allocated. The table refreshes every 5 s. Drain stops new placements; running
          instances keep running, and a drained node with none left goes to maintenance.
        </small>
      </p>
    </section>
  );
}
