import { Code, ConnectError } from "@connectrpc/connect";
import { InstancePhase, NodeStatus } from "@gen/kondukter/api/v1/types_pb";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { instance } from "../../test/fakeApi";
import { auditEntry, fakeFleet, node, site } from "../../test/fakeFleet";
import { renderApp } from "../../test/fakeServer";

const row = (name: RegExp) => screen.getByRole("row", { name });
const findRow = (name: RegExp) => screen.findByRole("row", { name });
const location = () => screen.getByTestId("location").textContent;
const asOperator = (opts: Omit<Parameters<typeof fakeFleet>[0] & object, "as"> = {}) => fakeFleet({ as: "operator", ...opts });

const sites = () => [site("site-demo", "demo"), site("site-other", "other", { partnerId: "partner-other", region: "other" })];
const fleetNodes = () => [
  node("n1", "sim-amd64-00", { allocatedVcpu: 4, desiredGeneration: 7n, observedGeneration: 5n }),
  node("n2", "sim-arm64-00", { arch: "arm64", status: NodeStatus.QUARANTINED, statusReason: "quarantined by operator@demo.local" }),
  node("n3", "other-00", { siteId: "site-other", siteName: "other", partnerId: "partner-other", partnerName: "other-dc" }),
];

describe("/admin/nodes", () => {
  it("shows every partner's nodes with partner, generation lag and a link to their instances", async () => {
    renderApp("/admin/nodes", asOperator({ sites: sites(), nodes: fleetNodes() }));

    const a = within(await findRow(/sim-amd64-00/));
    expect(a.getByText("demo-dc")).toBeTruthy();
    expect(a.getByText("2 behind (desired 7, observed 5)")).toBeTruthy();
    expect(a.getByRole("link", { name: "2" }).getAttribute("href")).toBe("/admin/instances?node=n1");
    expect(a.getByRole("button", { name: "Quarantine" })).toBeTruthy();
    expect(a.getByRole("button", { name: "Drain" })).toBeTruthy();

    const q = within(row(/sim-arm64-00/));
    expect(q.getByText("quarantined (quarantined by operator@demo.local)")).toBeTruthy();
    expect(q.getByRole("button", { name: "Unquarantine" })).toBeTruthy();
    expect(q.queryByRole("button", { name: "Quarantine" })).toBeNull();
    expect(q.queryByRole("button", { name: "Drain" })).toBeNull();
    expect(q.queryByRole("link")).toBeNull();

    const o = within(row(/other-00/));
    expect(o.getByText("other-dc")).toBeTruthy();
    expect(o.getByText("0 (4)")).toBeTruthy();
  });

  it("quarantines a node with a reason after a confirmation and writes the audit log", async () => {
    const fleet = asOperator({ nodes: fleetNodes() });
    renderApp("/admin/nodes", fleet);

    fireEvent.click(within(await findRow(/sim-amd64-00/)).getByRole("button", { name: "Quarantine" }));
    const ask = screen.getByRole("group", { name: /^Quarantine sim-amd64-00\?/ });
    expect(ask.textContent).toContain("Instances on it keep running (active: 2)");
    expect(fleet.calls.quarantineNode).toEqual([]);
    fireEvent.change(within(ask).getByLabelText("Reason (optional, kept in the audit log)"), { target: { value: " bad gpu " } });
    fireEvent.click(within(ask).getByRole("button", { name: "Quarantine" }));

    await waitFor(() => expect(fleet.calls.quarantineNode).toHaveLength(1));
    expect(fleet.calls.quarantineNode[0]).toMatchObject({ nodeId: "n1", reason: "bad gpu" });
    expect(fleet.calls.quarantineNode[0]?.idempotencyKey).toBeTruthy();
    const r = within(row(/sim-amd64-00/));
    expect(await r.findByText("quarantined (bad gpu)")).toBeTruthy();
    expect(r.getByRole("button", { name: "Unquarantine" })).toBeTruthy();
    expect(fleet.audit.map((e) => [e.action, e.target, e.actorDisplay])).toEqual([
      ["admin.quarantine_node", "node/n1", "operator@demo.local"],
    ]);
  });

  it("unquarantines a quarantined node", async () => {
    const fleet = asOperator({ nodes: fleetNodes() });
    renderApp("/admin/nodes", fleet);

    fireEvent.click(within(await findRow(/sim-arm64-00/)).getByRole("button", { name: "Unquarantine" }));
    const ask = screen.getByRole("group", { name: "Unquarantine sim-arm64-00? It becomes ready, or unreachable until its agent connects." });
    fireEvent.click(within(ask).getByRole("button", { name: "Unquarantine" }));
    expect(await within(row(/sim-arm64-00/)).findByText("ready")).toBeTruthy();
    expect(fleet.calls.unquarantineNode.map((c) => c.nodeId)).toEqual(["n2"]);
  });

  it("keeps the question open with the server's reason when a quarantine is refused", async () => {
    const fleet = asOperator({ nodes: fleetNodes() });
    fleet.fail.quarantineNode = new ConnectError("node is retiring", Code.FailedPrecondition);
    renderApp("/admin/nodes", fleet);

    fireEvent.click(within(await findRow(/sim-amd64-00/)).getByRole("button", { name: "Quarantine" }));
    const ask = screen.getByRole("group", { name: /^Quarantine sim-amd64-00\?/ });
    fireEvent.click(within(ask).getByRole("button", { name: "Quarantine" }));
    expect((await within(ask).findByRole("alert")).textContent).toContain("node is retiring");
    expect(within(row(/sim-amd64-00/)).getByText("ready")).toBeTruthy();
  });

  it("filters by status and site through the URL, which ListNodes receives", async () => {
    const fleet = asOperator({ sites: sites(), nodes: fleetNodes() });
    renderApp("/admin/nodes", fleet);
    await findRow(/other-00/);

    fireEvent.change(screen.getByLabelText("Status"), { target: { value: "quarantined" } });
    await waitFor(() => expect(screen.queryByRole("row", { name: /sim-amd64-00/ })).toBeNull());
    expect(row(/sim-arm64-00/)).toBeTruthy();
    expect(location()).toBe("/admin/nodes?status=quarantined");
    expect(fleet.calls.adminListNodes.at(-1)).toEqual({ siteId: "", statuses: [NodeStatus.QUARANTINED] });

    fireEvent.change(screen.getByLabelText("Status"), { target: { value: "" } });
    fireEvent.change(screen.getByLabelText("Site"), { target: { value: "site-other" } });
    await waitFor(() => expect(screen.queryByRole("row", { name: /sim-arm64-00/ })).toBeNull());
    expect(row(/other-00/)).toBeTruthy();
    expect(location()).toBe("/admin/nodes?site=site-other");
    expect(fleet.calls.adminListNodes.at(-1)).toEqual({ siteId: "site-other", statuses: [] });
  });

  it("says when no node matches the filter", async () => {
    renderApp("/admin/nodes?status=retired", asOperator({ nodes: fleetNodes() }));
    expect(await screen.findByText("No nodes match the filter.")).toBeTruthy();
    expect((screen.getByLabelText("Status") as HTMLSelectElement).value).toBe("retired");
  });
});

