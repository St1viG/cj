import { useMemo, useState } from "react";
import { TimeChart } from "../../components/charts/TimeChart";
import { type Column, DataTable } from "../../components/DataTable";
import { Field } from "../../components/Field";
import { Form } from "../../components/Form";
import { PageTitle } from "../../components/PageTitle";
import { Stat } from "../../components/Stat";
import { StatusChip, type StatusKind } from "../../components/Status";
import {
  addFakeTopUp,
  dailyBalance,
  dailySpend,
  eur,
  type Invoice,
  invoices,
  type LedgerEntry,
  type LedgerKind,
  useFakeBilling,
} from "../../lib/fakeBilling";

const kindLabel: Record<LedgerKind, string> = { topup: "top-up", bonus: "bonus", usage: "usage", refund: "refund" };
const kindStatus: Record<LedgerKind, StatusKind> = { topup: "run", bonus: "run", refund: "pend", usage: "end" };

const ledgerColumns: Column<LedgerEntry>[] = [
  { header: "When", cell: (e) => new Date(e.at).toLocaleString("en", { dateStyle: "medium", timeStyle: "short" }), className: "m" },
  { header: "Type", cell: (e) => <StatusChip kind={kindStatus[e.kind]}>{kindLabel[e.kind]}</StatusChip> },
  { header: "Description", cell: (e) => e.description },
  { header: "Reference", cell: (e) => e.ref, className: "m" },
  { header: "Amount", cell: (e) => <span className={e.amountMinor > 0 ? "pos" : ""}>{eur(e.amountMinor, true)}</span>, className: "m num" },
  { header: "Balance after", cell: (e) => eur(e.balanceAfter), className: "m num ink-h" },
];

const invoiceColumns: Column<Invoice>[] = [
  { header: "Invoice", cell: (i) => i.id, className: "m ink-h" },
  { header: "Period", cell: (i) => new Date(`${i.month}-01T12:00:00`).toLocaleDateString("en", { month: "long", year: "numeric" }) },
  { header: "Total", cell: (i) => eur(i.totalMinor), className: "m num" },
  { header: "Status", cell: (i) => <StatusChip kind={i.status === "paid" ? "run" : "pend"}>{i.status}</StatusChip> },
];

type Filter = "all" | "credits" | "usage";
const VISIBLE = 12;

/**
 * `/app/billing`: a pretend balance with its history, a top-up form, invoices
 * and a payment method. It is generated in the browser (lib/fakeBilling.ts):
 * the demo has no ledger, so nothing here is real and no money moves.
 */
export function BillingPage() {
  const bill = useFakeBilling();
  const [filter, setFilter] = useState<Filter>("all");
  const [all, setAll] = useState(false);
  const [added, setAdded] = useState<string | null>(null);

  const balanceSeries = useMemo(() => dailyBalance(bill.entries), [bill.entries]);
  const spendSeries = useMemo(() => dailySpend(bill.entries), [bill.entries]);
  const rows = useMemo(() => {
    const newest = [...bill.entries].reverse();
    return newest.filter((e) => filter === "all" || (filter === "usage") === (e.kind === "usage"));
  }, [bill.entries, filter]);
  const shown = all ? rows : rows.slice(0, VISIBLE);

  return (
    <section>
      <PageTitle>Billing</PageTitle>
      <div className="g4">
        <Stat label="Balance" value={eur(bill.balance)} />
        <Stat label="Spent, 30 days" value={eur(bill.spent30)} />
        <Stat label="Daily burn" value={eur(bill.burnPerDay)} unit="/ day" />
        <Stat label="Runway" value={bill.runwayDays === undefined ? "∞" : Math.floor(bill.runwayDays)} unit="days" />
      </div>

      <div className="g2">
        <section>
          <h2>Add credit</h2>
          <Form
            submitLabel="Add credit"
            onSubmit={(data) => {
              const cents = Number(data.get("amount")) * 100;
              addFakeTopUp(cents);
              setAdded(eur(cents));
            }}
          >
            <Field as="select" label="Amount" name="amount" defaultValue="25">
              {[10, 25, 50, 100].map((a) => (
                <option key={a} value={a}>
                  €{a}
                </option>
              ))}
            </Field>
          </Form>
          {added && (
            <p role="status" className="msg">
              Added {added} to your balance.
            </p>
          )}
        </section>
        <section>
          <h2>Payment method</h2>
          <dl>
            <dt>Card</dt>
            <dd>Visa •••• 4242</dd>
            <dt>Expires</dt>
            <dd>09/28</dd>
            <dt>Auto top-up</dt>
            <dd>off</dd>
          </dl>
        </section>
      </div>

      <div className="g2">
        <section>
          <h2>Balance, last 30 days</h2>
          <TimeChart points={balanceSeries} kind="line" label="Balance" format={(v) => eur(Math.round(v))} />
        </section>
        <section>
          <h2>Daily spend</h2>
          <TimeChart points={spendSeries} kind="bars" label="Daily spend" format={(v) => eur(Math.round(v))} />
        </section>
      </div>

      <div className="tabs" role="tablist" aria-label="History filter">
        {(["all", "credits", "usage"] as const).map((f) => (
          <button key={f} type="button" role="tab" aria-selected={filter === f} className="g" onClick={() => setFilter(f)}>
            {f === "all" ? "All activity" : f === "credits" ? "Credits" : "Usage"}
          </button>
        ))}
      </div>
      <DataTable caption="Balance history" columns={ledgerColumns} rows={shown} rowKey={(e) => e.id} empty="No activity yet." />
      {rows.length > VISIBLE && (
        <button type="button" className="sm" style={{ width: "fit-content" }} onClick={() => setAll((v) => !v)}>
          {all ? "Show fewer" : `Show all ${rows.length}`}
        </button>
      )}

      <DataTable caption="Invoices" columns={invoiceColumns} rows={invoices(bill.entries)} rowKey={(i) => i.id} empty="No invoices yet." />
    </section>
  );
}
