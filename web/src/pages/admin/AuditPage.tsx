import type { AuditEntry } from "@gen/kondukter/api/v1/admin_pb";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useApi } from "../../api/ApiProvider";
import { keys } from "../../api/keys";
import { type Column, DataTable } from "../../components/DataTable";
import { useParam } from "../../hooks/useParam";
import { formatTimestamp, shortId } from "../../lib/format";
import { PageTitle } from "../../components/PageTitle";

export const AUDIT_PAGE_SIZE = 50;

/** The actions the control plane writes (`internal/controlplane/api/audit.go`); the filter is an exact match. */
export const auditActions = [
  "admin.quarantine_node",
  "admin.unquarantine_node",
  "admin.force_terminate_instance",
  "partner.create_enrollment_token",
  "partner.drain_node",
  "partner.undrain_node",
  "instance.terminate",
];

/** "node/7f3c1a2b-…" reads "node/7f3c1a2b", the whole target as the tooltip. */
function Target({ target }: { target: string }) {
  const [type, id] = target.split("/", 2);
  return <span title={target}>{id ? `${type}/${shortId(id)}` : target}</span>;
}

function Payload({ entry }: { entry: AuditEntry }) {
  if (!entry.payload || Object.keys(entry.payload).length === 0) return <>—</>;
  const line = JSON.stringify(entry.payload);
  return (
    <details>
      <summary>{line.length > 80 ? `${line.slice(0, 79)}…` : line}</summary>
      <pre>{JSON.stringify(entry.payload, null, 2)}</pre>
    </details>
  );
}

/**
 * `/admin/audit`: the audit log, newest first, 50 entries a page. `?action=`
 * and `?actor=` (a user id) filter on the server; `?page=` is the server's
 * page token, dropped whenever a filter changes.
 */
export function AuditPage() {
  const { admin } = useApi();
  const [action, setAction] = useParam("action", ["page"]);
  const [actor, setActor] = useParam("actor", ["page"]);
  const [page, setPage] = useParam("page");
  const log = useQuery({
    queryKey: keys.admin.audit({ action, actor, page }),
    queryFn: () => admin.listAuditLog({ action, actorId: actor, pageSize: AUDIT_PAGE_SIZE, pageToken: page }),
    placeholderData: keepPreviousData,
  });
  const entries = log.data?.entries;
  const actorName = entries?.find((e) => e.actorId === actor)?.actorDisplay;
  const next = log.data?.nextPageToken ?? "";

  const columns: Column<AuditEntry>[] = [
    { header: "Time", cell: (e) => formatTimestamp(e.at) },
    {
      header: "Actor",
      cell: (e) => (
        <>
          <button type="button" className="sm g m" title="Show only this actor" onClick={() => setActor(e.actorId)}>
            {e.actorDisplay || e.actorId}
          </button>{" "}
          ({e.actorType.replace("_", " ")})
        </>
      ),
    },
    { header: "Action", cell: (e) => e.action, className: "m ink-h" },
    { header: "Target", cell: (e) => <Target target={e.target} /> },
    { header: "Payload", cell: (e) => <Payload entry={e} /> },
  ];

  return (
    <section>
      <PageTitle>Audit log</PageTitle>
      <div className="filterrow">
        <label>
          Action{" "}
          <select value={action} onChange={(e) => setAction(e.target.value)}>
            <option value="">all actions</option>
            {auditActions.map((a) => (
              <option key={a} value={a}>
                {a}
              </option>
            ))}
            {action && !auditActions.includes(action) && <option value={action}>{action}</option>}
          </select>
        </label>{" "}
        {actor && (
          <>
            Actor: {actorName ?? actor}{" "}
            <button type="button" className="sm" onClick={() => setActor("")}>
              Any actor
            </button>
          </>
        )}
      </div>
      <DataTable
        columns={columns}
        rows={entries}
        rowKey={(e) => e.id}
        loading={log.isPending}
        error={log.error}
        onRetry={() => void log.refetch()}
        empty={action || actor ? "No entries match the filter." : page ? "No older entries." : "The audit log is empty."}
      />
      <p>
        {page && (
          <button type="button" onClick={() => setPage("")}>
            Newest entries
          </button>
        )}{" "}
        {next && (
          <button type="button" onClick={() => setPage(next)}>
            Older entries
          </button>
        )}
      </p>
      <p>
        <small>
          Newest first, {AUDIT_PAGE_SIZE} a page{page ? "; this is an older page" : ""}. Select an actor to see only theirs, open
          a payload for the details. The page refreshes every 5 s.
        </small>
      </p>
    </section>
  );
}
