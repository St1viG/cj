import { Code, ConnectError } from "@connectrpc/connect";
import { InstancePhase, Protocol } from "@gen/kondukter/api/v1/types_pb";
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { fakeApi, runningConnection } from "../../test/fakeApi";
import { renderApp } from "../../test/fakeServer";
import { KEY_FILE } from "./RentGpuCard";

afterEach(() => {
  vi.unstubAllGlobals();
  sessionStorage.clear();
});

const rentButton = () => screen.findByRole("button", { name: /Rent a GeForce GTX 1060/ });
const card = (name: string) => screen.getByLabelText(name);

describe("the Rent a GPU card on /app/offers", () => {
  it("offers the GPU machines, and only those", async () => {
    renderApp("/app/offers", fakeApi());
    const button = await rentButton();
    expect(button.textContent).toBe("Rent a GeForce GTX 1060 6GB");
    expect(screen.getAllByRole("button", { name: /^Rent a / })).toHaveLength(1); // the CPU offer has none
  });

  it("is also on the page a customer lands on after login", async () => {
    renderApp("/app/instances", fakeApi());
    expect(await rentButton()).toBeTruthy();
  });

  it("makes a key, creates the instance with its public half and the service port, then shows how to log in", async () => {
    const api = fakeApi();
    renderApp("/app/offers", api);
    fireEvent.click(await rentButton());

    await waitFor(() => expect(api.calls.createInstance).toHaveLength(1));
    const req = api.calls.createInstance[0];
    expect(req?.offerId).toBe("offer-gpu");
    expect(req?.sshPublicKeys).toHaveLength(1);
    expect(req?.sshPublicKeys[0]).toMatch(/^ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA\S+ kondukter-\S+$/);
    expect(req?.sshKeyIds).toEqual([]); // nothing is saved in the account
    expect(req?.ports.map((p) => [p.guestPort, p.protocol])).toEqual([[8080, Protocol.TCP]]);
    expect(req?.name).toMatch(/^gpu-[0-9a-f]{6}$/);
    expect(req?.idempotencyKey).not.toBe("");
    // The image is one the GPU can run, found through the catalog.
    expect(api.calls.listImages).toEqual([{ offerId: "offer-gpu", arch: "amd64" }]);
    expect(req?.imageId).not.toBe("");

    // It follows the machine and shows the phase, with no login details yet.
    const your = await waitFor(() => card("Your GPU"));
    expect(within(your).queryByRole("button", { name: "Download private key" })).toBeNull();
    const id = [...api.instances.keys()][0] ?? "";
    await act(async () => {
      api.setPhase(id, InstancePhase.RUNNING, { connection: runningConnection(40000), phaseReason: "agent_running" });
    });

    expect(await within(card("Your GPU")).findByRole("button", { name: "Download private key" })).toBeTruthy();
    const text = card("Your GPU").textContent ?? "";
    expect(text).toContain(`ssh -i ~/Downloads/${KEY_FILE} -p 40000 ubuntu@100.64.0.5`);
    expect(text).toContain(`chmod 600 ~/Downloads/${KEY_FILE}`);
    expect(text).toContain("http://100.64.0.5:40001"); // the mapping of guest port 8080 in the fake
  });

  it("downloads the private key that matches the public one it sent", async () => {
    const api = fakeApi();
    let blob: Blob | undefined;
    let filename = "";
    // jsdom has no object URLs; add them to the real URL class rather than replacing it.
    URL.createObjectURL = (b: Blob | MediaSource) => ((blob = b as Blob), "blob:key");
    URL.revokeObjectURL = () => {};
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
      filename = this.download;
    });
    renderApp("/app/offers", api);
    fireEvent.click(await rentButton());
    await waitFor(() => expect(api.calls.createInstance).toHaveLength(1));
    const id = [...api.instances.keys()][0] ?? "";
    await waitFor(() => card("Your GPU"));
    await act(async () => {
      api.setPhase(id, InstancePhase.RUNNING, { connection: runningConnection(40000) });
    });

    fireEvent.click(await screen.findByRole("button", { name: "Download private key" }));
    expect(filename).toBe(KEY_FILE);
    const text = await blob?.text();
    expect(text).toMatch(/^-----BEGIN OPENSSH PRIVATE KEY-----\n/);
    expect(text).toMatch(/-----END OPENSSH PRIVATE KEY-----\n$/);
    click.mockRestore();
  });

  it("says so when the machine cannot be started, and lets the person rent another", async () => {
    const api = fakeApi();
    renderApp("/app/offers", api);
    fireEvent.click(await rentButton());
    await waitFor(() => card("Your GPU"));
    const id = [...api.instances.keys()][0] ?? "";
    await act(async () => {
      api.setPhase(id, InstancePhase.FAILED, { phaseReason: "no_capacity" });
    });

    expect(await screen.findByText("The machine did not start.")).toBeTruthy();
    expect(within(card("Your GPU")).getByText("no_capacity")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Rent another" }));
    expect(await rentButton()).toBeTruthy();
  });

  it("gives the machine back", async () => {
    const api = fakeApi();
    renderApp("/app/offers", api);
    fireEvent.click(await rentButton());
    await waitFor(() => card("Your GPU"));
    const id = [...api.instances.keys()][0] ?? "";

    fireEvent.click(screen.getByRole("button", { name: "Give it back" }));
    await waitFor(() => expect(api.calls.terminate).toEqual([id]));
  });

  it("shows the error when the API refuses, and stays on the offer", async () => {
    const api = fakeApi();
    api.fail.createInstance = new ConnectError("quota exceeded", Code.ResourceExhausted);
    renderApp("/app/offers", api);
    fireEvent.click(await rentButton());

    expect((await screen.findByRole("alert")).textContent).toContain("quota exceeded");
    expect(await rentButton()).toBeTruthy(); // can try again
  });

  it("picks up a rental after a page reload", async () => {
    const api = fakeApi();
    renderApp("/app/offers", api);
    fireEvent.click(await rentButton());
    await waitFor(() => card("Your GPU"));
    const id = [...api.instances.keys()][0] ?? "";
    await act(async () => {
      api.setPhase(id, InstancePhase.RUNNING, { connection: runningConnection(40000) });
    });
    await screen.findByRole("button", { name: "Download private key" });
    expect(sessionStorage.getItem("kondukter.rental")).toContain(id);

    // A new render of the app, same tab: the card finds the rental again.
    document.body.innerHTML = "";
    renderApp("/app/offers", api);
    expect(await screen.findByRole("button", { name: "Download private key" })).toBeTruthy();
  });
});
