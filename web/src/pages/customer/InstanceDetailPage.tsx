import type { InstanceEvent } from "@gen/kondukter/api/v1/instance_pb";
import type { PortMapping } from "@gen/kondukter/api/v1/types_pb";
import { useQueryClient } from "@tanstack/react-query";
import { Link, useParams } from "react-router";
import { useApi } from "../../api/ApiProvider";
import { keys } from "../../api/keys";
import { Confirm } from "../../components/Confirm";
import { type Column, DataTable } from "../../components/DataTable";
import { Pipeline } from "../../components/Pipeline";
import { QueryState } from "../../components/State";
import { phaseKind, StatusChip } from "../../components/Status";
import { Terminal } from "../../components/Terminal";
import { useWatchInstance } from "../../hooks/useWatchInstance";
import {
  canTerminate,
  formatMinutes,
  formatMoney,
  formatTimestamp,
  phaseLabel,
  protocolLabel,
  shortId,
} from "../../lib/format";
import { newId } from "../../lib/ids";
import { useInstance } from "./queries";
import { PageTitle } from "../../components/PageTitle";

const timelineColumns: Column<InstanceEvent>[] = [
  { header: "When", cell: (e) => formatTimestamp(e.at) },
  { header: "From", cell: (e) => (e.fromPhase ? phaseLabel(e.fromPhase) : "—") },
  { header: "To", cell: (e) => phaseLabel(e.toPhase), className: "m" },
  { header: "Reason", cell: (e) => e.reason || "—" },
];

const mappingColumns: Column<PortMapping>[] = [
  { header: "Guest port", cell: (m) => `${m.guestPort}/${protocolLabel(m.protocol)}` },
  { header: "Reachable at", cell: (m) => `${m.hostAddress}:${m.hostPort}` },
];

/** `/app/instances/:id`: one instance with its live phase, timeline, connection details and cost. */
export function InstanceDetailPage() {
  const { id = "" } = useParams();
  const { instances } = useApi();
  const qc = useQueryClient();
  const query = useInstance(id);
  const watch = useWatchInstance(id);

  return (
    <section>
      <p>
        <Link to="/app/instances">← Instances</Link>
      </p>
      <QueryState query={query}>
        {({ instance, events }) => {
          if (!instance) return <p role="alert">The server sent no instance.</p>;
          const connection = instance.connection;
          return (
            <>
              <PageTitle>{instance.name || `Instance ${shortId(instance.id)}`}</PageTitle>
              {watch.status === "reconnecting" && (
                <p role="status" className="warn">
                  Live updates interrupted; reconnecting…
                </p>
              )}
              {watch.status === "failed" && (
                <p role="alert" className="warn">
                  Live updates stopped: {watch.error?.rawMessage}
                </p>
              )}

              <div className="card reg">
                <Pipeline phase={instance.phase} />
              </div>

              <section>
                <h2>Status</h2>
                <dl>
                  <dt>Phase</dt>
                  <dd>
                    <StatusChip kind={phaseKind(instance.phase)}>{phaseLabel(instance.phase)}</StatusChip>
                  </dd>
                  <dt>Reason</dt>
                  <dd>{instance.phaseReason || "—"}</dd>
                  <dt>Since</dt>
                  <dd>{formatTimestamp(instance.phaseChangedAt)}</dd>
                </dl>
                <div className="actions">
                  <Confirm
                    label="Terminate"
                    message="Terminate this instance? This cannot be undone."
                    disabled={!canTerminate(instance.phase)}
                    onConfirm={async () => {
                      await instances.terminateInstance({ id: instance.id, idempotencyKey: newId() });
                      await qc.invalidateQueries({ queryKey: keys.instances.detail(instance.id) });
                    }}
                  />
                </div>
              </section>

              <section>
                <h2>Connect</h2>
                {connection ? (
                  <>
                    <Terminal command={connection.sshCommand} />
                    <dl>
                      <dt>Host</dt>
                      <dd>{connection.host}</dd>
                      <dt>SSH port</dt>
                      <dd>{connection.sshPort}</dd>
                      <dt>User</dt>
                      <dd>{connection.sshUser}</dd>
                    </dl>
                    <DataTable
                      caption="Port mappings"
                      columns={mappingColumns}
                      rows={connection.portMappings}
                      rowKey={(m) => `${m.guestPort}/${m.protocol}`}
                      empty="No ports are mapped yet."
                    />
                  </>
                ) : (
                  <p className="mut">Connection details appear once the instance is running.</p>
                )}
              </section>

              <div className="g2">
                <section>
                  <h2>Details</h2>
                  <dl>
                    <dt>Id</dt>
                    <dd>{instance.id}</dd>
                    <dt>Offer</dt>
                    <dd>{instance.shape?.name}</dd>
                    <dt>Region</dt>
                    <dd>{instance.region}</dd>
                    <dt>Image</dt>
                    <dd>
                      {instance.imageName} ({instance.arch})
                    </dd>
                    <dt>Created</dt>
                    <dd>{formatTimestamp(instance.createdAt)}</dd>
                    <dt>SSH keys</dt>
                    <dd>{instance.sshKeyFingerprints.join(", ") || "—"}</dd>
                    <dt>Exposed ports</dt>
                    <dd>{instance.ports.map((p) => `${p.guestPort}/${protocolLabel(p.protocol)}`).join(", ") || "—"}</dd>
                  </dl>
                </section>

                <section>
                  <h2>Usage</h2>
                  <dl>
                    <dt>Running time</dt>
                    <dd>{formatMinutes(instance.usage?.runningMinutes)}</dd>
                    <dt>Price per hour</dt>
                    <dd>{formatMoney(instance.pricePerHour)}</dd>
                    <dt>Estimated cost</dt>
                    <dd>{formatMoney(instance.usage?.estimatedCost)}</dd>
                  </dl>
                </section>
              </div>

              <DataTable caption="Timeline" columns={timelineColumns} rows={events} rowKey={(e) => `${e.at?.seconds}.${e.at?.nanos}-${e.toPhase}`} />
            </>
          );
        }}
      </QueryState>
    </section>
  );
}
