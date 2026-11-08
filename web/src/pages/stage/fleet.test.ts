import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { InstancePhase } from "@gen/kondukter/api/v1/types_pb";
import { describe, expect, it } from "vitest";
import { instance } from "../../test/fakeApi";
import { node } from "../../test/fakeFleet";
import { groupFleet, instanceLabel, phaseText } from "./fleet";

const at = (iso: string) => timestampFromDate(new Date(iso));
const made = (id: string, nodeId: string, iso: string) => instance(id, InstancePhase.RUNNING, { nodeId, createdAt: at(iso) });

describe("groupFleet", () => {
  it("groups nodes under their site and puts each instance under its node", () => {
    const view = groupFleet(
      [node("n1", "fed-d"), node("n2", "mac-a"), node("n3", "lab-1", { siteId: "site-lab", siteName: "lab", partnerName: "lab-co" })],
      [made("i1", "n1", "2026-11-08T10:00:00Z"), made("i2", "n3", "2026-11-08T10:01:00Z"), made("i3", "n1", "2026-11-08T10:02:00Z")],
    );
    expect(view.sites.map((s) => [s.name, s.partner, s.nodes.map((n) => [n.node.hostname, n.instances.map((i) => i.id)])])).toEqual([
      ["demo", "demo-dc", [["fed-d", ["i1", "i3"]], ["mac-a", []]]],
      ["lab", "lab-co", [["lab-1", ["i2"]]]],
    ]);
    expect(view.elsewhere).toEqual([]);
  });

  it("orders sites and hostnames by name, with numbers read as numbers", () => {
    const view = groupFleet(
      [node("a", "node-10"), node("b", "node-2"), node("c", "node-1", { siteId: "site-a", siteName: "alpha" })],
      [],
    );
    expect(view.sites.map((s) => s.name)).toEqual(["alpha", "demo"]);
    expect(view.sites[1]?.nodes.map((n) => n.node.hostname)).toEqual(["node-2", "node-10"]);
  });

  it("lists instances oldest first, and by id when created together", () => {
    const view = groupFleet(
      [node("n1", "fed-d")],
      [made("b", "n1", "2026-11-08T10:00:00Z"), made("c", "n1", "2026-11-08T09:00:00Z"), made("a", "n1", "2026-11-08T10:00:00Z")],
    );
    expect(view.sites[0]?.nodes[0]?.instances.map((i) => i.id)).toEqual(["c", "a", "b"]);
  });

  it("keeps the order whatever order the server answers in", () => {
    const nodes = [node("n1", "fed-d"), node("n2", "mac-a")];
    const insts = [made("i1", "n1", "2026-11-08T10:00:00Z"), made("i2", "n2", "2026-11-08T10:01:00Z")];
    expect(groupFleet([...nodes].reverse(), [...insts].reverse())).toEqual(groupFleet(nodes, insts));
  });

  it("sets aside instances with no node yet and those on a node that is not listed", () => {
    const view = groupFleet(
      [node("n1", "fed-d")],
      [made("queued", "", "2026-11-08T10:00:00Z"), made("ghost", "n-gone", "2026-11-08T10:01:00Z"), made("ok", "n1", "2026-11-08T10:02:00Z")],
    );
    expect(view.elsewhere.map((i) => i.id)).toEqual(["queued", "ghost"]);
    expect(view.sites[0]?.nodes[0]?.instances.map((i) => i.id)).toEqual(["ok"]);
  });

  it("handles an empty fleet", () => {
    expect(groupFleet([], [])).toEqual({ sites: [], elsewhere: [] });
  });
});

describe("phaseText", () => {
  const withReason = (phase: InstancePhase, phaseReason: string) => instance("i", phase, { phaseReason });

  it("gives a problem or a wait its reason, as the control plane words it", () => {
    expect(phaseText(withReason(InstancePhase.PENDING, "no_capacity"))).toBe("pending (no_capacity)");
    expect(phaseText(withReason(InstancePhase.UNREACHABLE, "node_lost"))).toBe("unreachable (node_lost)");
    expect(phaseText(withReason(InstancePhase.PROVISIONING, ""))).toBe("provisioning");
  });

  it("leaves running bare: its reason is only ever the normal one", () => {
    expect(phaseText(withReason(InstancePhase.RUNNING, "agent_running"))).toBe("running");
    // Also after a recovery, where the age ("running for 3 s") is what tells the story.
    expect(phaseText(withReason(InstancePhase.RUNNING, "node_back"))).toBe("running");
  });
});

describe("instanceLabel", () => {
  const named = instance("aaaaaaaa-1111-2222-3333-444444444444", InstancePhase.RUNNING, { name: "web-1" });
  const unnamed = instance("bbbbbbbb-1111-2222-3333-444444444444", InstancePhase.RUNNING);

  it("names it for the projector, with a short id when it has no name", () => {
    expect(instanceLabel(named, true)).toBe("web-1");
    expect(instanceLabel(unnamed, true)).toBe("bbbbbbbb");
  });

  it("gives the whole id otherwise", () => {
    expect(instanceLabel(named, false)).toBe("web-1 (aaaaaaaa-1111-2222-3333-444444444444)");
    expect(instanceLabel(unnamed, false)).toBe("bbbbbbbb-1111-2222-3333-444444444444");
  });
});
