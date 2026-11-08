/*
 * Fake partner earnings for the demo: thirty days of rental revenue for the
 * demo site's four nodes, an illustrative revenue share, and payout history.
 * Generated in the browser with a fixed seed, so it looks the same every time.
 * The commercial model is undecided (D-03); none of this is real.
 */

export const REVENUE_SHARE = 0.7;
const DAY = 86_400_000;

function rng(seed: number) {
  let a = seed;
  return () => {
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export interface NodeEarnings {
  node: string;
  kind: string;
  /** Hours rented over the 30 days, and what that earned the partner, in cents. */
  hours: number;
  utilization: number;
  earnedMinor: number;
  daily: number[];
}

/** Hourly price in cents and a typical share of the day each node is rented. */
const nodes = [
  { node: "mac-a", kind: "arm64 · CPU", price: 5, load: 0.55 },
  { node: "mac-b", kind: "arm64 · CPU", price: 5, load: 0.4 },
  { node: "fed-d", kind: "amd64 · GTX 1060", price: 40, load: 0.35 },
  { node: "fed-l", kind: "amd64 · CPU", price: 5, load: 0.15 },
];

export function earningsByNode(): NodeEarnings[] {
  return nodes.map((n, idx) => {
    const r = rng(11 + idx * 7);
    const daily = Array.from({ length: 30 }, () => {
      const hours = Math.max(0, Math.min(24, 24 * n.load * (0.4 + r() * 1.2)));
      return hours;
    });
    const hours = daily.reduce((s, h) => s + h, 0);
    return {
      node: n.node,
      kind: n.kind,
      hours: Math.round(hours),
      utilization: Math.round((hours / (30 * 24)) * 100),
      earnedMinor: Math.round(hours * n.price * REVENUE_SHARE),
      daily: daily.map((h) => Math.round(h * n.price * REVENUE_SHARE)),
    };
  });
}

/** Total earnings per local day across the nodes, oldest first. */
export function dailyEarnings(now = Date.now()): Array<{ t: number; v: number }> {
  const midnight = new Date(now);
  midnight.setHours(0, 0, 0, 0);
  const start = midnight.getTime() - 29 * DAY;
  const per = earningsByNode();
  return Array.from({ length: 30 }, (_, i) => ({ t: start + i * DAY, v: per.reduce((s, n) => s + (n.daily[i] ?? 0), 0) }));
}

export interface Payout {
  id: string;
  date: number;
  amountMinor: number;
  method: string;
  status: "paid" | "scheduled";
}

/** Two paid payouts, and the next one scheduled at the end of the month. */
export function payouts(now = Date.now()): Payout[] {
  const d = new Date(now);
  const at = (monthOffset: number, day: number) => new Date(d.getFullYear(), d.getMonth() + monthOffset, day, 12).getTime();
  const id = (offset: number) => {
    const x = new Date(d.getFullYear(), d.getMonth() + offset, 1);
    return `PAYOUT-${x.getFullYear()}-${String(x.getMonth() + 1).padStart(2, "0")}`;
  };
  const total = earningsByNode().reduce((s, n) => s + n.earnedMinor, 0);
  return [
    { id: id(0), date: at(1, 1), amountMinor: total, method: "Bank transfer ••0042", status: "scheduled" },
    { id: id(-1), date: at(0, 1), amountMinor: Math.round(total * 0.83), method: "Bank transfer ••0042", status: "paid" },
    { id: id(-2), date: at(-1, 1), amountMinor: Math.round(total * 0.61), method: "Bank transfer ••0042", status: "paid" },
  ];
}
