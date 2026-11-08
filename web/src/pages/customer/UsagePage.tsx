import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import type { InstanceUsage } from "@gen/kondukter/api/v1/usage_pb";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router";
import { useApi } from "../../api/ApiProvider";
import { keys } from "../../api/keys";
import { type Column, DataTable } from "../../components/DataTable";
import { Field } from "../../components/Field";
import { phaseKind, StatusChip } from "../../components/Status";
import { Form } from "../../components/Form";
import { formatMinutes, formatMoney, phaseLabel, shortId } from "../../lib/format";
import { PageTitle } from "../../components/PageTitle";

/** A `YYYY-MM-DD` from a date input, as local midnight; `undefined` when empty. */
function localDay(value: string): Date | undefined {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value);
  return m ? new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3])) : undefined;
}

const columns: Column<InstanceUsage>[] = [
  {
    header: "Instance",
    cell: (u) => (
      <Link to={`/app/instances/${u.instanceId}`} title={u.instanceId}>
        {u.instanceName || shortId(u.instanceId)}
      </Link>
    ),
  },
  { header: "Offer", cell: (u) => u.shapeName },
  { header: "Phase", cell: (u) => <StatusChip kind={phaseKind(u.phase)}>{phaseLabel(u.phase)}</StatusChip> },
  { header: "Price / hour", cell: (u) => formatMoney(u.pricePerHour) },
  { header: "Running", cell: (u) => formatMinutes(u.usage?.runningMinutes) },
  { header: "Estimated cost", cell: (u) => formatMoney(u.usage?.estimatedCost) },
];

/**
 * `/app/usage`: running time and estimated cost per instance for a period.
 * The period is whole days in the browser's time zone: from the start of
 * "From" to the end of "To". Costs are estimates (ADR-0023); no money moves.
 */
export function UsagePage() {
  const { usage } = useApi();
  const [period, setPeriod] = useState({ from: "", to: "" });

  const query = useQuery({
    queryKey: keys.usage.list(period),
    queryFn: () => {
      const from = localDay(period.from);
      const to = localDay(period.to);
      if (to) to.setDate(to.getDate() + 1); // the end is exclusive: include all of "To"
      return usage.listUsage({
        periodStart: from && timestampFromDate(from),
        periodEnd: to && timestampFromDate(to),
      });
    },
  });
  const costs = query.data?.totalEstimatedCost ?? [];

  return (
    <section>
      <PageTitle>Usage</PageTitle>
      <Form inline submitLabel="Show period" onSubmit={(data) => setPeriod({ from: String(data.get("from") ?? ""), to: String(data.get("to") ?? "") })}>
        <Field label="From" name="from" type="date" defaultValue={period.from} />
        <Field label="To (included)" name="to" type="date" defaultValue={period.to} />
      </Form>
      <DataTable
        caption={period.from || period.to ? `Usage ${period.from || "from the start"} to ${period.to || "now"}` : "Usage, all time"}
        columns={columns}
        rows={query.data?.instances}
        rowKey={(u) => u.instanceId}
        loading={query.isPending}
        error={query.error}
        onRetry={() => void query.refetch()}
        empty="No instances ran in this period."
      />
      {query.data && query.data.instances.length > 0 && (
        <dl>
          <dt>Total running time</dt>
          <dd>{formatMinutes(query.data.totalRunningMinutes)}</dd>
          <dt>Total estimated cost</dt>
          <dd>{costs.length === 0 ? formatMoney(undefined) : costs.map((m) => formatMoney(m)).join(" + ")}</dd>
        </dl>
      )}
    </section>
  );
}
