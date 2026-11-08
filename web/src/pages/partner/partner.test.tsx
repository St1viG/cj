import { timestampDate } from "@bufbuild/protobuf/wkt";
import { Code, ConnectError } from "@connectrpc/connect";
import { NodeStatus } from "@gen/kondukter/api/v1/types_pb";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { fakeFleet, gpu, node, site } from "../../test/fakeFleet";
import { renderApp } from "../../test/fakeServer";

const row = (name: RegExp) => screen.getByRole("row", { name });
const findRow = (name: RegExp) => screen.findByRole("row", { name });
const location = () => screen.getByTestId("location").textContent;

const twoSites = () => [site("site-demo", "demo"), site("site-lab", "lab", { region: "lab", countryCode: "" })];

describe("/partner/sites", () => {
  it("lists the sites with their nodes counted by status, and links to nodes and enrollment", async () => {
    const fleet = fakeFleet({
      sites: twoSites(),
      nodes: [
        node("n1", "sim-amd64-00"),
        node("n2", "sim-amd64-01"),
        node("n3", "sim-arm64-00", { status: NodeStatus.UNREACHABLE }),
        node("n4", "lab-1", { siteId: "site-lab", siteName: "lab" }),
      ],
    });
    renderApp("/partner/sites", fleet);

    const demo = within(await findRow(/^demo/));
    expect(demo.getByText("3")).toBeTruthy();
    expect(demo.getByText("ready 2, unreachable 1")).toBeTruthy();
    expect(demo.getByText("port-map")).toBeTruthy();
    expect(demo.getByRole("link", { name: "Nodes" }).getAttribute("href")).toBe("/partner/nodes?site=site-demo");
    expect(demo.getByRole("link", { name: "Enroll a node" }).getAttribute("href")).toBe("/partner/enroll?site=site-demo");
    expect(within(row(/^lab/)).getByText("ready 1")).toBeTruthy();
  });

  it("says when there are none", async () => {
    renderApp("/partner/sites", fakeFleet({ sites: [] }));
    expect(await screen.findByText("No sites yet. The operator creates a partner's sites.")).toBeTruthy();
  });

  it("shows the error with Retry", async () => {
    const fleet = fakeFleet();
    fleet.fail.listSites = new ConnectError("database is down", Code.Unavailable);
    renderApp("/partner/sites", fleet);
    expect((await screen.findByText("database is down")).closest("[role=alert]")).toBeTruthy();
    delete fleet.fail.listSites;
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(await findRow(/^demo/)).toBeTruthy();
  });
});

