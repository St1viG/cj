import { timestampMs } from "@bufbuild/protobuf/wkt";
import type { Instance } from "@gen/kondukter/api/v1/instance_pb";
import { InstancePhase, type Node } from "@gen/kondukter/api/v1/types_pb";
import { phaseLabel, shortId, withReason } from "../../lib/format";

/** One node and the instances on it, as the stage page lists them. */
export interface NodeGroup {
  node: Node;
  instances: Instance[];
}

export interface SiteGroup {
  /** The site id, or its name when the node carries no id. */
  key: string;
  name: string;
  partner: string;
  nodes: NodeGroup[];
}

export interface FleetView {
  sites: SiteGroup[];
  /** Instances on no node yet (pending), or on a node the list does not show. */
  elsewhere: Instance[];
}

const createdMs = (i: Instance) => (i.createdAt ? timestampMs(i.createdAt) : 0);

/** Oldest first, then by id, so rows keep their place from one poll to the next. */
function byCreated(a: Instance, b: Instance): number {
  return createdMs(a) - createdMs(b) || a.id.localeCompare(b.id);
}

const byName = (a: string, b: string) => a.localeCompare(b, undefined, { numeric: true });

/**
 * Nodes grouped by site, each with its instances. The order is stable (sites
 * and hostnames by name, instances oldest first), so a poll that changes a
 * status changes that line and moves nothing else.
 */
export function groupFleet(nodes: readonly Node[], instances: readonly Instance[]): FleetView {
  const known = new Set(nodes.map((n) => n.id));
  const onNode = new Map<string, Instance[]>();
  const elsewhere: Instance[] = [];
  for (const i of [...instances].sort(byCreated)) {
    if (i.nodeId && known.has(i.nodeId)) onNode.set(i.nodeId, [...(onNode.get(i.nodeId) ?? []), i]);
    else elsewhere.push(i);
  }

  const sites = new Map<string, SiteGroup>();
  for (const node of [...nodes].sort((a, b) => byName(a.hostname || a.id, b.hostname || b.id) || a.id.localeCompare(b.id))) {
    const key = node.siteId || node.siteName;
    let site = sites.get(key);
    if (!site) {
      site = { key, name: node.siteName || shortId(node.siteId), partner: node.partnerName, nodes: [] };
      sites.set(key, site);
    }
    site.nodes.push({ node, instances: onNode.get(node.id) ?? [] });
  }
  return { sites: [...sites.values()].sort((a, b) => byName(a.name, b.name)), elsewhere };
}

/**
 * The phase as the page words it. A phase that is a problem or a wait gets
 * the control plane's reason ("pending (no_capacity)", "unreachable (node_lost)");
 * `running` does not, because its reason is only the normal one ("agent_running").
 */
export function phaseText(i: Instance): string {
  return i.phase === InstancePhase.RUNNING ? phaseLabel(i.phase) : withReason(phaseLabel(i.phase), i.phaseReason);
}

/**
 * How the page names an instance. For the projector (`demo`) the name, or the
 * short id when it has none; otherwise the name with the full id, or the id.
 */
export function instanceLabel(i: Instance, demo: boolean): string {
  if (demo) return i.name || shortId(i.id);
  return i.name ? `${i.name} (${i.id})` : i.id;
}
