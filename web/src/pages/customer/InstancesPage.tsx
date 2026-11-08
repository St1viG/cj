import type { Instance } from "@gen/kondukter/api/v1/instance_pb";
import { InstancePhase } from "@gen/kondukter/api/v1/types_pb";
import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router";
import { useApi } from "../../api/ApiProvider";
import { keys } from "../../api/keys";
import { Confirm } from "../../components/Confirm";
import { Stat } from "../../components/Stat";
import { phaseKind, StatusChip } from "../../components/Status";
import { type Column, DataTable } from "../../components/DataTable";
import { useWatchInstance } from "../../hooks/useWatchInstance";
import { canTerminate, formatMinutes, formatMoney, formatTimestamp, isTransitional, phaseLabel, shortId } from "../../lib/format";
import { newId } from "../../lib/ids";
import { INSTANCE_PAGE_SIZE, useInstances } from "./queries";
import { RentGpuCard } from "./RentGpuCard";
import { PageTitle } from "../../components/PageTitle";

/**
 * How many instances the list follows with a stream. A browser allows about
 * six HTTP/1.1 connections per host and every stream holds one, so the rest
 * of the page would starve if each row had its own. The list polls anyway; the
 * streams just make the instances a person is waiting on update at once.
 */
export const MAX_LIST_STREAMS = 3;

/** Follows one instance for as long as it is mounted; draws nothing. */
function Follow({ id }: { id: string }) {
  useWatchInstance(id);
  return null;
}

/** `/app/instances`: the caller's instances with live phase, running time and estimated cost. */
export function InstancesPage() {
  const { instances } = useApi();
  const qc = useQueryClient();
  const [includeTerminal, setIncludeTerminal] = useState(false);
  const list = useInstances(includeTerminal);
  const rows = list.data?.instances;

  const columns: Column<Instance>[] = [
    { header: "Name", cell: (i) => i.name || "—" },
    {
      header: "Id",
      cell: (i) => (
        <Link className="m" to={`/app/instances/${i.id}`} title={i.id}>
          {shortId(i.id)}
        </Link>
      ),
    },
    { header: "Phase", cell: (i) => <StatusChip kind={phaseKind(i.phase)}>{phaseLabel(i.phase)}</StatusChip> },
    { header: "Offer", cell: (i) => i.shape?.name },
    { header: "Region", cell: (i) => i.region },
    { header: "Running", cell: (i) => formatMinutes(i.usage?.runningMinutes), className: "m" },
    { header: "Estimated cost", cell: (i) => formatMoney(i.usage?.estimatedCost), className: "m ink-h" },
    { header: "Created", cell: (i) => formatTimestamp(i.createdAt) },
    {
      header: "",
      cell: (i) => (
        <Confirm
          label="Terminate"
          message={`Terminate ${i.name || shortId(i.id)}?`}
          disabled={!canTerminate(i.phase)}
          onConfirm={async () => {
            await instances.terminateInstance({ id: i.id, idempotencyKey: newId() });
            await qc.invalidateQueries({ queryKey: keys.instances.all });
          }}
        />
      ),
    },
  ];

  const following = (rows ?? []).filter((i) => isTransitional(i.phase)).slice(0, MAX_LIST_STREAMS);

  return (
    <section>
      <PageTitle>Instances</PageTitle>
      <RentGpuCard />
      <p>
        <Link to="/app/offers">Browse offers</Link> to create one.
      </p>
      <div className="g3">
        <Stat label="Running" value={(rows ?? []).filter((i) => i.phase === InstancePhase.RUNNING).length} />
        <Stat label="In transition" value={following.length} />
        <Stat label="Listed" value={rows?.length ?? 0} />
      </div>
      <label>
        <input type="checkbox" checked={includeTerminal} onChange={(e) => setIncludeTerminal(e.currentTarget.checked)} /> Show
        terminated and failed instances
      </label>
      {following.map((i) => (
        <Follow key={i.id} id={i.id} />
      ))}
      <DataTable
        caption="Your instances"
        columns={columns}
        rows={rows}
        rowKey={(i) => i.id}
        loading={list.isPending}
        error={list.error}
        onRetry={() => void list.refetch()}
        empty={
          <>
            You have no instances{includeTerminal ? "" : " running"}. <Link to="/app/offers">Browse offers</Link>.
          </>
        }
      />
      {list.data?.nextPageToken && <p>Showing the newest {INSTANCE_PAGE_SIZE} instances.</p>}
    </section>
  );
}