describe("/partner/nodes", () => {
  it("shows node facts: status, arch, driver, free of total, accelerators, instances, last seen", async () => {
    const fleet = fakeFleet({
      nodes: [
        node("n1", "sim-gpu-00", { allocatedVcpu: 2, accelerators: [gpu("GeForce GTX 1060 6GB", true)] }),
        node("n2", "sim-arm64-00", { arch: "arm64", status: NodeStatus.UNREACHABLE, statusReason: "missed heartbeats" }),
      ],
    });
    renderApp("/partner/nodes", fleet);

    const g = within(await findRow(/sim-gpu-00/));
    expect(g.getByText("ready")).toBeTruthy();
    expect(g.getByText("mock")).toBeTruthy();
    expect(g.getByText("6 of 8")).toBeTruthy();
    expect(g.getByText("14 GiB of 16 GiB")).toBeTruthy();
    expect(g.getByText("180 GiB of 200 GiB")).toBeTruthy();
    expect(g.getByText("GeForce GTX 1060 6GB (ok, in use)")).toBeTruthy();
    expect(g.getByText("1")).toBeTruthy();
    expect(g.getByText(/^\d+ s ago$/)).toBeTruthy();

    const a = within(row(/sim-arm64-00/));
    expect(a.getByText("unreachable (missed heartbeats)")).toBeTruthy();
    expect(a.getByText("arm64")).toBeTruthy();
    expect(a.getByText("none")).toBeTruthy();
    // Neither Drain nor Undrain fits an unreachable node.
    expect(a.queryByRole("button")).toBeNull();
  });

  it("filters by site through ?site=, which ListNodes receives", async () => {
    const fleet = fakeFleet({
      sites: twoSites(),
      nodes: [node("n1", "sim-amd64-00"), node("n4", "lab-1", { siteId: "site-lab", siteName: "lab" })],
    });
    renderApp("/partner/nodes", fleet);
    await findRow(/lab-1/);

    fireEvent.change(screen.getByLabelText("Site"), { target: { value: "site-lab" } });
    await waitFor(() => expect(screen.queryByRole("row", { name: /sim-amd64-00/ })).toBeNull());
    expect(row(/lab-1/)).toBeTruthy();
    expect(location()).toBe("/partner/nodes?site=site-lab");
    expect(fleet.calls.listNodes.at(-1)).toBe("site-lab");
  });

  it("drops a chosen site when a link opens the page without one", async () => {
    const fleet = fakeFleet({
      sites: twoSites(),
      nodes: [node("n1", "sim-amd64-00"), node("n4", "lab-1", { siteId: "site-lab", siteName: "lab" })],
    });
    renderApp("/partner/nodes", fleet);
    await findRow(/lab-1/);
    fireEvent.change(screen.getByLabelText("Site"), { target: { value: "site-lab" } });
    await waitFor(() => expect(location()).toBe("/partner/nodes?site=site-lab"));
    await waitFor(() => expect(screen.queryByRole("row", { name: /sim-amd64-00/ })).toBeNull());

    fireEvent.click(within(screen.getByRole("navigation")).getByRole("link", { name: "Nodes" }));
    await waitFor(() => expect(location()).toBe("/partner/nodes"));
    expect(await findRow(/sim-amd64-00/)).toBeTruthy();
    expect((screen.getByLabelText("Site") as HTMLSelectElement).value).toBe("");
  });

  it("opens with the site from the link", async () => {
    const fleet = fakeFleet({ sites: twoSites(), nodes: [node("n4", "lab-1", { siteId: "site-lab", siteName: "lab" })] });
    renderApp("/partner/nodes?site=site-lab", fleet);
    await findRow(/lab-1/);
    expect((screen.getByLabelText("Site") as HTMLSelectElement).value).toBe("site-lab");
    expect(fleet.calls.listNodes).toEqual(["site-lab"]);
  });

  it("drains a ready node with a reason after a confirmation, then offers Undrain", async () => {
    const fleet = fakeFleet({ nodes: [node("n1", "sim-amd64-00")] });
    renderApp("/partner/nodes", fleet);

    fireEvent.click(within(await findRow(/sim-amd64-00/)).getByRole("button", { name: "Drain" }));
    expect(screen.getByText("Drain sim-amd64-00? No new instances are placed on it; running ones keep running.")).toBeTruthy();
    expect(fleet.calls.drainNode).toEqual([]);
    fireEvent.change(screen.getByLabelText("Reason (optional, kept in the audit log)"), { target: { value: " kernel update " } });
    fireEvent.click(within(row(/sim-amd64-00/)).getByRole("button", { name: "Drain" }));

    await waitFor(() => expect(fleet.calls.drainNode).toHaveLength(1));
    expect(fleet.calls.drainNode[0]?.reason).toBe("kernel update");
    expect(fleet.calls.drainNode[0]?.idempotencyKey).toBeTruthy();
    const r = within(row(/sim-amd64-00/));
    expect(await r.findByText("draining (kernel update)")).toBeTruthy();
    expect(r.getByRole("button", { name: "Undrain" })).toBeTruthy();
    expect(r.queryByRole("button", { name: "Drain" })).toBeNull();
  });

  it("cancelling the confirmation drains nothing", async () => {
    const fleet = fakeFleet({ nodes: [node("n1", "sim-amd64-00")] });
    renderApp("/partner/nodes", fleet);
    fireEvent.click(within(await findRow(/sim-amd64-00/)).getByRole("button", { name: "Drain" }));
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(within(row(/sim-amd64-00/)).getByRole("button", { name: "Drain" })).toBeTruthy();
    expect(fleet.calls.drainNode).toEqual([]);
  });

  it("undrains a node in maintenance", async () => {
    const fleet = fakeFleet({ nodes: [node("n1", "sim-amd64-00", { status: NodeStatus.MAINTENANCE, statusReason: "drained by partner@demo.local" })] });
    renderApp("/partner/nodes", fleet);

    fireEvent.click(within(await findRow(/sim-amd64-00/)).getByRole("button", { name: "Undrain" }));
    expect(screen.getByText("Undrain sim-amd64-00? It becomes ready and takes new instances again.")).toBeTruthy();
    fireEvent.click(within(row(/sim-amd64-00/)).getByRole("button", { name: "Undrain" }));

    await waitFor(() => expect(fleet.calls.undrainNode).toEqual(["n1"]));
    expect(await within(row(/sim-amd64-00/)).findByText("ready")).toBeTruthy();
  });

  it("keeps the question open with the server's reason when a drain is refused", async () => {
    const fleet = fakeFleet({ nodes: [node("n1", "sim-amd64-00")] });
    fleet.fail.drainNode = new ConnectError("node is not ready", Code.FailedPrecondition);
    renderApp("/partner/nodes", fleet);
    fireEvent.click(within(await findRow(/sim-amd64-00/)).getByRole("button", { name: "Drain" }));
    fireEvent.click(within(row(/sim-amd64-00/)).getByRole("button", { name: "Drain" }));
    expect((await within(row(/sim-amd64-00/)).findByRole("alert")).textContent).toContain("node is not ready");
    expect(screen.getByText(/^Drain sim-amd64-00\?/)).toBeTruthy();
  });

  it("points to enrollment when there are no nodes", async () => {
    renderApp("/partner/nodes", fakeFleet());
    const empty = (await screen.findByText(/No nodes yet/)).closest("p");
    expect(empty && within(empty).getByRole("link", { name: "Enroll a node" }).getAttribute("href")).toBe("/partner/enroll");
  });
});

