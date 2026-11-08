import { create } from "@bufbuild/protobuf";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import { InstancePhase, MoneySchema, UsageSchema } from "@gen/kondukter/api/v1/types_pb";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { fakeApi, instance } from "../../test/fakeApi";
import { renderApp } from "../../test/fakeServer";

const usage = (minutes: number, cents: number) =>
  create(UsageSchema, {
    runningSeconds: BigInt(minutes * 60),
    runningMinutes: BigInt(minutes),
    estimatedCost: create(MoneySchema, { amountMinor: BigInt(cents), currency: "EUR" }),
  });

const two = () =>
  fakeApi({
    instances: [
      instance("aaaaaaaa-0000-0000-0000-000000000001", InstancePhase.RUNNING, { name: "web-1", usage: usage(120, 10) }),
      instance("bbbbbbbb-0000-0000-0000-000000000002", InstancePhase.TERMINATED, { usage: usage(30, 3) }),
    ],
  });

describe("/app/usage", () => {
  it("shows running time and estimated cost per instance, with totals", async () => {
    const api = two();
    renderApp("/app/usage", api);

    const web = within(await screen.findByRole("row", { name: /web-1/ }));
    expect(web.getByText("running")).toBeTruthy();
    expect(web.getByText("cpu-2x2")).toBeTruthy();
    expect(web.getByText("€0.05")).toBeTruthy(); // price per hour
    expect(web.getByText("120 min")).toBeTruthy();
    expect(web.getByText("€0.10")).toBeTruthy();
    expect(web.getByRole("link", { name: "web-1" }).getAttribute("href")).toBe("/app/instances/aaaaaaaa-0000-0000-0000-000000000001");

    // An unnamed instance is shown by its short id.
    expect(within(screen.getByRole("row", { name: /bbbbbbbb/ })).getByText("terminated")).toBeTruthy();

    const totals = screen.getByText("Total running time").parentElement as HTMLElement;
    expect(within(totals).getByText("150 min")).toBeTruthy();
    expect(within(totals).getByText("€0.13")).toBeTruthy();

    // No period chosen: the whole history.
    expect(api.calls.listUsage[0]?.periodStart).toBeUndefined();
    expect(api.calls.listUsage[0]?.periodEnd).toBeUndefined();
    expect(screen.getByRole("table", { name: "Usage, all time" })).toBeTruthy();
  });

  it("asks for whole days in the viewer's time zone, with the last day included", async () => {
    const api = two();
    renderApp("/app/usage", api);
    await screen.findByRole("row", { name: /web-1/ });

    fireEvent.change(screen.getByLabelText("From"), { target: { value: "2026-11-01" } });
    fireEvent.change(screen.getByLabelText("To (included)"), { target: { value: "2026-11-07" } });
    fireEvent.click(screen.getByRole("button", { name: "Show period" }));

    await waitFor(() => expect(api.calls.listUsage.length).toBeGreaterThan(1));
    const sent = api.calls.listUsage.at(-1);
    // From the start of the 1st to the start of the 8th: all of the 7th is in.
    expect(timestampDate(sent?.periodStart ?? create(UsageSchema) as never).getTime()).toBe(new Date(2026, 10, 1).getTime());
    expect(timestampDate(sent?.periodEnd ?? create(UsageSchema) as never).getTime()).toBe(new Date(2026, 10, 8).getTime());
    expect(await screen.findByRole("table", { name: "Usage 2026-11-01 to 2026-11-07" })).toBeTruthy();
  });

  it("accepts a period with only one end", async () => {
    const api = two();
    renderApp("/app/usage", api);
    await screen.findByRole("row", { name: /web-1/ });

    fireEvent.change(screen.getByLabelText("From"), { target: { value: "2026-11-01" } });
    fireEvent.click(screen.getByRole("button", { name: "Show period" }));

    await waitFor(() => expect(api.calls.listUsage.at(-1)?.periodStart).toBeDefined());
    expect(api.calls.listUsage.at(-1)?.periodEnd).toBeUndefined();
  });

  it("says when nothing ran", async () => {
    renderApp("/app/usage", fakeApi());
    expect(await screen.findByText("No instances ran in this period.")).toBeTruthy();
    expect(screen.queryByText("Total running time")).toBeNull();
  });
});
