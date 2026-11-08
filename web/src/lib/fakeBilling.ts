import { useSyncExternalStore } from "react";

/*
 * Fake billing for the demo. The backend has no ledger (ADR-0023: no money in
 * the demo), so this is generated in the browser: thirty days of top-ups,
 * usage charges and one refund that end at a sensible balance. A top-up made
 * in the UI is added to it and kept in localStorage. Nothing here is real.
 */

export type LedgerKind = "topup" | "bonus" | "usage" | "refund";

export interface LedgerEntry {
  id: string;
  /** Milliseconds since the epoch. */
  at: number;
  kind: LedgerKind;
  description: string;
  /** Instance name or payment method. */
  ref: string;
  /** Signed, in cents: credits are positive, charges negative. */
  amountMinor: number;
  /** The balance right after this entry. */
  balanceAfter: number;
}

export const CURRENCY = "EUR";
const DAY = 86_400_000;
const STORAGE_KEY = "kdk-fake-topups";

interface Seed {
  daysAgo: number;
  /** Hour of the day, to order same-day entries. */
  hour: number;
  kind: LedgerKind;
  description: string;
  ref: string;
  amountMinor: number;
}

/** The 30-day story: a CPU instance running all along, GPU sessions now and then, three top-ups. */
function seeds(): Seed[] {
  const out: Seed[] = [
    { daysAgo: 29, hour: 9, kind: "bonus", description: "Welcome credit", ref: "promo WELCOME5", amountMinor: 500 },
    { daysAgo: 29, hour: 9, kind: "topup", description: "Card top-up", ref: "Visa ••4242", amountMinor: 2500 },
    { daysAgo: 14, hour: 8, kind: "topup", description: "Card top-up", ref: "Visa ••4242", amountMinor: 3000 },
    { daysAgo: 10, hour: 15, kind: "refund", description: "Refund: node unreachable", ref: "training", amountMinor: 120 },
    { daysAgo: 4, hour: 8, kind: "topup", description: "Card top-up", ref: "Visa ••4242", amountMinor: 2500 },
  ];
  // cpu-2x2 at €0.05/h, 24 h a day since day 27.
  for (let d = 27; d >= 0; d--) {
    out.push({ daysAgo: d, hour: 23, kind: "usage", description: "cpu-2x2 · 24 h", ref: "web", amountMinor: -120 });
  }
  // gpu-gtx1060-1x at €0.40/h, in sessions of a few hours.
  const sessions: Array<[number, number]> = [[26, 4], [24, 6], [21, 3], [19, 8], [17, 5], [13, 6], [11, 4], [9, 7], [6, 5], [3, 4], [1, 3]];
  for (const [d, h] of sessions) {
    out.push({ daysAgo: d, hour: 20, kind: "usage", description: `gpu-gtx1060-1x · ${h} h`, ref: "training", amountMinor: -h * 40 });
  }
  return out;
}

function build(now: number, extra: Array<{ at: number; amountMinor: number }>): LedgerEntry[] {
  const midnight = new Date(now);
  midnight.setHours(0, 0, 0, 0);
  const base = midnight.getTime();
  const all = seeds()
    .map((s, i) => ({ id: `seed-${i}`, at: base - s.daysAgo * DAY + s.hour * 3_600_000, kind: s.kind, description: s.description, ref: s.ref, amountMinor: s.amountMinor }))
    .concat(extra.map((e, i) => ({ id: `topup-${i}`, at: e.at, kind: "topup" as const, description: "Card top-up", ref: "Visa ••4242", amountMinor: e.amountMinor })))
    .sort((a, b) => a.at - b.at || a.id.localeCompare(b.id));
  let balance = 0;
  return all.map((e) => {
    balance += e.amountMinor;
    return { ...e, balanceAfter: balance };
  });
}

function loadExtra(): Array<{ at: number; amountMinor: number }> {
  try {
    const raw = globalThis.localStorage?.getItem(STORAGE_KEY);
    const parsed: unknown = raw ? JSON.parse(raw) : [];
    return Array.isArray(parsed) ? parsed.filter((e): e is { at: number; amountMinor: number } => typeof e?.at === "number" && typeof e?.amountMinor === "number") : [];
  } catch {
    return [];
  }
}

let extra = loadExtra();
let snapshot = build(Date.now(), extra);
const listeners = new Set<() => void>();