const orgInstances = () => [
  instance("i-acme-1", InstancePhase.RUNNING, { name: "web", orgId: "org-acme", orgName: "acme", nodeId: "n1" }),
  instance("i-acme-2", InstancePhase.PROVISIONING, { orgId: "org-acme", orgName: "acme", nodeId: "n3" }),
  instance("i-beta-1", InstancePhase.RUNNING, { name: "train", orgId: "org-beta", orgName: "beta", nodeId: "n3" }),
  instance("i-old", InstancePhase.TERMINATED, {
    name: "gone",
    orgId: "org-acme",
    orgName: "acme",
    nodeId: "n1",
    phaseReason: "terminated by customer",
  }),
];

describe("/admin/instances", () => {
  it("lists every org's live instances with org, phase, node hostname and offer", async () => {
    const fleet = asOperator({ nodes: fleetNodes(), instances: orgInstances() });
    renderApp("/admin/instances", fleet);

    const web = within(await findRow(/i-acme-1/));
    expect(web.getByRole("button", { name: "acme" })).toBeTruthy();
    expect(web.getByText("running")).toBeTruthy();
    expect(await web.findByRole("button", { name: "sim-amd64-00" })).toBeTruthy();
    expect(web.getByText("cpu-2x2, €0.05/h")).toBeTruthy();
    expect(web.getByText(/^\d+ (s|min \d\d s|h \d\d min)$/)).toBeTruthy();
    expect(within(row(/i-beta-1/)).getByRole("button", { name: "beta" })).toBeTruthy();
    expect(screen.queryByRole("row", { name: /i-old/ })).toBeNull();
    expect(fleet.calls.listInstances[0]).toMatchObject({ includeTerminal: false, nodeId: "", orgId: "", pageSize: 200 });
  });

  it("adds ended instances with ?all=1", async () => {
    const fleet = asOperator({ nodes: fleetNodes(), instances: orgInstances() });
    renderApp("/admin/instances", fleet);
    await findRow(/i-acme-1/);

    fireEvent.click(screen.getByLabelText("Show terminated, failed and lost instances"));
    const old = within(await findRow(/i-old/));
    expect(old.getByText("terminated (terminated by customer)")).toBeTruthy();
    expect(old.getByRole("button", { name: "Force terminate" }).hasAttribute("disabled")).toBe(true);
    expect(location()).toBe("/admin/instances?all=1");
    expect(fleet.calls.listInstances.at(-1)?.includeTerminal).toBe(true);
  });

  it("narrows to one org or one node from the table, and clears it again", async () => {
    const fleet = asOperator({ nodes: fleetNodes(), instances: orgInstances() });
    renderApp("/admin/instances", fleet);

    fireEvent.click(within(await findRow(/i-beta-1/)).getByRole("button", { name: "beta" }));
    await waitFor(() => expect(screen.queryByRole("row", { name: /i-acme-1/ })).toBeNull());
    expect(location()).toBe("/admin/instances?org=org-beta");
    expect(screen.getByText(/Org: beta/)).toBeTruthy();
    expect(fleet.calls.listInstances.at(-1)?.orgId).toBe("org-beta");

    fireEvent.click(screen.getByRole("button", { name: "Any org" }));
    fireEvent.click(within(await findRow(/i-acme-2/)).getByRole("button", { name: "other-00" }));
    await waitFor(() => expect(screen.queryByRole("row", { name: /i-acme-1/ })).toBeNull());
    expect(row(/i-beta-1/)).toBeTruthy();
    expect(location()).toBe("/admin/instances?node=n3");
    expect(screen.getByText(/Node: other-00/)).toBeTruthy();
    expect(fleet.calls.listInstances.at(-1)?.nodeId).toBe("n3");
  });

  it("opens a node's instances from the nodes page", async () => {
    const fleet = asOperator({ nodes: fleetNodes(), instances: orgInstances() });
    renderApp("/admin/nodes", fleet);
    fireEvent.click(within(await findRow(/sim-amd64-00/)).getByRole("link", { name: "2" }));
    expect(await screen.findByRole("heading", { name: "Instances" })).toBeTruthy();
    expect(await findRow(/i-acme-1/)).toBeTruthy();
    expect(screen.queryByRole("row", { name: /i-beta-1/ })).toBeNull();
    expect(location()).toBe("/admin/instances?node=n1");
  });

  it("force terminates with a reason after a confirmation", async () => {
    const fleet = asOperator({ nodes: fleetNodes(), instances: orgInstances() });
    renderApp("/admin/instances", fleet);

    fireEvent.click(within(await findRow(/i-beta-1/)).getByRole("button", { name: "Force terminate" }));
    const ask = screen.getByRole("group", { name: "Force terminate train of beta? Its VM is deleted now and the customer cannot undo it." });
    expect(fleet.calls.forceTerminateInstance).toEqual([]);
    fireEvent.change(within(ask).getByLabelText("Reason (optional, kept in the audit log)"), { target: { value: "abuse report" } });
    fireEvent.click(within(ask).getByRole("button", { name: "Force terminate" }));

    const r = within(row(/i-beta-1/));
    expect(await r.findByText("terminating (force-terminated by operator@demo.local: abuse report)")).toBeTruthy();
    expect(r.getByRole("button", { name: "Force terminate" }).hasAttribute("disabled")).toBe(true);
    expect(fleet.calls.forceTerminateInstance).toEqual([
      { instanceId: "i-beta-1", reason: "abuse report", idempotencyKey: expect.any(String) as string },
    ]);
    expect(fleet.audit.map((e) => e.action)).toEqual(["admin.force_terminate_instance"]);
  });

  it("says when more instances exist than one page holds", async () => {
    const many = Array.from({ length: 201 }, (_, n) => instance(`i-${String(n).padStart(3, "0")}`, InstancePhase.RUNNING, { orgId: "org-acme", orgName: "acme" }));
    renderApp("/admin/instances", asOperator({ instances: many }));
    expect(await screen.findByText(/The newest 200 are shown; narrow by org or node to see others\./)).toBeTruthy();
    expect(screen.getAllByRole("row")).toHaveLength(201);
  });

  it("says when there are none", async () => {
    renderApp("/admin/instances", asOperator());
    expect(await screen.findByText("No active instances.")).toBeTruthy();
  });
});

