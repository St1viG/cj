import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { InstancePhase, MoneySchema, UsageSchema } from "@gen/kondukter/api/v1/types_pb";
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { fakeApi, instance, runningConnection } from "../../test/fakeApi";
import { renderApp } from "../../test/fakeServer";

const ID = "aaaaaaaa-0000-0000-0000-000000000001";
const eur = (cents: number) => create(MoneySchema, { amountMinor: BigInt(cents), currency: "EUR" });

function running() {
  return instance(ID, InstancePhase.RUNNING, {
    name: "web-1",
    connection: runningConnection(),
    sshKeyFingerprints: ["SHA256:k1fp"],
    usage: create(UsageSchema, { runningSeconds: 7500n, runningMinutes: 125n, estimatedCost: eur(10) }),
  });
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("/app/instances/:id", () => {
  it("shows the instance: status, connection, mappings, details, usage and timeline", async () => {
    renderApp(`/app/instances/${ID}`, fakeApi({ instances: [running()] }));

    expect(await screen.findByRole("heading", { name: "web-1" })).toBeTruthy();
    expect(screen.getByText("ssh -p 40001 ubuntu@100.64.0.5")).toBeTruthy();

    const status = screen.getByRole("heading", { name: "Status" }).nextElementSibling as HTMLElement;
    expect(within(status).getByText("running")).toBeTruthy();

    const mappings = screen.getByRole("table", { name: "Port mappings" });
    expect(within(mappings).getByText("22/tcp")).toBeTruthy();
    expect(within(mappings).getByText("100.64.0.5:40001")).toBeTruthy();
    expect(within(mappings).getByText("8080/tcp")).toBeTruthy();
    expect(within(mappings).getByText("100.64.0.5:40002")).toBeTruthy();

    expect(screen.getByText("ubuntu 24.04 (amd64)")).toBeTruthy();
    expect(screen.getByText("SHA256:k1fp")).toBeTruthy();
    expect(screen.getByText("125 min")).toBeTruthy();
    expect(screen.getByText("€0.05")).toBeTruthy(); // price per hour
    expect(screen.getByText("€0.10")).toBeTruthy(); // estimated cost
  });

  it("copies the ssh command", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal("isSecureContext", true);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    renderApp(`/app/instances/${ID}`, fakeApi({ instances: [running()] }));

    fireEvent.click(await screen.findByRole("button", { name: "Copy" }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith("ssh -p 40001 ubuntu@100.64.0.5"));
    expect(await screen.findByText("Copied")).toBeTruthy();
  });

  it("says so when copying is impossible, and leaves the command to select", async () => {
    vi.stubGlobal("isSecureContext", false);
    document.execCommand = vi.fn(() => false);
    renderApp(`/app/instances/${ID}`, fakeApi({ instances: [running()] }));

    fireEvent.click(await screen.findByRole("button", { name: "Copy" }));
    expect(await screen.findByText(/Could not copy/)).toBeTruthy();
    expect(screen.getByText("ssh -p 40001 ubuntu@100.64.0.5")).toBeTruthy();
  });

  it("follows the instance to running without a refresh, and shows how to connect once it is", async () => {
    const api = fakeApi({ instances: [instance(ID, InstancePhase.PENDING, { name: "web-1" })] });
    renderApp(`/app/instances/${ID}`, api);

    expect(await screen.findByRole("heading", { name: "web-1" })).toBeTruthy();
    expect(screen.getByText("Connection details appear once the instance is running.")).toBeTruthy();
    await waitFor(() => expect(api.calls.watches).toEqual([ID]));

    act(() => api.setPhase(ID, InstancePhase.PROVISIONING, { phaseReason: "image cached" }));
    const status = () => screen.getByRole("heading", { name: "Status" }).nextElementSibling as HTMLElement;
    await waitFor(() => expect(within(status()).getByText("provisioning")).toBeTruthy());
    expect(within(status()).getByText("image cached")).toBeTruthy();

    act(() => api.setPhase(ID, InstancePhase.RUNNING, { connection: runningConnection() }));
    await waitFor(() => expect(within(status()).getByText("running")).toBeTruthy());
    expect(screen.getByText("ssh -p 40001 ubuntu@100.64.0.5")).toBeTruthy();
    expect(screen.queryByText("Connection details appear once the instance is running.")).toBeNull();
  });

  it("lists every phase change in the timeline, oldest first", async () => {
    const api = fakeApi({ instances: [instance(ID, InstancePhase.PENDING, { name: "web-1" })] });
    renderApp(`/app/instances/${ID}`, api);
    await screen.findByRole("heading", { name: "web-1" });
    await waitFor(() => expect(api.calls.watches).toEqual([ID]));

    act(() => api.setPhase(ID, InstancePhase.SCHEDULED));
    act(() => api.setPhase(ID, InstancePhase.RUNNING, { connection: runningConnection() }));

    // The timeline is refetched when the phase changes.
    await waitFor(() => {
      const timeline = screen.getByRole("table", { name: "Timeline" });
      const to = within(timeline)
        .getAllByRole("row")
        .slice(1)
        .map((r) => within(r).getAllByRole("cell")[2]?.textContent);
      expect(to).toEqual(["scheduled", "running"]);
    });
  });

  it("terminates after a confirmation and shows the phase change", async () => {
    const api = fakeApi({ instances: [running()] });
    renderApp(`/app/instances/${ID}`, api);
    await screen.findByRole("heading", { name: "web-1" });

    fireEvent.click(screen.getByRole("button", { name: "Terminate" }));
    expect(api.calls.terminate).toEqual([]);
    expect(screen.getByText("Terminate this instance? This cannot be undone.")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Terminate" }));

    await waitFor(() => expect(api.calls.terminate).toEqual([ID]));
    const status = () => screen.getByRole("heading", { name: "Status" }).nextElementSibling as HTMLElement;
    await waitFor(() => expect(within(status()).getByText("terminating")).toBeTruthy());
    expect(within(status()).getByText("terminated by customer@demo.local")).toBeTruthy();
    // Nothing left to terminate.
    expect((screen.getByRole("button", { name: "Terminate" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("disables Terminate for an instance that has ended", async () => {
    renderApp(`/app/instances/${ID}`, fakeApi({ instances: [instance(ID, InstancePhase.TERMINATED, { name: "old" })] }));
    await screen.findByRole("heading", { name: "old" });
    expect((screen.getByRole("button", { name: "Terminate" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("says when the instance does not exist, with a way back", async () => {
    renderApp("/app/instances/not-there", fakeApi());
    expect((await screen.findByRole("alert")).textContent).toContain("instance not found");
    expect(screen.getByRole("link", { name: "← Instances" }).getAttribute("href")).toBe("/app/instances");
  });

  it("shows a failure to load, with a retry that works", async () => {
    const api = fakeApi({ instances: [running()] });
    api.fail.getInstance = new ConnectError("database unavailable", Code.Unavailable);
    renderApp(`/app/instances/${ID}`, api);
    expect((await screen.findByRole("alert")).textContent).toContain("database unavailable");
    expect(screen.queryByRole("heading", { name: "web-1" })).toBeNull();

    delete api.fail.getInstance;
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByRole("heading", { name: "web-1" })).toBeTruthy();
  });
});
