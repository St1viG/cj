import { Code, ConnectError } from "@connectrpc/connect";
import { CapacityClass } from "@gen/kondukter/api/v1/types_pb";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { fakeApi } from "../../test/fakeApi";
import { renderApp } from "../../test/fakeServer";

const row = (name: RegExp) => screen.getByRole("row", { name });
const location = () => screen.getByTestId("location").textContent;

async function apply(change: (form: HTMLElement) => void | Promise<void>) {
  await screen.findByRole("option", { name: "nvidia" }); // the filter choices come from the full list
  await change(screen.getByRole("button", { name: "Apply filters" }).closest("form") as HTMLElement);
  fireEvent.click(screen.getByRole("button", { name: "Apply filters" }));
}

describe("/app/offers", () => {
  it("lists what is for sale, with sizes, accelerators and prices", async () => {
    renderApp("/app/offers", fakeApi());

    const cpu = within(await waitFor(() => row(/cpu-2x2/)));
    expect(cpu.getByText("none")).toBeTruthy();
    expect(cpu.getByText("2 GiB")).toBeTruthy();
    expect(cpu.getByText("20 GiB")).toBeTruthy();
    expect(cpu.getByText("amd64, arm64")).toBeTruthy();
    expect(cpu.getByText("dedicated")).toBeTruthy();
    expect(cpu.getByText("€0.05")).toBeTruthy();

    const gpu = within(row(/gpu-gtx1060-1x/));
    expect(gpu.getByText("GeForce GTX 1060 6GB × 1")).toBeTruthy();
    expect(gpu.getByText("16 GiB")).toBeTruthy();
    expect(gpu.getByText("€0.40")).toBeTruthy();
  });

  it("links each offer to the create form with the offer chosen", async () => {
    renderApp("/app/offers", fakeApi());
    const link = within(await waitFor(() => row(/gpu-gtx1060-1x/))).getByRole("link", { name: "Create instance" });
    expect(link.getAttribute("href")).toBe("/app/instances/new?offer=offer-gpu");
    fireEvent.click(link);
    await waitFor(() => expect(location()).toBe("/app/instances/new?offer=offer-gpu"));
    expect(await screen.findByRole("heading", { name: "New instance" })).toBeTruthy();
  });

  it("sends the filters to ListOffers and narrows the table", async () => {
    const api = fakeApi();
    renderApp("/app/offers", api);
    await waitFor(() => row(/cpu-2x2/));

    await apply((form) => {
      fireEvent.change(within(form).getByLabelText("Accelerator"), { target: { value: "nvidia" } });
    });

    await waitFor(() => expect(screen.queryByRole("row", { name: /cpu-2x2/ })).toBeNull());
    expect(await screen.findByRole("row", { name: /gpu-gtx1060-1x/ })).toBeTruthy();
    // Not `.at(-1)`: the unfiltered list behind the dropdowns polls too.
    expect(api.calls.listOffers.some((r) => r.acceleratorKind === "nvidia")).toBe(true);
  });

  it("filters by size, architecture and capacity", async () => {
    const api = fakeApi();
    renderApp("/app/offers", api);
    await waitFor(() => row(/cpu-2x2/));

    await apply((form) => {
      fireEvent.change(within(form).getByLabelText("Architecture"), { target: { value: "arm64" } });
      fireEvent.change(within(form).getByLabelText("Capacity"), { target: { value: String(CapacityClass.DEDICATED) } });
      fireEvent.change(within(form).getByLabelText("Min vCPU"), { target: { value: "2" } });
      fireEvent.change(within(form).getByLabelText("Min RAM (GiB)"), { target: { value: "1.5" } });
    });

    await waitFor(() => expect(api.calls.listOffers.some((r) => r.arch === "arm64")).toBe(true));
    const sent = api.calls.listOffers.find((r) => r.arch === "arm64");
    expect(sent?.capacityClass).toBe(CapacityClass.DEDICATED);
    expect(sent?.minVcpu).toBe(2);
    expect(sent?.minRamMib).toBe(1536n);
    await waitFor(() => expect(screen.queryByRole("row", { name: /gpu-gtx1060-1x/ })).toBeNull());
    expect(row(/cpu-2x2/)).toBeTruthy();
  });

  it("says when nothing matches and clears the filters", async () => {
    const api = fakeApi();
    renderApp("/app/offers", api);
    await waitFor(() => row(/cpu-2x2/));

    await apply((form) => {
      fireEvent.change(within(form).getByLabelText("Min vCPU"), { target: { value: "64" } });
    });
    expect(await screen.findByText("No offers match these filters.")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Clear filters" }));
    await waitFor(() => row(/cpu-2x2/));
    // Cleared: the form is empty again, and the table is back to the full list.
    expect((screen.getByLabelText("Min vCPU") as HTMLInputElement).value).toBe("");
    expect(screen.queryByText("No offers match these filters.")).toBeNull();
    expect(screen.queryByRole("button", { name: "Clear filters" })).toBeNull();
  });

  it("says when nothing is for sale", async () => {
    renderApp("/app/offers", fakeApi({ offers: [] }));
    expect(await screen.findByText("No offers are for sale yet.")).toBeTruthy();
  });

  it("shows a failure with a retry that works", async () => {
    const api = fakeApi();
    api.fail.listOffers = new ConnectError("catalog unavailable", Code.Unavailable);
    renderApp("/app/offers", api);
    expect((await screen.findAllByText("catalog unavailable")).length).toBeGreaterThan(0);

    delete api.fail.listOffers;
    fireEvent.click(screen.getAllByRole("button", { name: "Retry" })[0] as HTMLElement);
    await waitFor(() => row(/cpu-2x2/));
  });
});
