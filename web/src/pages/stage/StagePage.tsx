import type { Instance } from "@gen/kondukter/api/v1/instance_pb";
import { type Node, NodeStatus } from "@gen/kondukter/api/v1/types_pb";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { Link, useSearchParams } from "react-router";
import { useApi } from "../../api/ApiProvider";
import { keys } from "../../api/keys";
import { Logo } from "../../components/Logo";
import { ErrorState, LoadingState } from "../../components/State";
import { nodeKind, phaseKind, StatusChip } from "../../components/Status";
import { useNow } from "../../hooks/useNow";
import { formatAge, formatClock, nodeStatusLabel, phaseLabel, shortId, withReason } from "../../lib/format";

/** How often the projector view asks for the fleet (WEB_PAGES.md: a 2 s interval). */
export const STAGE_POLL_MS = 2000;

const noFilter = { site: "", status: 0 } as const;
const liveFilter = { includeTerminal: false, node: "", org: "" } as const;

function NodeCard({ node, instances, now, short }: { node: Node; instances: readonly Instance[]; now: number; short: (id: string) => string }) {
  const kind = nodeKind(node.status);
  return (
    <article className={`snode s-${kind}`} data-node={node.hostname}>
      <div className="row">
        <h3>{node.hostname || short(node.id)}</h3>
        <span className="sp" />
        <StatusChip kind={kind}>{withReason(nodeStatusLabel(node.status), node.statusReason)}</StatusChip>
      </div>
      <div className="row mut m">
        <span>{node.arch || "?"}</span>
        <span>·</span>
        <span>{node.driver || "?"}</span>
        {node.accelerators.length > 0 && (
          <>
            <span>·</span>
            <span className="chip gpu">{node.accelerators.map((a) => a.model || a.kind).join(", ")}</span>
          </>
        )}
        <span className="sp" />
        <span title="last report">{node.lastSeenAt ? `${formatAge(node.lastSeenAt, now)} ago` : "never seen"}</span>
      </div>
      {instances.length === 0 ? (
        <p className="mut2 m">no instances</p>
      ) : (
        <ul>
          {instances.map((i) => (
            <li key={i.id} className={`s-${phaseKind(i.phase)}`} data-instance={i.id}>
              <StatusChip kind={phaseKind(i.phase)}>{phaseLabel(i.phase)}</StatusChip>
              <span>{i.name || short(i.id)}</span>
              <span className="age">{formatAge(i.phaseChangedAt, now)}</span>
            </li>
          ))}
        </ul>
      )}
    </article>
  );
}

/**
 * `/stage`: the whole fleet for a projector. Nodes grouped by site, each with
 * its status and its live instances with the time spent in the phase; it
 * refreshes every 2 s, so a drill shows RUNNING -> UNREACHABLE -> RUNNING and
 * the node status change without a reload. `?demo=1` shortens ids.
 */
export function StagePage() {
  const { admin } = useApi();
  const [params] = useSearchParams();
  const now = useNow();
  const short = (id: string) => (params.get("demo") === "1" ? id.slice(0, 4) : shortId(id));

  const nodes = useQuery({
    queryKey: keys.fleet.adminNodes(noFilter),
    queryFn: () => admin.listNodes({}),
    refetchInterval: STAGE_POLL_MS,
    placeholderData: keepPreviousData,
  });
  const instances = useQuery({
    queryKey: keys.fleet.adminInstances(liveFilter),
    queryFn: () => admin.listInstances({ includeTerminal: false, pageSize: 200 }),
    refetchInterval: STAGE_POLL_MS,
    placeholderData: keepPreviousData,
  });

  const bySite = new Map<string, { name: string; partner: string; nodes: Node[] }>();
  for (const n of nodes.data?.nodes ?? []) {
    if (n.status === NodeStatus.RETIRED) continue;
    const key = n.siteId || n.siteName;
    const site = bySite.get(key) ?? { name: n.siteName, partner: n.partnerName, nodes: [] };
    site.nodes.push(n);
    bySite.set(key, site);
  }

  return (
    <div className="stage">
      <header>
        <div className="col">
          <Logo size={30} />
          <h1>Fleet</h1>
        </div>
        <div className="row">
          <span className={`live s-${nodes.isFetching ? "prov" : "run"}`}>
            <span className="led" aria-hidden="true" />
            <span className="cw" data-w={`LIVE · ${formatClock(now)}`} />
          </span>
          <Link className="btn sm g" to="/admin/nodes">
            Exit
          </Link>
        </div>
      </header>
      {nodes.isPending ? (
        <LoadingState label="Loading the fleet…" />
      ) : nodes.isError && !nodes.data ? (
        <ErrorState error={nodes.error} onRetry={() => void nodes.refetch()} />
      ) : bySite.size === 0 ? (
        <p className="mut">No nodes are enrolled yet.</p>
      ) : (
        <div className="sites">
          {[...bySite.entries()].map(([key, site]) => (
            <section key={key} className="col">
              <div className="row">
                <span className="lbl">Site</span>
                <span className="disp" style={{ fontSize: 32 }}>
                  {site.name}
                </span>
                <span className="mut2 m">{site.partner}</span>
              </div>
              <div className="nodes">
                {site.nodes.map((n) => (
                  <NodeCard
                    key={n.id}
                    node={n}
                    instances={(instances.data?.instances ?? []).filter((i) => i.nodeId === n.id)}
                    now={now}
                    short={short}
                  />
                ))}
              </div>
            </section>
          ))}
        </div>
      )}
      {instances.isError && <ErrorState error={instances.error} onRetry={() => void instances.refetch()} />}
    </div>
  );
}
