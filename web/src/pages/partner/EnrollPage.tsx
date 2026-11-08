import type { CreateEnrollmentTokenResponse } from "@gen/kondukter/api/v1/partner_pb";
import { useState } from "react";
import { useSearchParams } from "react-router";
import { useApi } from "../../api/ApiProvider";
import { CopyButton } from "../../components/CopyButton";
import { Field } from "../../components/Field";
import { Form } from "../../components/Form";
import { ErrorState, LoadingState } from "../../components/State";
import { formatTimestamp } from "../../lib/format";
import { newId } from "../../lib/ids";
import { useSites } from "./sites";

/**
 * `/partner/enroll`: a one-time enrollment token for a site, shown as the
 * command to run on the node. The server keeps only the token's hash, so the
 * command is shown once; a new submit makes a new token.
 */
export function EnrollPage() {
  const { partner } = useApi();
  const [params] = useSearchParams();
  const sites = useSites();
  const [created, setCreated] = useState<{ res: CreateEnrollmentTokenResponse; site: string } | null>(null);
  const list = sites.data?.sites;

  return (
    <section>
      <h1>Enroll a node</h1>
      {sites.isPending ? (
        <LoadingState />
      ) : !list ? (
        <ErrorState error={sites.error} onRetry={() => void sites.refetch()} />
      ) : list.length === 0 ? (
        <p>There is no site to enroll into yet. The operator creates a partner's sites.</p>
      ) : (
        <Form
          submitLabel="Create enrollment token"
          onSubmit={async (data) => {
            const siteId = String(data.get("site") ?? "");
            setCreated(null);
            // A fresh key every time: a replay would not return the token again.
            const res = await partner.createEnrollmentToken({ siteId, idempotencyKey: newId() });
            setCreated({ res, site: list.find((s) => s.id === siteId)?.name ?? siteId });
          }}
        >
          <Field label="Site" as="select" name="site" required defaultValue={params.get("site") ?? list[0]?.id}>
            {list.map((s) => (
              <option key={s.id} value={s.id}>
                {s.name} ({s.region})
              </option>
            ))}
          </Field>
        </Form>
      )}
      {created && (
        <div role="region" aria-label="Enrollment command">
          <p>
            <strong>
              Token for site {created.site} created. Copy the command now: it is shown only once, works once, and expires at{" "}
              {formatTimestamp(created.res.expiresAt)}.
            </strong>
          </p>
          <p>
            <code data-testid="enroll-command">{created.res.enrollCommand}</code> <CopyButton text={created.res.enrollCommand} />
          </p>
          <p>
            Run it on the node, from a Kondukter checkout: it installs the agent as a service, enrolls it with the token and
            starts it. The node shows under Nodes and turns ready once the agent connects.{" "}
            <button type="button" onClick={() => setCreated(null)}>
              Done
            </button>
          </p>
        </div>
      )}
    </section>
  );
}
