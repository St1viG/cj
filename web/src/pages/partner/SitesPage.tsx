import type { Site } from "@gen/kondukter/api/v1/partner_pb";
import { Link } from "react-router";
import { type Column, DataTable } from "../../components/DataTable";
import { formatTimestamp, nodeStatusLabel } from "../../lib/format";
import { useSites } from "./sites";

const columns: Column<Site>[] = [
  { header: "Name", cell: (s) => s.name },
  { header: "Region", cell: (s) => s.region },
  { header: "Country", cell: (s) => s.countryCode || "—" },
  { header: "Network mode", cell: (s) => s.networkMode || "—" },
  { header: "Nodes", cell: (s) => s.nodeCount },
  {
    header: "Nodes by status",
    cell: (s) => (s.nodesByStatus.length ? s.nodesByStatus.map((c) => `${nodeStatusLabel(c.status)} ${c.count}`).join(", ") : "—"),
  },
  { header: "Created", cell: (s) => formatTimestamp(s.createdAt) },
  {
    header: "Actions",
    cell: (s) => (
      <>
        <Link to={`/partner/nodes?site=${s.id}`}>Nodes</Link> · <Link to={`/partner/enroll?site=${s.id}`}>Enroll a node</Link>
      </>
    ),
  },
];

/** `/partner/sites`: the partner's sites with their nodes counted by status. */
export function SitesPage() {
  const sites = useSites();
  return (
    <section>
      <h1>Sites</h1>
      <DataTable
        columns={columns}
        rows={sites.data?.sites}
        rowKey={(s) => s.id}
        loading={sites.isPending}
        error={sites.error}
        onRetry={() => void sites.refetch()}
        empty="No sites yet. The operator creates a partner's sites."
      />
    </section>
  );
}
