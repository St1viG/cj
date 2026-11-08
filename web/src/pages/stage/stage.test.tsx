import { InstancePhase, NodeStatus } from "@gen/kondukter/api/v1/types_pb";
import { screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { instance } from "../../test/fakeApi";
import { fakeFleet, node, site } from "../../test/fakeFleet";
import { renderApp } from "../../test/fakeServer";

describe("/stage", () => {
  it("shows the nodes by site with their statuses and live instances", async () => {
    const fleet = fakeFleet({
      as: "operator",
      sites: [site("site-demo", "demo")],
      nodes: [
        node("n1", "fed-d", { status: NodeStatus.UNREACHABLE, statusReason: "missed heartbeats" }),
        node("n2", "mac-a", { arch: "arm64", driver: "lima" }),
      ],
      instances: [instance("i-7f3c1a2b9d", InstancePhase.UNREACHABLE, { nodeId: "n1", name: "gpu-1" })],
    });
    renderApp("/stage?demo=1", fleet);

    expect(await screen.findByRole("heading", { name: "Fleet" })).toBeTruthy();
    const fedD = await screen.findByRole("heading", { name: "fed-d" });
    const card = fedD.closest("article") as HTMLElement;
    expect(within(card).getByText("unreachable (missed heartbeats)")).toBeTruthy();
    expect(within(card).getByText("gpu-1")).toBeTruthy();
    expect(within(card).getByText("unreachable", { selector: "li *" })).toBeTruthy();

    const macA = (await screen.findByRole("heading", { name: "mac-a" })).closest("article") as HTMLElement;
    expect(within(macA).getByText("no instances")).toBeTruthy();
    expect(within(macA).getByText("ready")).toBeTruthy();
  });

  it("is for operators only", async () => {
    renderApp("/stage", fakeFleet({ as: "partner" }));
    expect(await screen.findByRole("heading", { name: "Nodes" })).toBeTruthy();
  });
});
