import type { Timestamp } from "@bufbuild/protobuf/wkt";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import type { Shape } from "@gen/kondukter/api/v1/catalog_pb";
import { CapacityClass, InstancePhase, type Money, Protocol } from "@gen/kondukter/api/v1/types_pb";

const dash = "—";

function fractionDigits(currency: string): number {
  try {
    return new Intl.NumberFormat("en", { style: "currency", currency }).resolvedOptions().maximumFractionDigits ?? 2;
  } catch {
    return 2;
  }
}

/** `Money` is integer minor units (cents): 15 EUR cents is "€0.15". */
export function formatMoney(m: Money | undefined, locale?: string): string {
  if (!m) return dash;
  const digits = fractionDigits(m.currency);
  const major = Number(m.amountMinor) / 10 ** digits;
  try {
    return new Intl.NumberFormat(locale, { style: "currency", currency: m.currency }).format(major);
  } catch {
    return `${major.toFixed(digits)} ${m.currency}`;
  }
}

/** 8192 MiB is "8 GiB"; 512 is "512 MiB"; 1536 is "1.5 GiB". */
export function formatMiB(mib: bigint | number): string {
  const n = Number(mib);
  if (n < 1024) return `${n} MiB`;
  return `${Number.isInteger(n / 1024) ? n / 1024 : (n / 1024).toFixed(1)} GiB`;
}

export function formatGiB(gib: bigint | number): string {
  return `${Number(gib)} GiB`;
}

/** "none" for a CPU shape, else "GeForce GTX 1060 6GB × 1". */
export function formatAccelerator(shape: Shape | undefined): string {
  if (!shape || shape.acceleratorCount === 0) return "none";
  return `${shape.acceleratorModel || shape.acceleratorKind} × ${shape.acceleratorCount}`;
}

export function formatMinutes(minutes: bigint | number | undefined): string {
  return minutes === undefined ? dash : `${Number(minutes)} min`;
}

/** Local date and time, to the second. */
export function formatTimestamp(ts: Timestamp | undefined, locale?: string): string {
  if (!ts) return dash;
  return timestampDate(ts).toLocaleString(locale, { dateStyle: "medium", timeStyle: "medium" });
}

/** The phase as the customer reads it: `RUNNING` is "running". */
export function phaseLabel(phase: InstancePhase): string {
  return (InstancePhase[phase] ?? "unknown").toLowerCase();
}

export function capacityLabel(c: CapacityClass): string {
  return (CapacityClass[c] ?? "unknown").toLowerCase();
}

export function protocolLabel(p: Protocol): string {
  return (Protocol[p] ?? "tcp").toLowerCase();
}

/** The first 8 characters of an id, enough to tell instances apart in a table. */
export function shortId(id: string): string {
  return id.slice(0, 8);
}

/** Phases a person is waiting on: the instance is between two stable states. */
export function isTransitional(phase: InstancePhase): boolean {
  return (
    phase === InstancePhase.PENDING ||
    phase === InstancePhase.SCHEDULED ||
    phase === InstancePhase.PROVISIONING ||
    phase === InstancePhase.TERMINATING
  );
}

/** Whether TerminateInstance still means something for an instance in `phase`. */
export function canTerminate(phase: InstancePhase): boolean {
  return phase !== InstancePhase.TERMINATING && phase !== InstancePhase.TERMINATED && phase !== InstancePhase.FAILED && phase !== InstancePhase.LOST;
}
