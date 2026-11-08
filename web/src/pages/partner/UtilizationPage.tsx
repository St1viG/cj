import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import type { NodeUtilization } from "@gen/kondukter/api/v1/partner_pb";
import type { ResourceAmounts } from "@gen/kondukter/api/v1/types_pb";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { useApi } from "../../api/ApiProvider";
import { keys } from "../../api/keys";
import { type Column, DataTable } from "../../components/DataTable";
import { Field } from "../../components/Field";
import { ResourceBar } from "../../components/ResourceBar";
import { nodeKind, StatusChip } from "../../components/Status";
import { Form } from "../../components/Form";
import { formatGiB, formatMiB, formatMinutes, formatTimestamp, nodeStatusLabel, shortId } from "../../lib/format";
import { SiteFilter, useSiteParam } from "./sites";
import { PageTitle } from "../../components/PageTitle";

/** A `YYYY-MM-DD` from a date input, as local midnight; `undefined` when empty. */
function localDay(value: string): Date | undefined {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value);
  return m ? new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3])) : undefined;
}

/** "2 of 8 (25%)": allocated now of allocatable (total minus reserved). */
function share(allocated: number, of: number, format: (v: number) => string = String): string {
  const pct = of > 0 ? ` (${Math.round((allocated / of) * 100)}%)` : "";
  return `${format(allocated)} of ${format(of)}${pct}`;
}

const pick = (r: ResourceAmounts | undefined, k: "vcpu" | "ramMib" | "diskGib") => Number(r?.[k] ?? 0);

const columns: Column<NodeUtilization>[] = [
  { header: "Hostname", cell: (u) => <span title={u.nodeId}>{u.hostname || shortId(u.nodeId)}</span> },
  { header: "Status", cell: (u) => <StatusChip kind={nodeKind(u.status)}>{nodeStatusLabel(u.status)}</StatusChip> },
  { header: "vCPU allocated", cell: (u) => <ResourceBar text={share(pick(u.allocated, "vcpu"), pick(u.allocatable, "vcpu"))} used={pick(u.allocated, "vcpu")} total={pick(u.allocatable, "vcpu")} /> },
  { header: "RAM allocated", cell: (u) => <ResourceBar text={share(pick(u.allocated, "ramMib"), pick(u.allocatable, "ramMib"), formatMiB)} used={pick(u.allocated, "ramMib")} total={pick(u.allocatable, "ramMib")} /> },
  { header: "Disk allocated", cell: (u) => <ResourceBar text={share(pick(u.allocated, "diskGib"), pick(u.allocatable, "diskGib"), formatGiB)} used={pick(u.allocated, "diskGib")} total={pick(u.allocatable, "diskGib")} /> },
  {
    header: "Accelerators allocated",
    cell: (u) => (u.acceleratorsTotal ? <ResourceBar text={share(u.acceleratorsAllocated, u.acceleratorsTotal)} used={u.acceleratorsAllocated} total={u.acceleratorsTotal} /> : "none"),
  },
  { header: "Active instances", cell: (u) => u.activeInstanceCount },
  { header: "Instance minutes", cell: (u) => formatMinutes(u.instanceMinutes) },
];

/**
 * `/partner/utilization`: per node, what active instances hold now and how
 * many instance minutes ran in the period. The period is whole local days;
 * with none chosen the server reports the last 24 hours.
 */
export function UtilizationPage() {
  const { partner } = useApi();
  const [site] = useSiteParam();
  const [period, setPeriod] = useState({ from: "", to: "" });
  const util = useQuery({
    queryKey: keys.fleet.utilization({ site, ...period }),
    queryFn: () => {
      const from = localDay(period.from);
      const to = localDay(period.to);
      if (to) to.setDate(to.getDate() + 1); // the end is exclusive: include all of "To"
      return partner.getUtilization({
        siteId: site,
        periodStart: from && timestampFromDate(from),
        periodEnd: to && timestampFromDate(to),
      });
    },
    placeholderData: keepPreviousData,
  });
  const data = util.data;
  const total = data?.nodes.reduce((t, u) => t + u.instanceMinutes, 0n);

  return (
    <section>
      <PageTitle>Utilization</PageTitle>
      <SiteFilter />
      <Form inline submitLabel="Show period" onSubmit={(d) => setPeriod({ from: String(d.get("from") ?? ""), to: String(d.get("to") ?? "") })}>
        <Field label="From" name="from" type="date" defaultValue={period.from} hint="Empty: 24 hours before the end." />
        <Field label="To (included)" name="to" type="date" defaultValue={period.to} hint="Empty: now." />
      </Form>
      {data && (
        <p>
          <span data-testid="utilization-period">
            {formatTimestamp(data.periodStart)} to {formatTimestamp(data.periodEnd)}
          </span>
          : <strong data-testid="utilization-total">{formatMinutes(total)}</strong> of instance time on {data.nodes.length}{" "}
          node(s).
        </p>
      )}
      <DataTable
        columns={columns}
        rows={data?.nodes}
        rowKey={(u) => u.nodeId}
        loading={util.isPending}
        error={util.error}
        onRetry={() => void util.refetch()}
        empty="No nodes to report."
      />
      <p>
        <small>
          Allocated is what active instances hold now, of allocatable (total minus reserved). Instance minutes is the running
          time of instances on the node within the period.
        </small>
      </p>
    </section>
  );
}
