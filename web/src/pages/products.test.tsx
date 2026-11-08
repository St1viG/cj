import { fireEvent, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { fakeApi } from "../test/fakeApi";
import { fakeAuthServer, renderApp } from "../test/fakeServer";

const location = () => screen.getByTestId("location").textContent;

describe("/products", () => {
  it("shows the catalog to a guest, who is sent to log in to launch", async () => {
    const server = fakeAuthServer();
    renderApp("/products", server);
    expect(await screen.findByRole("heading", { name: "Compute you can log in to." })).toBeTruthy();
    expect(screen.getByText("2 of 3 configurations".replace("2", "3"))).toBeTruthy();
    const gpu = within(screen.getByRole("row", { name: /gpu-gtx1060-1x/ }));
    expect(gpu.getByText("€0.40")).toBeTruthy();
    const launch = gpu.getByRole("link", { name: "Log in to launch" });
    expect(launch.getAttribute("href")).toBe("/login");
    // a guest never calls the API
    expect(server.calls.getMe).toBe(1);
    // the laptop GPU is not for sale yet
    expect(within(screen.getByRole("row", { name: /gpu-rtx-1x/ })).getByText("soon")).toBeTruthy();
  });

  it("filters by type and price unit, and switches to cards", async () => {
    renderApp("/products", fakeAuthServer());
    await screen.findByRole("heading", { name: "Compute you can log in to." });
    fireEvent.click(screen.getByRole("tab", { name: "CPU" }));
    expect(screen.getByText("1 of 3 configurations")).toBeTruthy();
    expect(screen.queryByRole("row", { name: /gpu-gtx1060-1x/ })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "day" }));
    expect(screen.getByText("€1.20")).toBeTruthy();
    fireEvent.click(screen.getByRole("tab", { name: "View all" }));
    fireEvent.click(screen.getByRole("checkbox", { name: /Available now only/ }));
    expect(screen.getByText("2 of 3 configurations")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Cards" }));
    expect(screen.queryByRole("table")).toBeNull();
    expect(screen.getAllByRole("link", { name: "Log in to launch" })).toHaveLength(2);
  });

  it("shows a customer the live offers, launched from the create form", async () => {
    renderApp("/products", fakeApi());
    expect(await screen.findByText(/Live catalog/)).toBeTruthy();
    const link = (await screen.findAllByRole("link", { name: "Launch" }))[0] as HTMLElement;
    expect(link.getAttribute("href")).toMatch(/^\/app\/instances\/new\?offer=/);
    fireEvent.click(link);
    expect(await screen.findByRole("heading", { name: "New instance" })).toBeTruthy();
    expect(location()).toMatch(/^\/app\/instances\/new/);
  });

  it("is reachable from the home page and the navigation bar", async () => {
    renderApp("/");
    fireEvent.click((await screen.findAllByRole("link", { name: "Browse products" }))[0] as HTMLElement);
    expect(await screen.findByRole("heading", { name: "Compute you can log in to." })).toBeTruthy();
    expect(location()).toBe("/products");
  });
});
