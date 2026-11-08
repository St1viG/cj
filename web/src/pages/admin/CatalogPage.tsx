import type { Image, Offer, Region, Shape } from "@gen/kondukter/api/v1/catalog_pb";
import { useQuery } from "@tanstack/react-query";
import { useApi } from "../../api/ApiProvider";
import { keys } from "../../api/keys";
import { type Column, DataTable } from "../../components/DataTable";
import { QueryState } from "../../components/State";
import { StatusChip } from "../../components/Status";
import { capacityLabel, formatAccelerator, formatGiB, formatMiB, formatMoney } from "../../lib/format";
import { PageTitle } from "../../components/PageTitle";

const offerColumns: Column<Offer>[] = [
  { header: "Shape", cell: (o) => o.shape?.name ?? "—" },
  { header: "Accelerators", cell: (o) => formatAccelerator(o.shape) },
  { header: "Region", cell: (o) => o.region },
  { header: "Capacity class", cell: (o) => capacityLabel(o.capacityClass) },
  { header: "Price per hour", cell: (o) => formatMoney(o.pricePerHour), className: "m ink-h" },
  { header: "For sale", cell: (o) => <StatusChip kind={o.active ? "run" : "end"}>{o.active ? "yes" : "no"}</StatusChip> },
  { header: "Architectures with an image", cell: (o) => o.archs.join(", ") || "none" },
  { header: "Id", cell: (o) => <code>{o.id}</code> },
];

const shapeColumns: Column<Shape>[] = [
  { header: "Name", cell: (s) => s.name },
  { header: "vCPU", cell: (s) => s.vcpu },
  { header: "RAM", cell: (s) => formatMiB(s.ramMib) },
  { header: "Disk", cell: (s) => formatGiB(s.diskGib) },
  { header: "Accelerators", cell: (s) => formatAccelerator(s) },
];

const imageColumns: Column<Image>[] = [
  { header: "Name", cell: (i) => i.name },
  { header: "Version", cell: (i) => i.version },
  { header: "Arch", cell: (i) => i.arch },
  { header: "OS", cell: (i) => i.os },
  { header: "Accelerator stack", cell: (i) => i.accelStack || "none" },
  { header: "Accelerator models", cell: (i) => i.compatibleAcceleratorModels.join(", ") || "—" },
  { header: "Status", cell: (i) => <StatusChip kind={i.status === "active" ? "run" : "end"}>{i.status}</StatusChip> },
  { header: "Size", cell: (i) => (i.sizeBytes > 0n ? formatMiB(Math.round(Number(i.sizeBytes) / 2 ** 20)) : "—") },
  { header: "Digest", cell: (i) => <span title={i.digest}>{i.digest ? `${i.digest.slice(0, 19)}…` : "—"}</span> },
];

const regionColumns: Column<Region>[] = [
  { header: "Name", cell: (r) => r.name },
  { header: "Description", cell: (r) => r.description || "—" },
];

/** `/admin/catalog`: offers, shapes, images and regions, read-only, inactive and deprecated entries included. */
export function CatalogPage() {
  const { admin } = useApi();
  const catalog = useQuery({ queryKey: keys.admin.catalog, queryFn: () => admin.listCatalog({}) });
  return (
    <section>
      <PageTitle>Catalog</PageTitle>
      <p>Read-only: the catalog is seeded, not edited here. Offers no longer for sale and deprecated images are listed too.</p>
      <QueryState query={catalog}>
        {(data) => (
          <>
            <h2>Offers ({data.offers.length})</h2>
            <DataTable columns={offerColumns} rows={data.offers} rowKey={(o) => o.id} empty="No offers." />
            <h2>Shapes ({data.shapes.length})</h2>
            <DataTable columns={shapeColumns} rows={data.shapes} rowKey={(s) => s.id} empty="No shapes." />
            <h2>Images ({data.images.length})</h2>
            <DataTable columns={imageColumns} rows={data.images} rowKey={(i) => i.id} empty="No images." />
            <h2>Regions ({data.regions.length})</h2>
            <DataTable columns={regionColumns} rows={data.regions} rowKey={(r) => r.name} empty="No regions." />
          </>
        )}
      </QueryState>
    </section>
  );
}
