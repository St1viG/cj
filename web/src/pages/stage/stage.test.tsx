import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { Code, ConnectError } from "@connectrpc/connect";
import { InstancePhase, NodeStatus } from "@gen/kondukter/api/v1/types_pb";
import { QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiProvider } from "../../api/ApiProvider";
import { createQueryClient } from "../../api/queryClient";
import { formatClock } from "../../lib/format";
import { instance } from "../../test/fakeApi";
import { node } from "../../test/fakeFleet";
import { type FakeStage, fakeStage } from "../../test/fakeStage";
import { renderApp } from "../../test/fakeServer";
import { StagePage } from "./StagePage";

/** The frozen "now" of these tests, so ages read the same every run. */
const T0 = Date.parse("2026-11-08T10:00:00Z");
const ts = (msFromNow: number) => timestampFromDate(new Date(T0 + msFromNow));

const WEB = "aaaaaaaa-1111-2222-3333-444444444444";
const ANON = "bbbbbbbb-1111-2222-3333-444444444444";

function fleet() {
  return fakeStage({
    nodes: [
      node("node-1", "fed-d", { lastSeenAt: ts(-3000) }),
      node("node-2", "mac-a", { lastSeenAt: ts(-12_000) }),
      node("node-3", "lab-1", { siteId: "site-lab", siteName: "lab", partnerName: "lab-co", lastSeenAt: ts(-1000) }),
      node("node-old", "retired-box", { status: NodeStatus.RETIRED }),
    ],
    instances: [
      instance(WEB, InstancePhase.RUNNING, { name: "web-1", nodeId: "node-1", createdAt: ts(-600_000), phaseChangedAt: ts(-125_000) }),
      instance(ANON, InstancePhase.PROVISIONING, { nodeId: "node-3", createdAt: ts(-60_000), phaseChangedAt: ts(-20_000) }),
      instance("cccccccc-0000-0000-0000-000000000000", InstancePhase.TERMINATED, { name: "gone", nodeId: "node-1", createdAt: ts(-900_000) }),
    ],
  });
}

function renderStage(api: FakeStage, { search = "", pollMs = 30 }: { search?: string; pollMs?: number } = {}) {
  const client = createQueryClient();
  return render(
    <ApiProvider transport={api.transport}>
      <QueryClientProvider client={client}>
        <MemoryRouter initialEntries={[`/stage${search}`]}>
          <Routes>
            <Route path="/stage" element={<StagePage pollMs={pollMs} />} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>
    </ApiProvider>,
  );
}

const main = () => screen.getByRole("main");
/** The list item of the node (or site) with this name. */
const itemOf = (name: string) => screen.getByText(name).closest("li") as HTMLElement;

beforeEach(() => {
  // Only the clock stands still: real timers keep the polling and the waits going.
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(T0);
});
afterEach(() => {
  vi.useRealTimers();
});