describe("/partner/enroll", () => {
  it("creates a token for the site from the link and shows its command once", async () => {
    const fleet = fakeFleet({ sites: twoSites() });
    renderApp("/partner/enroll?site=site-lab", fleet);

    const picker = (await screen.findByLabelText("Site")) as HTMLSelectElement;
    expect(picker.value).toBe("site-lab");
    fireEvent.click(screen.getByRole("button", { name: "Create enrollment token" }));

    const box = within(await screen.findByRole("region", { name: "Enrollment command" }));
    expect(box.getByTestId("enroll-command").textContent).toBe(
      "sudo deploy/nodes/install-agent.sh --server http://kondukter.test --token kdk_enroll_token1",
    );
    expect(box.getByText(/Token for site lab created\. Copy the command now: it is shown only once, works once/)).toBeTruthy();
    expect(box.getByRole("button", { name: "Copy" })).toBeTruthy();
    expect(fleet.calls.createEnrollmentToken[0]?.siteId).toBe("site-lab");

    fireEvent.click(box.getByRole("button", { name: "Done" }));
    expect(screen.queryByRole("region", { name: "Enrollment command" })).toBeNull();
    expect(screen.queryByText(/kdk_enroll_token1/)).toBeNull();
  });

  it("uses a fresh idempotency key for every token", async () => {
    const fleet = fakeFleet();
    renderApp("/partner/enroll", fleet);
    await screen.findByLabelText("Site");
    for (let n = 1; n <= 2; n++) {
      fireEvent.click(screen.getByRole("button", { name: "Create enrollment token" }));
      await waitFor(() => expect(screen.getByTestId("enroll-command").textContent).toContain(`kdk_enroll_token${n}`));
    }
    const [a, b] = fleet.calls.createEnrollmentToken.map((c) => c.idempotencyKey);
    expect(a).toBeTruthy();
    expect(b).not.toBe(a);
  });

  it("shows the server's reason when the token is refused", async () => {
    const fleet = fakeFleet();
    fleet.fail.createEnrollmentToken = new ConnectError("your role may not call this service", Code.PermissionDenied);
    renderApp("/partner/enroll", fleet);
    await screen.findByLabelText("Site");
    fireEvent.click(screen.getByRole("button", { name: "Create enrollment token" }));
    expect((await screen.findByRole("alert")).textContent).toBe("your role may not call this service");
  });

  it("says when there is no site to enroll into", async () => {
    renderApp("/partner/enroll", fakeFleet({ sites: [] }));
    expect(await screen.findByText(/There is no site to enroll into yet/)).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Create enrollment token" })).toBeNull();
  });
});

describe("/partner/utilization", () => {
  it("shows allocated of allocatable per node and the total for the default period", async () => {
    const fleet = fakeFleet({
      nodes: [node("n1", "sim-amd64-00", { allocatedVcpu: 2 }), node("n2", "sim-gpu-00", { accelerators: [gpu()] })],
    });
    renderApp("/partner/utilization", fleet);

    const busy = within(await findRow(/sim-amd64-00/));
    expect(busy.getByText("2 of 8 (25%)")).toBeTruthy();
    expect(busy.getByText("2 GiB of 16 GiB (13%)")).toBeTruthy();
    expect(busy.getByText("90 min")).toBeTruthy();
    expect(within(row(/sim-gpu-00/)).getByText("0 of 1 (0%)")).toBeTruthy();
    expect(within(row(/sim-amd64-00/)).getByText("none")).toBeTruthy();
    expect(screen.getByTestId("utilization-total").textContent).toBe("90 min");
    expect(fleet.calls.getUtilization[0]?.periodStart).toBeUndefined();
  });

  it("asks for whole local days, To included", async () => {
    const fleet = fakeFleet({ nodes: [node("n1", "sim-amd64-00", { allocatedVcpu: 2 })] });
    renderApp("/partner/utilization", fleet);
    await findRow(/sim-amd64-00/);

    fireEvent.change(screen.getByLabelText("From"), { target: { value: "2020-01-01" } });
    fireEvent.change(screen.getByLabelText("To (included)"), { target: { value: "2020-01-02" } });
    fireEvent.click(screen.getByRole("button", { name: "Show period" }));

    await waitFor(() => expect(fleet.calls.getUtilization).toHaveLength(2));
    const req = fleet.calls.getUtilization[1];
    expect(req?.periodStart && timestampDate(req.periodStart)).toEqual(new Date(2020, 0, 1));
    expect(req?.periodEnd && timestampDate(req.periodEnd)).toEqual(new Date(2020, 0, 3));
    await waitFor(() => expect(screen.getByTestId("utilization-total").textContent).toBe("0 min"));
    expect(within(row(/sim-amd64-00/)).getByText("2 of 8 (25%)")).toBeTruthy();
  });
});
