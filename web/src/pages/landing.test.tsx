import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { fakeAuthServer, renderApp } from "../test/fakeServer";

const location = () => screen.getByTestId("location").textContent;

describe("/ (the guest home page)", () => {
  it("is the first page a visitor sees, with what we do, what we offer and a log in button", async () => {
    renderApp("/");
    expect(await screen.findByRole("heading", { name: "Idle capacity, ready in seconds." })).toBeTruthy();
    expect(location()).toBe("/");
    expect(screen.getByRole("heading", { name: "Hardware that sat idle, put to work." })).toBeTruthy();
    expect(screen.getByRole("table", { name: /^Catalog/ })).toBeTruthy();
    expect(screen.getAllByText("gpu-gtx1060-1x").length).toBeGreaterThan(0);
  });

  it("makes the price tangible with a cost calculator", async () => {
    renderApp("/");
    await screen.findByRole("heading", { name: "Pennies an hour. Seriously." });
    const total = () => screen.getByTestId("calc-total").textContent;
    // GPU at €0.40/hr for the default 48 hours
    expect(total()).toBe("€19.20");
    fireEvent.change(screen.getByLabelText(/Run it for/), { target: { value: "10" } });
    expect(total()).toBe("€4.00");
    fireEvent.click(screen.getByRole("button", { name: "cpu-2x2" }));
    expect(total()).toBe("€0.50");
    expect(screen.getAllByText(/from €0\.05/).length).toBeGreaterThan(0);
  });

  it("leads to the login page", async () => {
    renderApp("/");
    const [login] = await screen.findAllByRole("link", { name: "Log in" });
    expect(login?.getAttribute("href")).toBe("/login");
    fireEvent.click(login as HTMLElement);
    expect(await screen.findByRole("heading", { name: "Log in" })).toBeTruthy();
    expect(location()).toBe("/login");
  });

  it("offers a signed-in person their dashboard instead", async () => {
    renderApp("/", fakeAuthServer("partner"));
    const [open] = await screen.findAllByRole("link", { name: "Open dashboard" });
    expect(open?.getAttribute("href")).toBe("/partner/nodes");
    expect(screen.queryByRole("link", { name: "Log in" })).toBeNull();
  });
});
