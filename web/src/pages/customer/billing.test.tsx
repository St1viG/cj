import { fireEvent, screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import { fakeApi } from "../../test/fakeApi";
import { fakeFleet } from "../../test/fakeFleet";
import { renderApp } from "../../test/fakeServer";
import { __entries, eur, resetFakeBilling, summarize } from "../../lib/fakeBilling";

beforeEach(() => {
  try {
    localStorage.clear();
  } catch {
    /* no storage */
  }
  resetFakeBilling();
});

describe("/app/billing", () => {
  it("shows the balance, its history and the invoices", async () => {
    renderApp("/app/billing", fakeApi());
    expect(await screen.findByRole("heading", { name: "Billing" })).toBeTruthy();
    const balance = eur(summarize(__entries()).balance);
    const tile = screen.getAllByText(balance).find((el) => el.closest(".stat"));
    expect(tile).toBeTruthy();
    expect(screen.getByRole("table", { name: "Balance history" })).toBeTruthy();
    expect(screen.getByRole("table", { name: "Invoices" })).toBeTruthy();
    expect(screen.getByRole("img", { name: /^Balance, 30 days/ })).toBeTruthy();
    expect(screen.getByRole("img", { name: /^Daily spend, 30 days/ })).toBeTruthy();
  });

  it("shows the balance in the navigation bar and moves it with a top-up", async () => {
    renderApp("/app/billing", fakeApi());
    await screen.findByRole("heading", { name: "Billing" });
    const nav = within(screen.getByRole("navigation", { name: "Main" }).closest("header") as HTMLElement);
    const before = summarize(__entries()).balance;
    expect(nav.getByText(eur(before))).toBeTruthy();

    fireEvent.change(screen.getByLabelText("Amount"), { target: { value: "50" } });
    fireEvent.click(screen.getByRole("button", { name: "Add credit" }));
    expect(await screen.findByText(/Added €50\.00/)).toBeTruthy();
    expect(nav.getByText(eur(before + 5000))).toBeTruthy();
  });

  it("filters the history to usage and back", async () => {
    renderApp("/app/billing", fakeApi());
    await screen.findByRole("heading", { name: "Billing" });
    const table = () => screen.getByRole("table", { name: "Balance history" });
    expect(within(table()).getAllByText("top-up").length + within(table()).queryAllByText("usage").length).toBeGreaterThan(0);
    fireEvent.click(screen.getByRole("tab", { name: "Usage" }));
    expect(within(table()).queryByText("top-up")).toBeNull();
    expect(within(table()).getAllByText("usage").length).toBeGreaterThan(0);
    fireEvent.click(screen.getByRole("tab", { name: "Credits" }));
    expect(within(table()).queryByText("usage")).toBeNull();
  });
});

describe("/partner/earnings", () => {
  it("shows earnings with a chart, nodes and payouts", async () => {
    renderApp("/partner/earnings", fakeFleet({ as: "partner" }));
    expect(await screen.findByRole("heading", { name: "Earnings" })).toBeTruthy();
    expect(screen.getByRole("img", { name: /^Daily earnings, 30 days/ })).toBeTruthy();
    const nodes = screen.getByRole("table", { name: /Earnings by node/ });
    expect(within(nodes).getByText("fed-d")).toBeTruthy();
    const pay = screen.getByRole("table", { name: "Payouts" });
    expect(within(pay).getByText("scheduled")).toBeTruthy();
    expect(within(pay).getAllByText("paid")).toHaveLength(2);
  });
});