describe("/admin/catalog", () => {
  it("shows offers, shapes, images and regions", async () => {
    renderApp("/admin/catalog", asOperator());
    expect(await screen.findByRole("heading", { name: "Offers (2)" })).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Shapes (2)" })).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Images (3)" })).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Regions (1)" })).toBeTruthy();
    const gpuOffer = within(row(/€0\.40/));
    expect(gpuOffer.getByText("GeForce GTX 1060 6GB × 1")).toBeTruthy();
    expect(gpuOffer.getByText("dedicated")).toBeTruthy();
    expect(gpuOffer.getByText("amd64")).toBeTruthy();
    expect(within(row(/^cpu-2x2 2 2 GiB 20 GiB none$/)).getByText("2 GiB")).toBeTruthy();
    expect(within(row(/ubuntu-gpu-pascal/)).getByText("nvidia-580-cuda12")).toBeTruthy();
    expect(screen.getByText("the showcase region")).toBeTruthy();
  });

  it("shows the error with Retry", async () => {
    const fleet = asOperator();
    fleet.fail.listCatalog = new ConnectError("database is down", Code.Unavailable);
    renderApp("/admin/catalog", fleet);
    expect((await screen.findByText("database is down")).closest("[role=alert]")).toBeTruthy();
    delete fleet.fail.listCatalog;
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByRole("heading", { name: "Offers (2)" })).toBeTruthy();
  });
});

