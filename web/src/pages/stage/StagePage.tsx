import { NodeStatus } from "@gen/kondukter/api/v1/types_pb";
import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { useSearchParams } from "react-router";
import { useApi } from "../../api/ApiProvider";
import { errorMessage } from "../../api/errors";
import { keys } from "../../api/keys";
import { EmptyState, ErrorState, LoadingState } from "../../components/State";
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

/**
 * `/stage`: the fleet for the projector during the failure drills. Nodes
 * grouped by site, each with its status and when it was last heard from, and
 * under it the instances that are not finished, with their phase and how long
 * they have been in it. Plain nested lists; the owner styles them.
 *
 * It polls `ListNodes` and `ListInstances` every 2 s. A drill's changes last
 * tens of seconds (a silent node is UNREACHABLE after about 45 s), so a poll
 * shows each one within 2 s and no server stream is needed. `?demo=1` shows
 * short ids, so the names fit on a projector.
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

  if (!view) {
    const failed = nodes.isError ? nodes : instances.isError ? instances : undefined;
    return (
      <main>
        <h1>Fleet</h1>
        {failed ? (
          <ErrorState
            error={failed.error}
            onRetry={() => {
              void nodes.refetch();
              void instances.refetch();
            }}
          />
        ) : (
          <LoadingState />
        )}
      </main>
    );
  }

  const failure = nodes.error ?? instances.error;
  // What the page shows is as old as its older answer.
  const shownAt = Math.min(nodes.dataUpdatedAt, instances.dataUpdatedAt);

  return (
    <main>
      <h1>Fleet</h1>
      <p>
        Updated {formatClock(shownAt)}, every {pollMs / 1000} s.
      </p>
      {failure && (
        <div role="alert">
          <p>
            Lost contact with Kondukter ({errorMessage(failure)}). Showing what it said at {formatClock(shownAt)},{" "}
            {formatDuration((now - shownAt) / 1000)} ago. Still trying.
          </p>
        </div>
      )}

      {view.sites.length === 0 ? (
        <EmptyState>No nodes have enrolled yet.</EmptyState>
      ) : (
        <ul>
          {view.sites.map((site) => (
            <li key={site.key}>
              <strong>{site.name}</strong>
              {site.partner && ` (${site.partner})`}
              <ul>
                {site.nodes.map(({ node, instances: onNode }) => (
                  <li key={node.id}>
                    <strong>{nodeName(node)}</strong>: {nodeStatusText(node)}, last seen {lastSeen(node.lastSeenAt, now)}
                    {onNode.length === 0 ? (
                      <p>No instances.</p>
                    ) : (
                      <ul>
                        {onNode.map((i) => (
                          <li key={i.id}>
                            {instanceLabel(i, demo)}: {phaseText(i)} for {formatAge(i.phaseChangedAt, now)}
                          </li>
                        ))}
                      </ul>
                    )}
                  </li>
                ))}
              </ul>
            </li>
          ))}
        </ul>
      )}

      {view.elsewhere.length > 0 && (
        <section>
          <h2>Not on a listed node</h2>
          <ul>
            {view.elsewhere.map((i) => (
              <li key={i.id}>
                {instanceLabel(i, demo)}: {phaseText(i)} for {formatAge(i.phaseChangedAt, now)}
                {i.nodeId && `, on node ${shortId(i.nodeId)}`}
              </li>
            ))}
          </ul>
        </section>
      )}

      {instances.data?.nextPageToken && <p>Showing the newest {MAX_INSTANCES} unfinished instances.</p>}
    </main>
  );
}
