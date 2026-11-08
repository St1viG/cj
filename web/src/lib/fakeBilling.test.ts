import { beforeEach, describe, expect, it } from "vitest";
import { __entries, addFakeTopUp, dailyBalance, dailySpend, invoices, resetFakeBilling, summarize } from "./fakeBilling";
import { dailyEarnings, earningsByNode, payouts } from "./fakeEarnings";

beforeEach(() => {
  try {
    localStorage.clear();
  } catch {
    /* no storage */
  }
  resetFakeBilling();
});

const entries = () => __entries();

describe("fake billing", () => {
  it("keeps the balance positive for the whole history and ends on a sensible figure", () => {
    const e = entries();
    expect(e.length).toBeGreaterThan(30);
    expect(Math.min(...e.map((x) => x.balanceAfter))).toBeGreaterThanOrEqual(0);
    const s = summarize(e);
    expect(s.balance).toBeGreaterThan(1000);
    expect(s.spent30).toBeGreaterThan(0);
    expect(s.burnPerDay).toBeGreaterThan(0);
    expect(s.runwayDays).toBeGreaterThan(1);
  });

  it("builds one point per day for the charts, and the balance chart ends on the balance", () => {
    const e = entries();
    expect(dailySpend(e)).toHaveLength(30);
    const series = dailyBalance(e);
    expect(series).toHaveLength(30);
    expect(series.at(-1)?.v).toBe(summarize(e).balance);
  });

  it("derives an invoice per month with charges, the current one open", () => {
    const inv = invoices(entries());
    expect(inv.length).toBeGreaterThanOrEqual(1);
    expect(inv[0]?.status).toBe("open");
    expect(inv.reduce((s, i) => s + i.totalMinor, 0)).toBe(-entries().filter((x) => x.kind === "usage").reduce((s, x) => s + x.amountMinor, 0));
  });

  it("adds a top-up to the balance and keeps it in storage", () => {
    const before = summarize(entries()).balance;
    addFakeTopUp(2500);
    expect(summarize(entries()).balance).toBe(before + 2500);
    expect(localStorage.getItem("kdk-fake-topups")).toContain("2500");
  });
});

describe("fake earnings", () => {
  it("is the same every time and adds up", () => {
    const a = earningsByNode();
    expect(earningsByNode()).toEqual(a);
    const daily = dailyEarnings();
    expect(daily).toHaveLength(30);
    // Daily figures are rounded per node, so allow a few cents of drift.
    expect(Math.abs(daily.reduce((s, d) => s + d.v, 0) - a.reduce((s, n) => s + n.earnedMinor, 0))).toBeLessThan(150);
    expect(payouts().map((p) => p.status)).toEqual(["scheduled", "paid", "paid"]);
  });
});