describe("/admin/audit", () => {
  const log = () => [
    auditEntry("1", "partner.drain_node", "node/7f3c1a2b-0000-4000-8000-000000000001", { payload: { reason: "kernel update" } }),
    auditEntry("2", "admin.quarantine_node", "node/7f3c1a2b-0000-4000-8000-000000000001", {
      actorId: "user-operator",
      actorDisplay: "operator@demo.local",
    }),
    auditEntry("3", "instance.terminate", "instance/9a8b7c6d-0000-4000-8000-000000000002", {
      actorType: "api_key",
      actorId: "user-customer",
      actorDisplay: "customer@demo.local",
    }),
  ];

  it("lists entries newest first with actor, action, short target and payload", async () => {
    renderApp("/admin/audit", asOperator({ audit: log() }));
    await findRow(/instance\.terminate/);
    const rows = screen.getAllByRole("row").slice(1);
    expect(rows.map((r) => within(r).getAllByRole("cell")[2]?.textContent)).toEqual([
      "instance.terminate",
      "admin.quarantine_node",
      "partner.drain_node",
    ]);
    const t = within(row(/instance\.terminate/));
    expect(t.getByText("(api key)")).toBeTruthy();
    expect(t.getByText("instance/9a8b7c6d").getAttribute("title")).toBe("instance/9a8b7c6d-0000-4000-8000-000000000002");
    expect(within(row(/partner\.drain_node/)).getByText('{"reason":"kernel update"}')).toBeTruthy();
    expect(within(row(/admin\.quarantine_node/)).getByText("—")).toBeTruthy();
  });

  it("filters by action and by actor, both sent to ListAuditLog", async () => {
    const fleet = asOperator({ audit: log() });
    renderApp("/admin/audit", fleet);
    await findRow(/instance\.terminate/);

    fireEvent.change(screen.getByLabelText("Action"), { target: { value: "admin.quarantine_node" } });
    await waitFor(() => expect(screen.queryByRole("row", { name: /instance\.terminate/ })).toBeNull());
    expect(location()).toBe("/admin/audit?action=admin.quarantine_node");
    expect(fleet.calls.listAuditLog.at(-1)).toMatchObject({ action: "admin.quarantine_node", actorId: "", pageSize: 50 });

    fireEvent.change(screen.getByLabelText("Action"), { target: { value: "" } });
    fireEvent.click(within(await findRow(/partner\.drain_node/)).getByRole("button", { name: "partner@demo.local" }));
    await waitFor(() => expect(screen.queryByRole("row", { name: /admin\.quarantine_node/ })).toBeNull());
    expect(location()).toBe("/admin/audit?actor=user-partner");
    expect(screen.getByText(/Actor: partner@demo\.local/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Any actor" }));
    expect(await findRow(/admin\.quarantine_node/)).toBeTruthy();
  });

  it("pages back through older entries, and a filter change returns to the newest", async () => {
    const many = Array.from({ length: 60 }, (_, n) => auditEntry(String(n + 1), "partner.drain_node", `node/n${n + 1}`));
    const fleet = asOperator({ audit: many });
    renderApp("/admin/audit", fleet);
    await findRow(/node\/n60\b/);
    expect(screen.getAllByRole("row")).toHaveLength(51);
    expect(screen.queryByRole("button", { name: "Newest entries" })).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Older entries" }));
    expect(await findRow(/node\/n10\b/)).toBeTruthy();
    expect(screen.getAllByRole("row")).toHaveLength(11);
    expect(location()).toBe("/admin/audit?page=50");
    expect(screen.queryByRole("button", { name: "Older entries" })).toBeNull();

    fireEvent.change(screen.getByLabelText("Action"), { target: { value: "partner.drain_node" } });
    expect(await findRow(/node\/n60\b/)).toBeTruthy();
    expect(location()).toBe("/admin/audit?action=partner.drain_node");
    expect(fleet.calls.listAuditLog.at(-1)).toMatchObject({ action: "partner.drain_node", pageToken: "" });
  });

  it("says when the log is empty", async () => {
    renderApp("/admin/audit", asOperator());
    expect(await screen.findByText("The audit log is empty.")).toBeTruthy();
  });
});
