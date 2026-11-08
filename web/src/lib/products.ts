import type { Offer } from "@gen/kondukter/api/v1/catalog_pb";
import { capacityLabel } from "./format";

/** One line of the product catalog, from the demo catalog or from a live offer. */
export interface Product {
  id: string;
  /** Shape name, e.g. "gpu-gtx1060-1x". */
  name: string;
  /** The accelerator, if any: model, how many, and its memory when the name carries it. */
  gpu?: { model: string; count: number; vramGiB?: number };
  vcpu: number;
  ramMib: number;
  diskGib: number;
  archs: string[];
  region: string;
  capacity: string;
  /** Price per hour in minor units (cents); undefined while unpriced. */
  priceMinor?: number;
  currency: string;
  availability: "available";
  /** Who hosts it, when the source knows. */
  host?: string;
  /** The offer to create an instance from, for live products. */
  offerId?: string;
}

/**
 * The demo catalog as it is seeded (controlplane/catalog/demo.yaml). A guest
 * cannot call the API, so this is what a visitor sees; a signed-in customer
 * sees the live offers instead.
 */
export const demoProducts: readonly Product[] = [
  {
    id: "demo-cpu-2x2",
    name: "cpu-2x2",
    vcpu: 2,
    ramMib: 2048,
    diskGib: 20,
    archs: ["arm64", "amd64"],
    region: "eu-west",
    capacity: "dedicated",
    priceMinor: 5,
    currency: "EUR",
    availability: "available",
    host: "Partner datacenter · amd64 and arm64 nodes",
  },
  {
    id: "demo-gpu-gtx1060-1x",
    name: "gpu-gtx1060-1x",
    gpu: { model: "GeForce GTX 1060 6GB", count: 1, vramGiB: 6 },
    vcpu: 4,
    ramMib: 8192,
    diskGib: 40,
    archs: ["amd64"],
    region: "eu-west",
    capacity: "dedicated",
    priceMinor: 40,
    currency: "EUR",
    availability: "available",
    host: "Partner datacenter · Fedora GPU node",
  },
];

/** A live offer as a product line. */
export function productFromOffer(o: Offer): Product {
  const s = o.shape;
  const count = s?.acceleratorCount ?? 0;
  const model = s?.acceleratorModel || s?.acceleratorKind || "";
  const vram = /(\d+)\s*GB/i.exec(model)?.[1];
  return {
    id: o.id,
    name: s?.name ?? o.id,
    gpu: count > 0 ? { model, count, vramGiB: vram ? Number(vram) : undefined } : undefined,
    vcpu: s?.vcpu ?? 0,
    ramMib: Number(s?.ramMib ?? 0n),
    diskGib: Number(s?.diskGib ?? 0n),
    archs: [...o.archs],
    region: o.region,
    capacity: capacityLabel(o.capacityClass),
    priceMinor: o.pricePerHour ? Number(o.pricePerHour.amountMinor) : undefined,
    currency: o.pricePerHour?.currency || "EUR",
    availability: "available",
    offerId: o.id,
  };
}

export type PriceUnit = "hour" | "day" | "month";
const HOURS: Record<PriceUnit, number> = { hour: 1, day: 24, month: 730 };

/** "€0.40" for a price in minor units; the unit scales an hourly price to a day or a month. */
export function formatPrice(minorPerHour: number | undefined, currency: string, unit: PriceUnit = "hour"): string {
  if (minorPerHour === undefined) return "—";
  const major = (minorPerHour * HOURS[unit]) / 100;
  try {
    return new Intl.NumberFormat("en", { style: "currency", currency }).format(major);
  } catch {
    return `${major.toFixed(2)} ${currency}`;
  }
}

export const unitSuffix: Record<PriceUnit, string> = { hour: "/hr", day: "/day", month: "/mo" };
