import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { InstancePhase, MoneySchema, UsageSchema } from "@gen/kondukter/api/v1/types_pb";
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { fakeApi, instance, runningConnection } from "../../test/fakeApi";
import { renderApp } from "../../test/fakeServer";
import { MAX_LIST_STREAMS } from "./InstancesPage";

const row = (name: RegExp) => screen.getByRole("row", { name });
const findRow = (name: RegExp) => screen.findByRole("row", { name });
const location = () => screen.getByTestId("location").textContent;

const usage = (minutes: number, cents: number) =>
  create(UsageSchema, {
    runningSeconds: BigInt(minutes * 60),
    runningMinutes: BigInt(minutes),
    estimatedCost: create(MoneySchema, { amountMinor: BigInt(cents), currency: "EUR" }),
  });

describe("/app/instances", () => {
  it("lists instances with phase, offer, region, running time and estimated cost", async () => {
    const api = fakeApi({
      instances: [
        instance("aaaaaaaa-0000-0000-0000-000000000001", InstancePhase.RUNNING, { name: "web-1", usage: usage(125, 10) }),
      ],
    });
    renderApp("/app/instances", api);

    const r = within(await findRow(/web-1/));
    expect(r.getByText("running")).toBeTruthy();
    expect(r.getByText("cpu-2x2")).toBeTruthy();
    expect(r.getByText("demo")).toBeTruthy();
    expect(r.getByText("125 min")).toBeTruthy();
    expect(r.getByText("€0.10")).toBeTruthy();
    const link = r.getByRole("link", { name: "aaaaaaaa" });
    expect(link.getAttribute("href")).toBe("/app/instances/aaaaaaaa-0000-0000-0000-000000000001");
    expect(link.getAttribute("title")).toBe("aaaaaaaa-0000-0000-0000-000000000001");
  });

  it("hides ended instances until asked", async () => {
    const api = fakeApi({
      instances: [
        instance("run-0000-0000-0000-0000-000000000001", InstancePhase.RUNNING, { name: "alive" }),
        instance("end-0000-0000-0000-0000-000000000002", InstancePhase.TERMINATED, { name: "gone" }),
      ],
    });
    renderApp("/app/instances", api);
    await findRow(/alive/);
    expect(screen.queryByRole("row", { name: /gone/ })).toBeNull();

    fireEvent.click(screen.getByLabelText(/Show terminated and failed instances/));
    const gone = within(await findRow(/gone/));
    expect(api.calls.listInstances.at(-1)).toBe(true);
    expect((gone.getByRole("button", { name: "Terminate" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("points to the offers when there is nothing yet", async () => {
    renderApp("/app/instances", fakeApi());
    expect(await screen.findByText(/You have no instances running/)).toBeTruthy();
    const links = screen.getAllByRole("link", { name: "Browse offers" });
    expect(links.every((l) => l.getAttribute("href") === "/app/offers")).toBe(true);
  });

  it("terminates after a confirmation", async () => {
    const id = "aaaaaaaa-0000-0000-0000-000000000001";
    const api = fakeApi({ instances: [instance(id, InstancePhase.RUNNING, { name: "web-1" })] });
    renderApp("/app/instances", api);

    fireEvent.click(within(await findRow(/web-1/)).getByRole("button", { name: "Terminate" }));
    expect(api.calls.terminate).toEqual([]);
    expect(screen.getByText("Terminate web-1?")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Terminate" }));
    await waitFor(() => expect(api.calls.terminate).toEqual([id]));
    await waitFor(() => expect(within(row(/web-1/)).getByText("terminating")).toBeTruthy());
  });

  it("cancelling the confirmation terminates nothing", async () => {
    const api = fakeApi({ instances: [instance("aaaaaaaa-0000-0000-0000-000000000001", InstancePhase.RUNNING, { name: "web-1" })] });
    renderApp("/app/instances", api);
    fireEvent.click(within(await findRow(/web-1/)).getByRole("button", { name: "Terminate" }));
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(api.calls.terminate).toEqual([]);
  });

  it("shows why a termination failed", async () => {
    const api = fakeApi({ instances: [instance("aaaaaaaa-0000-0000-0000-000000000001", InstancePhase.RUNNING, { name: "web-1" })] });
    api.fail.terminate = new ConnectError("the control plane is restarting", Code.Unavailable);
    renderApp("/app/instances", api);
    fireEvent.click(within(await findRow(/web-1/)).getByRole("button", { name: "Terminate" }));
    fireEvent.click(screen.getByRole("button", { name: "Terminate" }));
    expect((await screen.findByRole("alert")).textContent).toContain("the control plane is restarting");
  });

  it("shows a failure to load, with a retry", async () => {
    const api = fakeApi();
    api.fail.listInstances = new ConnectError("database unavailable", Code.Unavailable);
    renderApp("/app/instances", api);
    expect((await screen.findByRole("alert")).textContent).toContain("database unavailable");
    delete api.fail.listInstances;
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByText(/You have no instances/)).toBeTruthy();
  });

  it("updates an instance in flight at once, from its stream, without waiting for the next poll", async () => {
    const id = "aaaaaaaa-0000-0000-0000-000000000001";
    const api = fakeApi({ instances: [instance(id, InstancePhase.PROVISIONING, { name: "web-1" })] });
    renderApp("/app/instances", api);

    expect(within(await findRow(/web-1/)).getByText("provisioning")).toBeTruthy();
    await waitFor(() => expect(api.calls.watches).toEqual([id]));

    act(() => api.setPhase(id, InstancePhase.RUNNING, { connection: runningConnection() }));
    await waitFor(() => expect(within(row(/web-1/)).getByText("running")).toBeTruthy());
  });

  it("follows only a few instances with streams, because a browser has few connections per host", async () => {
    const many = Array.from({ length: MAX_LIST_STREAMS + 3 }, (_, n) =>
      instance(`bbbbbbbb-0000-0000-0000-00000000000${n}`, InstancePhase.PROVISIONING, { name: `vm-${n}` }),
    );
    const api = fakeApi({ instances: many });
    renderApp("/app/instances", api);
    await findRow(/vm-0/);
    await waitFor(() => expect(api.calls.watches.length).toBe(MAX_LIST_STREAMS));
    // Instances that are not in flight are not followed at all.
    expect(new Set(api.calls.watches).size).toBe(MAX_LIST_STREAMS);
  });

  it("does not stream an instance that is running", async () => {
    const api = fakeApi({ instances: [instance("aaaaaaaa-0000-0000-0000-000000000001", InstancePhase.RUNNING, { name: "web-1" })] });
    renderApp("/app/instances", api);
    await findRow(/web-1/);
    expect(api.calls.watches).toEqual([]);
  });

  it("opens the detail from the id", async () => {
    const id = "aaaaaaaa-0000-0000-0000-000000000001";
    renderApp("/app/instances", fakeApi({ instances: [instance(id, InstancePhase.RUNNING, { name: "web-1" })] }));
    fireEvent.click(within(await findRow(/web-1/)).getByRole("link", { name: "aaaaaaaa" }));
    await waitFor(() => expect(location()).toBe(`/app/instances/${id}`));
    expect(await screen.findByRole("heading", { name: "web-1" })).toBeTruthy();
  });
});