function publish() {
  snapshot = build(Date.now(), extra);
  try {
    globalThis.localStorage?.setItem(STORAGE_KEY, JSON.stringify(extra));
  } catch {
    /* storage blocked: the top-up just lasts until the page reloads */
  }
  for (const l of listeners) l();
}

/** Adds a pretend card top-up of `amountMinor` cents. */
export function addFakeTopUp(amountMinor: number) {
  if (!Number.isFinite(amountMinor) || amountMinor <= 0) return;
  extra = [...extra, { at: Date.now(), amountMinor: Math.round(amountMinor) }];
  publish();
}

/** Back to the generated history (tests, and a way to start over). */
export function resetFakeBilling() {
  extra = [];
  publish();
}

const subscribe = (l: () => void) => {
  listeners.add(l);
  return () => listeners.delete(l);
};

export interface BillingSummary {
  entries: readonly LedgerEntry[];
  balance: number;
  /** Charges in the last 30 days, as a positive number. */
  spent30: number;
  /** Average daily charges over the last 7 days. */
  burnPerDay: number;
  /** Days the balance lasts at that rate; undefined when nothing is being spent. */
  runwayDays?: number;
}

export function summarize(entries: readonly LedgerEntry[], now = Date.now()): BillingSummary {
  const balance = entries.at(-1)?.balanceAfter ?? 0;
  const charges = entries.filter((e) => e.kind === "usage");
  const spent30 = -charges.filter((e) => e.at > now - 30 * DAY).reduce((s, e) => s + e.amountMinor, 0);
  const burnPerDay = Math.round(-charges.filter((e) => e.at > now - 7 * DAY).reduce((s, e) => s + e.amountMinor, 0) / 7);
  return { entries, balance, spent30, burnPerDay, runwayDays: burnPerDay > 0 ? balance / burnPerDay : undefined };
}

/** The current entries, for tests and non-React callers. */
export const __entries = () => snapshot;

export function useFakeBilling(): BillingSummary {
  const entries = useSyncExternalStore(subscribe, () => snapshot);
  return summarize(entries);
}

/** "€30.60"; a sign shows "+€25.00" / "−€1.20". */
export function eur(minor: number, signed = false): string {
  const text = new Intl.NumberFormat("en", { style: "currency", currency: CURRENCY }).format(Math.abs(minor) / 100);
  if (!signed) return minor < 0 ? `−${text}` : text;
  return `${minor < 0 ? "−" : "+"}${text}`;
}

/** Charges per local day, oldest first, for the last `days` days (days with none are zero). */
export function dailySpend(entries: readonly LedgerEntry[], days = 30, now = Date.now()): Array<{ t: number; v: number }> {
  const midnight = new Date(now);
  midnight.setHours(0, 0, 0, 0);
  const start = midnight.getTime() - (days - 1) * DAY;
  const out = Array.from({ length: days }, (_, i) => ({ t: start + i * DAY, v: 0 }));
  for (const e of entries) {
    if (e.kind !== "usage" || e.at < start) continue;
    const i = Math.min(days - 1, Math.floor((e.at - start) / DAY));
    (out[i] as { v: number }).v += -e.amountMinor;
  }
  return out;
}

/** The balance at the end of each local day, oldest first. */
export function dailyBalance(entries: readonly LedgerEntry[], days = 30, now = Date.now()): Array<{ t: number; v: number }> {
  const midnight = new Date(now);
  midnight.setHours(0, 0, 0, 0);
  const start = midnight.getTime() - (days - 1) * DAY;
  return Array.from({ length: days }, (_, i) => {
    const end = start + (i + 1) * DAY;
    const last = [...entries].reverse().find((e) => e.at < end);
    return { t: start + i * DAY, v: last?.balanceAfter ?? 0 };
  });
}

export interface Invoice {
  id: string;
  month: string;
  totalMinor: number;
  status: "paid" | "open";
}

/** One invoice per calendar month that has charges; the current month is still open. */
export function invoices(entries: readonly LedgerEntry[], now = Date.now()): Invoice[] {
  const key = (t: number) => {
    const d = new Date(t);
    return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}`;
  };
  const totals = new Map<string, number>();
  for (const e of entries) if (e.kind === "usage") totals.set(key(e.at), (totals.get(key(e.at)) ?? 0) - e.amountMinor);
  return [...totals.entries()]
    .sort(([a], [b]) => (a < b ? 1 : -1))
    .map(([month, totalMinor]) => ({ id: `INV-${month}`, month, totalMinor, status: month === key(now) ? "open" : "paid" }));
}
