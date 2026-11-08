import type { Instance } from "@gen/kondukter/api/v1/instance_pb";
import { type Node, NodeStatus } from "@gen/kondukter/api/v1/types_pb";
import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { Link, useSearchParams } from "react-router";
import { useApi } from "../../api/ApiProvider";
import { errorMessage } from "../../api/errors";
import { keys } from "../../api/keys";
import { Logo } from "../../components/Logo";
import { EmptyState, ErrorState, LoadingState } from "../../components/State";
import { nodeKind, phaseKind, StatusChip } from "../../components/Status";
import { useNow } from "../../hooks/useNow";
import { formatAge, formatClock, formatDuration, shortId } from "../../lib/format";
import { lastSeen, nodeName, nodeStatusText } from "../nodes";
import { groupFleet, instanceLabel, phaseText } from "./fleet";

/** How often the page asks for the fleet. */
export const STAGE_POLL_MS = 2000;

/** The most instances one poll asks for (the API's page limit); a note says when there are more. */
const MAX_INSTANCES = 200;

/** Every status but RETIRED, which is only clutter on a projector. */
const SHOWN_STATUSES = [
  NodeStatus.ENROLLING,
  NodeStatus.READY,
  NodeStatus.DRAINING,
  NodeStatus.MAINTENANCE,
  NodeStatus.UNREACHABLE,
  NodeStatus.QUARANTINED,
];

/** One unfinished instance: its phase (with the control plane's reason when it is a problem), its name and how long it has been in the phase. */
function InstanceRow({ instance, demo, now, onNode }: { instance: Instance; demo: boolean; now: number; onNode?: boolean }) {
  return (
    <li className={`s-${phaseKind(instance.phase)}`}>
      <StatusChip kind={phaseKind(instance.phase)}>{phaseText(instance)}</StatusChip>
      <span>{instanceLabel(instance, demo)}</span>
      {onNode && instance.nodeId && <span className="mut m">on node {shortId(instance.nodeId)}</span>}
      <span className="age" title="time in this phase">
        {formatAge(instance.phaseChangedAt, now)}
      </span>
    </li>
  );
}

function NodeCard({ node, instances, demo, now }: { node: Node; instances: readonly Instance[]; demo: boolean; now: number }) {
  const kind = nodeKind(node.status);
  return (
    <article className={`snode s-${kind}`}>
      <div className="row">
        <h3>{nodeName(node)}</h3>
        <span className="sp" />
        <StatusChip kind={kind}>{nodeStatusText(node)}</StatusChip>
      </div>
      <div className="row mut m">
        <span>{node.arch || "?"}</span>
        <span>·</span>
        <span>{node.driver || "?"}</span>
        {node.accelerators.length > 0 && <span className="chip gpu">{node.accelerators.map((a) => a.model || a.kind).join(", ")}</span>}
        <span className="sp" />
        <span title="last report">last seen {lastSeen(node.lastSeenAt, now)}</span>
      </div>
      {instances.length === 0 ? (
        <p className="mut2 m">No instances.</p>
      ) : (
        <ul>
          {instances.map((i) => (
            <InstanceRow key={i.id} instance={i} demo={demo} now={now} />
          ))}
        </ul>
      )}
    </article>
  );
}

/**
 * `/stage`: the fleet for the projector during the failure drills. Nodes
 * grouped by site, one card per node with its status and when it was last
 * heard from, and in it the instances that are not finished, with their phase
 * and how long they have been in it.
 *
 * It polls `ListNodes` and `ListInstances` every 2 s. A drill's changes last
 * tens of seconds (a silent node is UNREACHABLE after about 45 s), so a poll
 * shows each one within 2 s and no server stream is needed. If a poll fails
 * the last fleet stays on screen under a banner. `?demo=1` shows short ids, so
 * the names fit on a projector.
 */
export function StagePage({ pollMs = STAGE_POLL_MS }: { pollMs?: number }) {
  const { admin } = useApi();
  const [params] = useSearchParams();
  const demo = params.get("demo") === "1";
  const now = useNow(1000);

  // A failed poll is simply followed by the next one; retrying inside it would only delay the banner.
  const poll = { refetchInterval: pollMs, retry: false } as const;
  const nodes = useQuery({ queryKey: keys.stage.nodes, queryFn: () => admin.listNodes({ statuses: SHOWN_STATUSES }), ...poll });
  const instances = useQuery({
    queryKey: keys.stage.instances,
    queryFn: () => admin.listInstances({ includeTerminal: false, pageSize: MAX_INSTANCES }),
    ...poll,
  });

  const view = useMemo(
    () => (nodes.data && instances.data ? groupFleet(nodes.data.nodes, instances.data.instances) : undefined),
    [nodes.data, instances.data],
  );

  const failure = nodes.error ?? instances.error;
  // What the page shows is as old as its older answer.
  const shownAt = Math.min(nodes.dataUpdatedAt, instances.dataUpdatedAt);

  const header = (
    <header>
      <div className="col">
        <Logo size={30} />
        <h1>Fleet</h1>
      </div>
      <div className="row">
        <span className={`live s-${failure ? "unr" : "run"}`}>
          <span className="led" aria-hidden="true" />
          <span className="cw" data-w={`LIVE · ${formatClock(now)}`} />
        </span>
        <Link className="btn sm g" to="/admin/nodes">
          Exit
        </Link>
      </div>
    </header>
  );

  if (!view) {
    const failed = nodes.isError ? nodes : instances.isError ? instances : undefined;
    return (
      <main className="stage">
        {header}
        {failed ? (
          <ErrorState
            error={failed.error}
            onRetry={() => {
              void nodes.refetch();
              void instances.refetch();
            }}
          />
        ) : (
          <LoadingState label="Loading the fleet…" />
        )}
      </main>
    );
  }

  return (
    <main className="stage">
      {header}
      <p className="mut m">
        Updated {formatClock(shownAt)}, every {pollMs / 1000} s.
      </p>
      {failure && (
        <div role="alert" className="errp">
          <p>
            Lost contact with Kondukter ({errorMessage(failure)}). Showing what it said at {formatClock(shownAt)},{" "}
            {formatDuration((now - shownAt) / 1000)} ago. Still trying.
          </p>
        </div>
      )}

      {view.sites.length === 0 ? (
        <EmptyState>No nodes have enrolled yet.</EmptyState>
      ) : (
        <div className="sites">
          {view.sites.map((site) => (
            <section key={site.key} className="col">
              <div className="row">
                <span className="lbl">Site</span>
                <h2 className="disp" style={{ fontSize: 32, margin: 0 }}>
                  {site.name}
                </h2>
                {site.partner && <span className="mut2 m">{site.partner}</span>}
              </div>
              <div className="nodes">
                {site.nodes.map(({ node, instances: onNode }) => (
                  <NodeCard key={node.id} node={node} instances={onNode} demo={demo} now={now} />
                ))}
              </div>
            </section>
          ))}
        </div>
      )}

      {view.elsewhere.length > 0 && (
        <section className="col">
          <h2 className="disp" style={{ fontSize: 32, margin: 0 }}>
            Not on a listed node
          </h2>
          <article className="snode s-end">
            <ul>
              {view.elsewhere.map((i) => (
                <InstanceRow key={i.id} instance={i} demo={demo} now={now} onNode />
              ))}
            </ul>
          </article>
        </section>
      )}

      {instances.data?.nextPageToken && <p className="mut m">Showing the newest {MAX_INSTANCES} unfinished instances.</p>}
    </main>
  );
}
