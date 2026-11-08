import { fireEvent, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { landingSections } from "../lib/landingFx";
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

  it("sells the GTX 1060 first, as a product that is on sale now", async () => {
    renderApp("/");
    await screen.findByRole("heading", { name: "Simple prices. Real hardware." });
    const cards = Array.from(document.querySelectorAll<HTMLElement>(".plan"));
    expect(cards.map((c) => within(c).getByRole("heading", { level: 3 }).textContent)).toEqual(["cpu-2x2", "gpu-gtx1060-1x"]);
    const featured = document.querySelector<HTMLElement>('.plan[data-featured="true"]');
    expect(featured).toBeTruthy();
    expect(within(featured as HTMLElement).getByText("Most popular")).toBeTruthy();
    expect(within(featured as HTMLElement).getByText("€0.40")).toBeTruthy();
    expect(within(featured as HTMLElement).getByText("€9.60/day · €292.00/mo")).toBeTruthy();
    // what a run costs at that price
    expect(screen.getByText("€0.07")).toBeTruthy();
    expect(screen.getByText("€3.20")).toBeTruthy();
    expect(screen.getByText("€67.20")).toBeTruthy();
    // presented as a business that is open: nothing is marked as coming later
    expect(document.body.textContent).not.toMatch(/soon/i);
  });

  it("has a rail that points at every section, and the questions a buyer asks", async () => {
    renderApp("/");
    await screen.findByRole("heading", { name: "Answers before you ask." });
    for (const s of landingSections) {
      expect(screen.getByRole("button", { name: s.label })).toBeTruthy();
      if (s.id !== "top") expect(document.getElementById(s.id)).toBeTruthy();
    }
    expect(document.querySelectorAll(".faq details")).toHaveLength(8);
    expect(screen.getByText("How does billing work?")).toBeTruthy();
  });

  it("walks through how it works in four steps next to a pinned panel", async () => {
    renderApp("/");
    await screen.findByRole("heading", { name: "Calm by default, even when things break." });
    const steps = Array.from(document.querySelectorAll(".scrolly .step"));
    expect(steps).toHaveLength(4);
    expect(steps.filter((s) => s.classList.contains("active"))).toHaveLength(1);
    expect(document.querySelector(".pin")).toBeTruthy();
    expect(document.querySelectorAll(".pin-scene")).toHaveLength(4);
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
