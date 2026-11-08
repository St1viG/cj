import type { Node } from "@gen/kondukter/api/v1/types_pb";
import { NodeStatus } from "@gen/kondukter/api/v1/types_pb";
import { nodeStatusLabel } from "../lib/format";
import { nodeKind, type StatusKind } from "./Status";

/** The node's fill: how much of its allocatable vCPU is held, 0 to 100. */
function fill(n: Node): number {
  const total = n.capacity?.total?.vcpu ?? 0;
  const reserved = n.capacity?.reserved?.vcpu ?? 0;
  const held = n.capacity?.allocated?.vcpu ?? 0;
  const allocatable = total - reserved;
  return allocatable > 0 ? Math.min(100, Math.round((held / allocatable) * 100)) : 0;
}

function tip(n: Node): string {
  const gpus = n.accelerators.map((a) => `${a.model || a.kind}${a.allocated ? " (in use)" : ""}`).join(", ");
  return `${n.hostname || n.id} · ${nodeStatusLabel(n.status)} · ${n.arch || "?"} · ${n.activeInstanceCount} instance(s) · ${fill(n)}% vCPU${gpus ? ` · ${gpus}` : ""}`;
}

const legend: Array<[StatusKind, string]> = [
  ["run", "ready"],
  ["drain", "draining"],
  ["unr", "unreachable"],
  ["quar", "quarantined"],
  ["end", "retired"],
];

/**
 * The fleet as tiles: one cell per node, grouped by site, coloured by status
 * and filled to its vCPU use, with a dot per GPU (hollow when free). The
 * words are attributes and titles, so the map adds no text to the page.
 */
export function FleetMap({ nodes }: { nodes: readonly Node[] }) {
  const sites = new Map<string, { name: string; partner: string; nodes: Node[] }>();
  for (const n of nodes) {
    const key = n.siteId || n.siteName;
    const s = sites.get(key) ?? { name: n.siteName, partner: n.partnerName, nodes: [] };
    s.nodes.push(n);
    sites.set(key, s);
  }
  return (
    <div className="col">
      <div className="g3">
        {[...sites.entries()].map(([key, s]) => (
          <div key={key} className="site reg">
            <div className="row">
              <span className="cw lbl" data-w={`site · ${s.name}`} />
              <span className="sp" />
              <span className="cw m mut2" data-w={s.partner} />
            </div>
            <div className="cells">
              {s.nodes.map((n) => (
                <span
                  key={n.id}
                  className={`cell s-${nodeKind(n.status)}`}
                  style={{ ["--f" as string]: fill(n) }}
                  title={tip(n)}
                  role="img"
                  aria-label={tip(n)}
                >
                  {n.accelerators.map((a) => (
                    <i key={a.id} className={a.allocated ? "" : "off"} />
                  ))}
                </span>
              ))}
            </div>
          </div>
        ))}
      </div>
      <div className="legend" aria-hidden="true">
        {legend.map(([k, w]) => (
          <span key={w} className={`s-${k}`}>
            <span className="led" />
            <span className="cw" data-w={w} />
          </span>
        ))}
        <span>
          <span className="cw" data-w="fill = vCPU in use · dot = GPU" />
        </span>
      </div>
    </div>
  );
}

export const isLive = (s: NodeStatus) => s !== NodeStatus.RETIRED;
