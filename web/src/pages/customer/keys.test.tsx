import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { APIKeySchema } from "@gen/kondukter/api/v1/account_pb";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { fakeApi } from "../../test/fakeApi";
import { renderApp } from "../../test/fakeServer";

const row = (name: RegExp) => screen.getByRole("row", { name });
const findRow = (name: RegExp) => screen.findByRole("row", { name });
/** A field of the SSH key form (the API key form has a "Name" too). */
const sshField = (label: string) => {
  const form = screen.getByRole("button", { name: "Add SSH key" }).closest("form") as HTMLElement;
  return within(form).getByLabelText(label) as HTMLInputElement;
};

describe("/app/keys: SSH keys", () => {
  it("lists the keys with their fingerprints", async () => {
    renderApp("/app/keys", fakeApi());
    const laptop = within(await findRow(/laptop/));
    expect(laptop.getByText("SHA256:k1fp")).toBeTruthy();
    expect(within(row(/ci/)).getByText("SHA256:k2fp")).toBeTruthy();
  });

  it("says when there are none", async () => {
    renderApp("/app/keys", fakeApi({ sshKeys: [] }));
    expect(await screen.findByText("You have no SSH keys yet. Add one to log in to an instance.")).toBeTruthy();
  });

  it("adds a key and clears the form", async () => {
    const api = fakeApi();
    renderApp("/app/keys", api);
    await findRow(/laptop/);

    fireEvent.change(sshField("Name"), { target: { value: "  desktop " } });
    fireEvent.change(sshField("Public key"), { target: { value: " ssh-ed25519 AAAAdesktop me@desktop \n" } });
    fireEvent.click(screen.getByRole("button", { name: "Add SSH key" }));

    await waitFor(() => expect(api.calls.addSSHKey).toEqual([{ name: "desktop", publicKey: "ssh-ed25519 AAAAdesktop me@desktop" }]));
    expect(await findRow(/desktop/)).toBeTruthy();
    expect(sshField("Name").value).toBe("");
    expect(sshField("Public key").value).toBe("");
  });

  it("keeps what was typed and shows the reason when the key is refused", async () => {
    renderApp("/app/keys", fakeApi());
    await findRow(/laptop/);

    fireEvent.change(sshField("Name"), { target: { value: "bad" } });
    fireEvent.change(sshField("Public key"), { target: { value: "not a key" } });
    fireEvent.click(screen.getByRole("button", { name: "Add SSH key" }));

    expect((await screen.findByText("public_key: not an OpenSSH public key")).getAttribute("role")).toBe("alert");
    expect(sshField("Public key").value).toBe("not a key");
  });

  it("deletes a key after a confirmation", async () => {
    const api = fakeApi();
    renderApp("/app/keys", api);
    fireEvent.click(within(await findRow(/laptop/)).getByRole("button", { name: "Delete" }));
    expect(api.calls.deleteSSHKey).toEqual([]);
    expect(screen.getByText("Delete laptop? Instances already running keep it.")).toBeTruthy();
    fireEvent.click(within(row(/laptop/)).getByRole("button", { name: "Delete" }));

    await waitFor(() => expect(api.calls.deleteSSHKey).toEqual(["k1"]));
    await waitFor(() => expect(screen.queryByRole("row", { name: /laptop/ })).toBeNull());
    expect(row(/ci/)).toBeTruthy();
  });
});

describe("/app/keys: API keys", () => {
  const existing = create(APIKeySchema, {
    id: "ak-old",
    name: "ci-runner",
    prefix: "kdk_abcd",
    createdAt: timestampFromDate(new Date(2026, 10, 1, 12, 0, 0)),
  });

  it("lists keys with prefix, last use and a revoke button", async () => {
    renderApp("/app/keys", fakeApi({ apiKeys: [existing] }));
    const r = within(await findRow(/ci-runner/));
    expect(r.getByText("kdk_abcd…")).toBeTruthy();
    expect(r.getByText("never")).toBeTruthy();
    expect(r.getByRole("button", { name: "Revoke" })).toBeTruthy();
  });

  it("shows a revoked key as revoked, with no button", async () => {
    const revoked = create(APIKeySchema, { ...existing, revokedAt: timestampFromDate(new Date(2026, 10, 2, 8, 30, 0)) });
    renderApp("/app/keys", fakeApi({ apiKeys: [revoked] }));
    const r = within(await findRow(/ci-runner/));
    expect(r.getByText(/^revoked /)).toBeTruthy();
    expect(r.queryByRole("button", { name: "Revoke" })).toBeNull();
  });

  it("says when there are none", async () => {
    renderApp("/app/keys", fakeApi());
    expect(await screen.findByText("You have no API keys yet.")).toBeTruthy();
  });

  it("shows a new key's secret once, then never again", async () => {
    const api = fakeApi();
    renderApp("/app/keys", api);
    await screen.findByText("You have no API keys yet.");

    const form = screen.getByRole("button", { name: "Create API key" }).closest("form") as HTMLElement;
    fireEvent.change(within(form).getByLabelText("Name"), { target: { value: "laptop-cli" } });
    fireEvent.click(screen.getByRole("button", { name: "Create API key" }));

    const secret = (await screen.findByLabelText("New API key")) as HTMLInputElement;
    expect(secret.value).toBe("kdk_newkeysecret0123456789");
    expect(screen.getByText(/it is not shown again/)).toBeTruthy();
    expect(await findRow(/laptop-cli/)).toBeTruthy(); // the list now has the key, without its secret
    expect(within(row(/laptop-cli/)).queryByText(/kdk_newkeysecret/)).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "I have saved it" }));
    expect(screen.queryByLabelText("New API key")).toBeNull();
    expect(screen.queryByDisplayValue("kdk_newkeysecret0123456789")).toBeNull();
  });

  it("uses a fresh idempotency key for every creation, because a replay returns no secret", async () => {
    const api = fakeApi();
    renderApp("/app/keys", api);
    await screen.findByText("You have no API keys yet.");

    const form = () => screen.getByRole("button", { name: "Create API key" }).closest("form") as HTMLElement;
    for (let n = 0; n < 2; n++) {
      fireEvent.change(within(form()).getByLabelText("Name"), { target: { value: "same-name" } });
      fireEvent.click(screen.getByRole("button", { name: "Create API key" }));
      await waitFor(() => expect(api.calls.createAPIKey).toHaveLength(n + 1));
      await screen.findByLabelText("New API key");
    }
    const [a, b] = api.calls.createAPIKey.map((c) => c.idempotencyKey);
    expect(a).toBeTruthy();
    expect(b).not.toBe(a);
  });

  it("revokes a key after a confirmation", async () => {
    const api = fakeApi({ apiKeys: [existing] });
    renderApp("/app/keys", api);
    fireEvent.click(within(await findRow(/ci-runner/)).getByRole("button", { name: "Revoke" }));
    expect(screen.getByText("Revoke ci-runner? Anything using it stops working.")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Revoke" }));

    await waitFor(() => expect(api.calls.revokeAPIKey).toEqual(["ak-old"]));
    await waitFor(() => expect(within(row(/ci-runner/)).getByText(/^revoked /)).toBeTruthy());
  });
});
