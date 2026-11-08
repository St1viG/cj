import { create as createMessage } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { OfferSchema } from "@gen/kondukter/api/v1/catalog_pb";
import { CapacityClass, Protocol } from "@gen/kondukter/api/v1/types_pb";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { demoCatalog, fakeApi } from "../../test/fakeApi";
import { renderApp } from "../../test/fakeServer";

const location = () => screen.getByTestId("location").textContent;
const input = (label: string) => screen.getByLabelText(label) as HTMLInputElement;
const create = () => fireEvent.click(screen.getByRole("button", { name: "Create instance" }));

/** Opens the form for the CPU offer and waits until its images are listed. */
async function openCpuForm(api = fakeApi()) {
  renderApp("/app/instances/new?offer=offer-cpu", api);
  await screen.findByRole("option", { name: /ubuntu 24.04 · amd64/ });
  await screen.findByLabelText(/laptop/);
  return api;
}

describe("/app/instances/new", () => {
  it("preselects the offer from the link and lists the images that run on it", async () => {
    const api = await openCpuForm();
    expect(input("Offer").value).toBe("offer-cpu");
    expect(api.calls.listImages.at(-1)).toEqual({ offerId: "offer-cpu", arch: "amd64" });
    // A CPU offer gets CPU images only.
    expect(screen.queryByRole("option", { name: /ubuntu-gpu-pascal/ })).toBeNull();
  });

  it("offers the architecture choice when the offer has several, and follows it", async () => {
    const api = await openCpuForm();
    fireEvent.change(input("Architecture"), { target: { value: "arm64" } });
    expect(await screen.findByRole("option", { name: /ubuntu 24.04 · arm64/ })).toBeTruthy();
    expect(api.calls.listImages.at(-1)).toEqual({ offerId: "offer-cpu", arch: "arm64" });
    expect(screen.queryByRole("option", { name: /ubuntu 24.04 · amd64/ })).toBeNull();
  });

  it("shows a GPU offer's image and no architecture choice for its single architecture", async () => {
    renderApp("/app/instances/new?offer=offer-gpu", fakeApi());
    expect(await screen.findByRole("option", { name: /ubuntu-gpu-pascal 24.04-580 · amd64 · nvidia-580-cuda12/ })).toBeTruthy();
    expect(screen.queryByLabelText("Architecture")).toBeNull();
    expect(screen.queryByRole("option", { name: /^ubuntu 24.04/ })).toBeNull();
  });

  it("lets the customer pick the offer when none came with the link", async () => {
    renderApp("/app/instances/new", fakeApi());
    const offer = await screen.findByLabelText("Offer");
    expect(screen.queryByLabelText("Image")).toBeNull();
    fireEvent.change(offer, { target: { value: "offer-gpu" } });
    expect(await screen.findByRole("option", { name: /ubuntu-gpu-pascal/ })).toBeTruthy();
  });

  it("creates the instance with what was entered and opens it", async () => {
    const api = await openCpuForm();
    fireEvent.change(input("Name (optional)"), { target: { value: "web-1" } });
    fireEvent.click(screen.getByLabelText(/laptop/));
    fireEvent.change(input("Exposed ports"), { target: { value: "8080, 53/udp" } });
    fireEvent.change(input("Cloud-init user-data (optional)"), { target: { value: "#!/bin/sh\necho hi\n" } });
    create();

    await waitFor(() => expect(api.calls.createInstance).toHaveLength(1));
    const sent = api.calls.createInstance[0];
    expect(sent).toMatchObject({
      offerId: "offer-cpu",
      imageId: "img-cpu-amd64",
      sshKeyIds: ["k1"],
      userData: "#!/bin/sh\necho hi\n",
      name: "web-1",
    });
    expect(sent?.ports.map((p) => [p.guestPort, p.protocol])).toEqual([
      [8080, Protocol.TCP],
      [53, Protocol.UDP],
    ]);
    expect(sent?.idempotencyKey).toMatch(/^[0-9a-f-]{36}$/);

    await waitFor(() => expect(location()).toMatch(/^\/app\/instances\/inst-1-/));
    expect(await screen.findByRole("heading", { name: "web-1" })).toBeTruthy();
  });

  it("sends several keys", async () => {
    const api = await openCpuForm();
    fireEvent.click(screen.getByLabelText(/laptop/));
    fireEvent.click(screen.getByLabelText(/ci/));
    create();
    await waitFor(() => expect(api.calls.createInstance[0]?.sshKeyIds).toEqual(["k1", "k2"]));
  });

  it("asks for an SSH key before calling the API", async () => {
    const api = await openCpuForm();
    create();
    expect((await screen.findByRole("alert")).textContent).toBe("Select at least one SSH key.");
    expect(api.calls.createInstance).toHaveLength(0);
  });

  it("explains a bad port list before calling the API", async () => {
    const api = await openCpuForm();
    fireEvent.click(screen.getByLabelText(/laptop/));
    fireEvent.change(input("Exposed ports"), { target: { value: "22" } });
    create();
    expect((await screen.findByRole("alert")).textContent).toBe("22/tcp is always exposed; leave it out.");
    expect(api.calls.createInstance).toHaveLength(0);
  });

  it("refuses user-data over the 16 KiB limit, counting bytes", async () => {
    const api = await openCpuForm();
    fireEvent.click(screen.getByLabelText(/laptop/));
    // 8193 two-byte characters: 16386 bytes, though only 8193 characters.
    fireEvent.change(input("Cloud-init user-data (optional)"), { target: { value: "é".repeat(8193) } });
    create();
    expect((await screen.findByRole("alert")).textContent).toContain("16386 bytes");
    expect(api.calls.createInstance).toHaveLength(0);
  });

  it("shows the server's reason when it refuses", async () => {
    const api = await openCpuForm();
    api.fail.createInstance = new ConnectError("image_id: ubuntu 24.04 is not a CPU image for this offer", Code.InvalidArgument);
    fireEvent.click(screen.getByLabelText(/laptop/));
    create();
    expect((await screen.findByRole("alert")).textContent).toContain("is not a CPU image for this offer");
    expect(location()).toBe("/app/instances/new?offer=offer-cpu");
  });

  it("retries the same request under the same idempotency key, and a changed one under a new key", async () => {
    const api = await openCpuForm();
    api.fail.createInstance = new ConnectError("connection reset", Code.Unavailable);
    fireEvent.click(screen.getByLabelText(/laptop/));
    create();
    await waitFor(() => expect(api.calls.createInstance).toHaveLength(1));
    await screen.findByRole("alert");
    create();
    await waitFor(() => expect(api.calls.createInstance).toHaveLength(2));
    await waitFor(() => expect(screen.getByRole("button", { name: "Create instance" }).closest("fieldset")?.disabled).toBe(false));

    fireEvent.change(input("Name (optional)"), { target: { value: "changed" } });
    create();
    await waitFor(() => expect(api.calls.createInstance).toHaveLength(3));

    const [first, second, third] = api.calls.createInstance.map((r) => r.idempotencyKey);
    expect(second).toBe(first);
    expect(third).not.toBe(first);
  });

  it("points to the Keys page when the account has no SSH keys", async () => {
    renderApp("/app/instances/new?offer=offer-cpu", fakeApi({ sshKeys: [] }));
    const link = await screen.findByRole("link", { name: "Add one" });
    expect(link.getAttribute("href")).toBe("/app/keys");
  });

  it("says when the linked offer is not for sale", async () => {
    renderApp("/app/instances/new?offer=gone", fakeApi());
    expect((await screen.findByText("That offer is not for sale.")).getAttribute("role")).toBe("alert");
  });

  it("says when no image can run on the offer", async () => {
    const [base] = demoCatalog().offers;
    const bare = createMessage(OfferSchema, {
      id: "offer-bare",
      shape: base?.shape,
      region: "demo",
      capacityClass: CapacityClass.DEDICATED,
      active: true,
      archs: [],
    });
    renderApp("/app/instances/new?offer=offer-bare", fakeApi({ offers: [bare] }));
    expect((await screen.findByText("No image can run on this offer yet.")).getAttribute("role")).toBe("alert");
    expect(screen.queryByLabelText("Image")).toBeNull();
  });

  it("says when nothing is for sale", async () => {
    renderApp("/app/instances/new", fakeApi({ offers: [] }));
    expect(await screen.findByText("No offers are for sale yet.")).toBeTruthy();
  });
});
