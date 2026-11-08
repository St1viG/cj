import { Code, ConnectError } from "@connectrpc/connect";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { fakeApi } from "../test/fakeApi";
import { type AccountName, accounts, fakeAuthServer, PASSWORD, renderApp } from "../test/fakeServer";

function location(): string {
  return screen.getByTestId("location").textContent ?? "";
}

async function logIn(email: string, password: string) {
  fireEvent.change(await screen.findByLabelText("Email"), { target: { value: email } });
  fireEvent.change(screen.getByLabelText("Password"), { target: { value: password } });
  fireEvent.click(screen.getByRole("button", { name: "Log in" }));
}

describe("a visitor without a session", () => {
  it("is sent from /app to /login", async () => {
    renderApp("/app/instances");
    expect(await screen.findByRole("heading", { name: "Log in" })).toBeTruthy();
    expect(location()).toBe("/login");
  });

  it.each(["/app", "/partner/nodes", "/admin/audit", "/stage", "/"])("is sent from %s to /login", async (path) => {
    renderApp(path);
    expect(await screen.findByRole("heading", { name: "Log in" })).toBeTruthy();
    expect(location()).toBe("/login");
  });

  it("is sent from an unknown page inside an area to /login too", async () => {
    renderApp("/app/nonsense");
    expect(await screen.findByRole("heading", { name: "Log in" })).toBeTruthy();
  });

  it("sees not-found for a path outside every area", async () => {
    renderApp("/nonsense");
    expect(await screen.findByRole("heading", { name: "Page not found" })).toBeTruthy();
  });
});

describe("login", () => {
  it.each<[AccountName, string]>([
    ["customer", "/app/instances"],
    ["partner", "/partner/nodes"],
    ["operator", "/admin/nodes"],
  ])("lands the %s in %s", async (name, home) => {
    renderApp("/login");
    await logIn(accounts[name].email, PASSWORD);
    await waitFor(() => expect(location()).toBe(home));
    expect(screen.getByText(accounts[name].email, { exact: false })).toBeTruthy();
  });

  it("returns to the page the visitor first asked for", async () => {
    renderApp("/app/keys");
    await logIn(accounts.customer.email, PASSWORD);
    await waitFor(() => expect(location()).toBe("/app/keys"));
  });

  it("ignores that page when the role may not open it", async () => {
    renderApp("/admin/audit");
    await logIn(accounts.customer.email, PASSWORD);
    await waitFor(() => expect(location()).toBe("/app/instances"));
  });

  it("shows the server's message for wrong credentials and stays on the form", async () => {
    renderApp("/login");
    await logIn(accounts.customer.email, "wrong");
    expect((await screen.findByRole("alert")).textContent).toBe("wrong email or password");
    expect(location()).toBe("/login");
    // The form is usable again.
    expect((screen.getByRole("button", { name: "Log in" }) as HTMLButtonElement).disabled).toBe(false);
  });

  it("sends a visitor who already has a session on to their home", async () => {
    renderApp("/login", fakeAuthServer("partner"));
    await waitFor(() => expect(location()).toBe("/partner/nodes"));
  });
});

describe("a signed-in user", () => {
  it.each<[AccountName, string, string]>([
    ["customer", "/admin/nodes", "/app/instances"],
    ["customer", "/stage", "/app/instances"],
    ["partner", "/app/offers", "/partner/nodes"],
    ["operator", "/app/offers", "/admin/nodes"],
    ["operator", "/partner/nodes", "/admin/nodes"],
  ])("%s opening %s is sent to %s", async (name, path, home) => {
    renderApp(path, fakeAuthServer(name));
    await waitFor(() => expect(location()).toBe(home));
  });

  it("is redirected from an area's base path to its first page", async () => {
    renderApp("/app", fakeApi());
    await waitFor(() => expect(location()).toBe("/app/instances"));
    expect(screen.getByRole("heading", { name: "Instances" })).toBeTruthy();
  });

  it("opens /stage as operator, without the area header", async () => {
    renderApp("/stage?demo=1", fakeAuthServer("operator"));
    expect(await screen.findByRole("heading", { name: "Fleet" })).toBeTruthy();
    expect(location()).toBe("/stage?demo=1");
    expect(screen.queryByRole("button", { name: "Log out" })).toBeNull();
  });

  it("sees only their area's navigation", async () => {
    renderApp("/app/offers", fakeApi());
    const nav = await screen.findByRole("navigation", { name: "Customer" });
    const links = Array.from(nav.querySelectorAll("a")).map((a) => a.getAttribute("href"));
    expect(links).toEqual(["/app/offers", "/app/instances", "/app/keys", "/app/usage"]);
  });

  it("matches /app/instances/new before /app/instances/:id", async () => {
    renderApp("/app/instances/new", fakeApi());
    expect(await screen.findByRole("heading", { name: "New instance" })).toBeTruthy();
    // Not the detail page of an instance called "new".
    expect(screen.queryByRole("link", { name: "← Instances" })).toBeNull();
  });

  it("logs out to /login and starts the next login at home", async () => {
    const server = fakeAuthServer("customer");
    renderApp("/app/keys", server);
    fireEvent.click(await screen.findByRole("button", { name: "Log out" }));
    await waitFor(() => expect(location()).toBe("/login"));
    expect(server.calls.logout).toBe(1);

    await logIn(accounts.customer.email, PASSWORD);
    await waitFor(() => expect(location()).toBe("/app/instances"));
  });
});

describe("a session that ends", () => {
  it("sends the user to /login and back to the same page after the next login", async () => {
    const api = fakeApi();
    renderApp("/app/keys", api);
    await screen.findByRole("heading", { name: "Keys" });
    await waitFor(() => expect(api.calls.listSSHKeys.length).toBeGreaterThan(0));

    // The session expires behind the page's back; the next request finds out.
    api.fail.listSSHKeys = new ConnectError("session expired or logged out", Code.Unauthenticated);
    window.dispatchEvent(new Event("visibilitychange"));
    await waitFor(() => expect(location()).toBe("/login"));

    delete api.fail.listSSHKeys;
    await logIn(accounts.customer.email, PASSWORD);
    await waitFor(() => expect(location()).toBe("/app/keys"));
  });
});

describe("when the server cannot be reached", () => {
  it("shows the error with a retry instead of treating the user as signed out", async () => {
    const server = fakeAuthServer("customer");
    server.setDown(true);
    renderApp("/app/instances", server);
    expect((await screen.findByRole("alert")).textContent).toContain("the server is unreachable");
    expect(location()).toBe("/app/instances");

    server.setDown(false);
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByRole("heading", { name: "Instances" })).toBeTruthy();
  });
});
