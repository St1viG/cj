import type { Offer } from "@gen/kondukter/api/v1/catalog_pb";
import { CapacityClass } from "@gen/kondukter/api/v1/types_pb";
import { useMemo, useState } from "react";
import { Link } from "react-router";
import { type Column, DataTable } from "../../components/DataTable";
import { Field } from "../../components/Field";
import { Form } from "../../components/Form";
import { capacityLabel, formatAccelerator, formatGiB, formatMiB, formatMoney } from "../../lib/format";
import { type OffersFilter, useOffers } from "./queries";
import { PageTitle } from "../../components/PageTitle";

const columns: Column<Offer>[] = [
  { header: "Shape", cell: (o) => o.shape?.name },
  {
    header: "Accelerator",
    cell: (o) =>
      o.shape && o.shape.acceleratorCount > 0 ? <span className="chip gpu">{formatAccelerator(o.shape)}</span> : formatAccelerator(o.shape),
  },
  { header: "vCPU", cell: (o) => o.shape?.vcpu },
  { header: "RAM", cell: (o) => formatMiB(o.shape?.ramMib ?? 0n) },
  { header: "Disk", cell: (o) => formatGiB(o.shape?.diskGib ?? 0n) },
  { header: "Architectures", cell: (o) => o.archs.join(", ") || "none yet" },
  { header: "Region", cell: (o) => o.region },
  { header: "Capacity", cell: (o) => capacityLabel(o.capacityClass) },
  { header: "Price / hour", cell: (o) => formatMoney(o.pricePerHour), className: "m ink-h" },
  {
    header: "",
    cell: (o) => <Link className="btn sm p" to={`/app/instances/new?offer=${encodeURIComponent(o.id)}`}>
        Create instance
      </Link>,
  },
];

/** The values the filter controls offer, taken from the unfiltered list so they stay put while filtering. */
function filterOptions(offers: readonly Offer[]) {
  const distinct = (values: Iterable<string>) => [...new Set(values)].filter(Boolean).sort();
  return {
    regions: distinct(offers.map((o) => o.region)),
    kinds: distinct(["none", ...offers.map((o) => o.shape?.acceleratorKind ?? "")]),
    models: distinct(offers.map((o) => o.shape?.acceleratorModel ?? "")),
    archs: distinct(offers.flatMap((o) => o.archs)),
  };
}

function filterFromForm(data: FormData): OffersFilter {
  const text = (name: string) => String(data.get(name) ?? "").trim();
  const filter: OffersFilter = {};
  if (text("region")) filter.region = text("region");
  if (text("accelerator_kind")) filter.acceleratorKind = text("accelerator_kind");
  if (text("accelerator_model")) filter.acceleratorModel = text("accelerator_model");
  if (text("arch")) filter.arch = text("arch");
  if (Number(text("capacity")) > 0) filter.capacityClass = Number(text("capacity")) as CapacityClass;
  if (Number(text("min_vcpu")) > 0) filter.minVcpu = Math.floor(Number(text("min_vcpu")));
  if (Number(text("min_ram_gib")) > 0) filter.minRamMib = Math.round(Number(text("min_ram_gib")) * 1024);
  return filter;
}

function Options({ values }: { values: readonly string[] }) {
  return (
    <>
      <option value="">any</option>
      {values.map((v) => (
        <option key={v} value={v}>
          {v}
        </option>
      ))}
    </>
  );
}

/** `/app/offers`: what can be bought, with filters bound to `ListOffers`. */
export function OffersPage() {
  const [filter, setFilter] = useState<OffersFilter>({});
  const [formKey, setFormKey] = useState(0);
  const everything = useOffers({});
  const offers = useOffers(filter);
  const options = useMemo(() => filterOptions(everything.data?.offers ?? []), [everything.data]);
  const filtered = Object.keys(filter).length > 0;

  return (
    <section>
      <PageTitle>Offers</PageTitle>
      <Form inline key={formKey} submitLabel="Apply filters" onSubmit={(data) => setFilter(filterFromForm(data))}>
        <Field as="select" label="Region" name="region" defaultValue={filter.region ?? ""}>
          <Options values={options.regions} />
        </Field>
        <Field as="select" label="Accelerator" name="accelerator_kind" defaultValue={filter.acceleratorKind ?? ""}>
          <Options values={options.kinds} />
        </Field>
        <Field as="select" label="Accelerator model" name="accelerator_model" defaultValue={filter.acceleratorModel ?? ""}>
          <Options values={options.models} />
        </Field>
        <Field as="select" label="Architecture" name="arch" defaultValue={filter.arch ?? ""}>
          <Options values={options.archs} />
        </Field>
        <Field as="select" label="Capacity" name="capacity" defaultValue={String(filter.capacityClass ?? 0)}>
          <option value="0">any</option>
          <option value={CapacityClass.DEDICATED}>{capacityLabel(CapacityClass.DEDICATED)}</option>
          <option value={CapacityClass.PREEMPTIBLE}>{capacityLabel(CapacityClass.PREEMPTIBLE)}</option>
        </Field>
        <Field label="Min vCPU" name="min_vcpu" type="number" min={0} step={1} defaultValue={filter.minVcpu ?? ""} />
        <Field
          label="Min RAM (GiB)"
          name="min_ram_gib"
          type="number"
          min={0}
          step="any"
          defaultValue={filter.minRamMib ? filter.minRamMib / 1024 : ""}
        />
      </Form>
      {filtered && (
        <button
          type="button"
          className="sm"
          style={{ width: "fit-content" }}
          onClick={() => {
            setFilter({});
            setFormKey((k) => k + 1);
          }}
        >
          Clear filters
        </button>
      )}
      <DataTable
        caption="Offers for sale"
        columns={columns}
        rows={offers.data?.offers}
        rowKey={(o) => o.id}
        loading={offers.isPending}
        error={offers.error}
        onRetry={() => void offers.refetch()}
        empty={filtered ? "No offers match these filters." : "No offers are for sale yet."}
      />
    </section>
  );
}