describe("/stage", () => {
  it("lists nodes by site with status and last seen, and each node's unfinished instances with phase and age", async () => {
    renderStage(fleet());
    await screen.findByText("fed-d");

    // Sites by name, each with its partner.
    const sites = within(main()).getAllByText(/^(demo|lab)$/, { selector: "strong" }).map((e) => e.textContent);
    expect(sites).toEqual(["demo", "lab"]);
    expect(itemOf("demo").textContent).toContain("demo-dc");

    const fedD = itemOf("fed-d");
    expect(fedD.textContent).toContain("fed-d: ready, last seen 3 s ago");
    expect(within(fedD).getByText(`web-1 (${WEB}): running for 2 min 05 s`)).toBeTruthy();

    expect(itemOf("mac-a").textContent).toContain("last seen 12 s ago");
    expect(within(itemOf("mac-a")).getByText("No instances.")).toBeTruthy();

    // The unnamed instance is on the lab node, in its phase for 20 s.
    expect(within(itemOf("lab-1")).getByText(`${ANON}: provisioning for 20 s`)).toBeTruthy();
  });

  it("leaves out ended instances and retired nodes, and asks the server for exactly that", async () => {
    const api = fleet();
    renderStage(api);
    await screen.findByText("fed-d");

    expect(screen.queryByText(/gone/)).toBeNull();
    expect(screen.queryByText("retired-box")).toBeNull();
    expect(api.calls.listNodes[0]).toHaveLength(6);
    expect(api.calls.listNodes[0]).not.toContain(NodeStatus.RETIRED);
    expect(api.calls.listInstances[0]).toEqual({ includeTerminal: false, pageSize: 200 });
  });

  it("?demo=1 shows short names for the projector", async () => {
    renderStage(fleet(), { search: "?demo=1" });
    await screen.findByText("fed-d");

    expect(within(itemOf("fed-d")).getByText("web-1: running for 2 min 05 s")).toBeTruthy();
    expect(within(itemOf("lab-1")).getByText("bbbbbbbb: provisioning for 20 s")).toBeTruthy();
    expect(main().textContent).not.toContain(WEB);
    expect(main().textContent).not.toContain(ANON);
  });

  it("follows a drill without a reload: node and instance go unreachable, then recover", async () => {
    const api = fleet();
    renderStage(api);
    await screen.findByText("fed-d");
    expect(within(itemOf("fed-d")).getByText(/: running for/)).toBeTruthy();

    // The agent stops heartbeating; the watchdog marks the node, then its instances.
    api.setNode("node-1", { status: NodeStatus.UNREACHABLE, statusReason: "heartbeat lost" });
    api.setInstance(WEB, { phase: InstancePhase.UNREACHABLE, phaseReason: "node unreachable", phaseChangedAt: ts(0) });
    // The nodes and the instances are two polls, so each line may change a moment apart: wait for both.
    await waitFor(() => {
      expect(itemOf("fed-d").textContent).toContain("fed-d: unreachable (heartbeat lost)");
      expect(within(itemOf("fed-d")).getByText(`web-1 (${WEB}): unreachable (node unreachable) for 0 s`)).toBeTruthy();
    });

    // The agent is back.
    api.setNode("node-1", { status: NodeStatus.READY, statusReason: "" });
    // The control plane's own wording for a recovered instance; the page does not repeat it.
    api.setInstance(WEB, { phase: InstancePhase.RUNNING, phaseReason: "node_back", phaseChangedAt: ts(0) });
    await waitFor(() => {
      expect(itemOf("fed-d").textContent).toContain("fed-d: ready, last seen");
      expect(within(itemOf("fed-d")).getByText(`web-1 (${WEB}): running for 0 s`)).toBeTruthy();
    });
  });

  it("moves an instance to its new node and drops one that ended", async () => {
    const api = fleet();
    renderStage(api);
    await screen.findByText("fed-d");

    api.setInstance(WEB, { nodeId: "node-2" });
    await waitFor(() => expect(within(itemOf("mac-a")).getByText(/^web-1/)).toBeTruthy());
    expect(within(itemOf("fed-d")).getByText("No instances.")).toBeTruthy();

    api.setInstance(WEB, { phase: InstancePhase.TERMINATED });
    await waitFor(() => expect(screen.queryByText(/^web-1/)).toBeNull());
  });

  it("keeps ages counting between polls", async () => {
    renderStage(fleet(), { pollMs: 60_000 }); // no poll will land during the test
    await screen.findByText("fed-d");
    expect(main().textContent).toContain("running for 2 min 05 s");
    expect(main().textContent).toContain("last seen 3 s ago");

    vi.setSystemTime(T0 + 10_000);
    await waitFor(() => expect(main().textContent).toContain("running for 2 min 15 s"));
    expect(main().textContent).toContain("last seen 13 s ago");
  });

  it("sets aside instances with no node yet and those on a node it does not list", async () => {
    const api = fleet();
    api.addInstance(instance("dddddddd-0000-0000-0000-000000000000", InstancePhase.PENDING, { name: "queued", nodeId: "", createdAt: ts(-30_000), phaseChangedAt: ts(-30_000) }));
    api.addInstance(instance("eeeeeeee-0000-0000-0000-000000000000", InstancePhase.RUNNING, { name: "ghost", nodeId: "node-gone-9999", createdAt: ts(-40_000), phaseChangedAt: ts(-40_000) }));
    renderStage(api, { search: "?demo=1" });

    const section = (await screen.findByRole("heading", { name: "Not on a listed node" })).closest("section") as HTMLElement;
    expect(within(section).getByText("queued: pending for 30 s")).toBeTruthy();
    expect(within(section).getByText("ghost: running for 40 s, on node node-gon")).toBeTruthy();
  });

  it("says when no node has enrolled", async () => {
    renderStage(fakeStage());
    expect(await screen.findByText("No nodes have enrolled yet.")).toBeTruthy();
  });

  it("says when there are more unfinished instances than one page", async () => {
    renderStage(fakeStage({ nodes: [node("node-1", "fed-d")], moreInstances: true }));
    expect(await screen.findByText("Showing the newest 200 unfinished instances.")).toBeTruthy();
  });

  it("shows the first failure with a retry that works", async () => {
    const api = fleet();
    api.fail.listNodes = new ConnectError("kondukter is starting", Code.Unavailable);
    renderStage(api);
    expect((await screen.findByRole("alert")).textContent).toContain("kondukter is starting");

    delete api.fail.listNodes;
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByText("fed-d")).toBeTruthy();
  });

  it("keeps showing the last fleet, says so, and recovers, when contact is lost mid-drill", async () => {
    const api = fleet();
    renderStage(api);
    await screen.findByText("fed-d");

    // The control plane restarts (failure drill 2).
    api.fail.listNodes = new ConnectError("connection refused", Code.Unavailable);
    const banner = await screen.findByRole("alert");
    expect(banner.textContent).toContain("Lost contact with Kondukter (connection refused)");
    expect(banner.textContent).toContain(`Showing what it said at ${formatClock(T0)}`);
    expect(screen.getByText("fed-d")).toBeTruthy(); // not blanked
    expect(within(itemOf("fed-d")).getByText(/^web-1/)).toBeTruthy();

    delete api.fail.listNodes;
    await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
    expect(screen.getByText("fed-d")).toBeTruthy();
  });

  it("shows when it last updated and how often it asks", async () => {
    renderStage(fleet(), { pollMs: 2000 });
    expect((await screen.findByText(/^Updated /)).textContent).toBe(`Updated ${formatClock(T0)}, every 2 s.`);
  });
});

describe("/stage in the app", () => {
  it("opens for the operator without the area header, and honors ?demo=1", async () => {
    renderApp("/stage?demo=1", fleet());
    expect(await screen.findByRole("heading", { name: "Fleet" })).toBeTruthy();
    expect(await screen.findByText("fed-d")).toBeTruthy();
    expect(within(itemOf("fed-d")).getByText("web-1: running for 2 min 05 s")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Log out" })).toBeNull();
    expect(screen.getByTestId("location").textContent).toBe("/stage?demo=1");
  });
});
