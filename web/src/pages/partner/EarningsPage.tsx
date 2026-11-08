import { useMemo } from "react";
import { TimeChart } from "../../components/charts/TimeChart";
import { type Column, DataTable } from "../../components/DataTable";
import { PageTitle } from "../../components/PageTitle";
import { ResourceBar } from "../../components/ResourceBar";
import { Stat } from "../../components/Stat";
import { StatusChip } from "../../components/Status";
import { dailyEarnings, earningsByNode, type NodeEarnings, type Payout, payouts } from "../../lib/fakeEarnings";
import { eur } from "../../lib/fakeBilling";

const nodeColumns: Column<NodeEarnings>[] = [
  { header: "Node", cell: (n) => n.node, className: "m ink-h" },
  { header: "Hardware", cell: (n) => n.kind, className: "m" },
  { header: "Hours rented", cell: (n) => `${n.hours} h`, className: "m num" },
  { header: "Utilization", cell: (n) => <ResourceBar text={`${n.utilization}%`} used={n.utilization} total={100} /> },
  { header: "Earned", cell: (n) => eur(n.earnedMinor), className: "m num ink-h" },
];

const payoutColumns: Column<Payout>[] = [
  { header: "Payout", cell: (p) => p.id, className: "m ink-h" },
  { header: "Date", cell: (p) => new Date(p.date).toLocaleDateString("en", { dateStyle: "medium" }), className: "m" },
  { header: "Method", cell: (p) => p.method, className: "m" },
  { header: "Amount", cell: (p) => eur(p.amountMinor), className: "m num" },
  { header: "Status", cell: (p) => <StatusChip kind={p.status === "paid" ? "run" : "pend"}>{p.status}</StatusChip> },
];

/**
 * `/partner/earnings`: what lending idle machines would earn, with an
 * illustrative revenue share. Generated in the browser (lib/fakeEarnings.ts);
 * the commercial model is undecided and nothing here is real.
 */
export function EarningsPage() {
  const nodes = useMemo(() => earningsByNode(), []);
  const daily = useMemo(() => dailyEarnings(), []);
  const list = useMemo(() => payouts(), []);
  const earned = nodes.reduce((s, n) => s + n.earnedMinor, 0);
  const avgUtil = Math.round(nodes.reduce((s, n) => s + n.utilization, 0) / nodes.length);
  const lifetime = earned + list.filter((p) => p.status === "paid").reduce((s, p) => s + p.amountMinor, 0);

  return (
    <section>
      <PageTitle>Earnings</PageTitle>
      <div className="g4">
        <Stat label="Earned, 30 days" value={eur(earned)} />
        <Stat label="Next payout" value={eur(list[0]?.amountMinor ?? 0)} />
        <Stat label="Avg utilization" value={`${avgUtil}%`} />
        <Stat label="Lifetime" value={eur(lifetime)} />
      </div>

      <section>
        <h2>Daily earnings</h2>
        <TimeChart points={daily} kind="bars" label="Daily earnings" format={(v) => eur(Math.round(v))} />
      </section>

      <DataTable caption="Earnings by node, last 30 days" columns={nodeColumns} rows={nodes} rowKey={(n) => n.node} />
      <DataTable caption="Payouts" columns={payoutColumns} rows={list} rowKey={(p) => p.id} />
    </section>
  );
}
