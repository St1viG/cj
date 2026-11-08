import type { Offer } from "@gen/kondukter/api/v1/catalog_pb";
import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";
import { useApi } from "../../api/ApiProvider";
import { keys } from "../../api/keys";
import { Field } from "../../components/Field";
import { Form } from "../../components/Form";
import { EmptyState, ErrorState, LoadingState } from "../../components/State";
import { formatAccelerator, formatMiB, formatMoney } from "../../lib/format";
import { useIdempotencyKey } from "../../lib/ids";
import { parsePorts } from "../../lib/ports";
import { useImages, useOffers, useSshKeys } from "./queries";
import { PageTitle } from "../../components/PageTitle";

/** The API's limit on cloud-init user-data (`MaxUserDataBytes`). */
const MAX_USER_DATA_BYTES = 16 << 10;

function offerLabel(o: Offer): string {
  const s = o.shape;
  return [
    s?.name,
    formatAccelerator(s),
    `${s?.vcpu} vCPU`,
    formatMiB(s?.ramMib ?? 0n),
    o.region,
    `${formatMoney(o.pricePerHour)}/h`,
  ].join(" · ");
}

/**
 * `/app/instances/new?offer=<id>`: choose an offer, an image that runs on it
 * and the SSH keys that may log in, then create. The offer decides which
 * architectures and images exist; the form follows it.
 */
export function NewInstancePage() {
  const { instances } = useApi();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const keyFor = useIdempotencyKey();

  const offersQuery = useOffers();
  const sshKeys = useSshKeys();

  const [offerId, setOfferId] = useState(params.get("offer") ?? "");
  const [chosenArch, setChosenArch] = useState("");
  const offers = offersQuery.data?.offers ?? [];
  const offer = offers.find((o) => o.id === offerId);
  const arch = offer && chosenArch && offer.archs.includes(chosenArch) ? chosenArch : (offer?.archs[0] ?? "");
  const images = useImages(offer?.id ?? "", arch);

  if (offersQuery.isPending) return <LoadingState />;
  if (offersQuery.isError && !offersQuery.data) {
    return <ErrorState error={offersQuery.error} onRetry={() => void offersQuery.refetch()} />;
  }

  return (
    <section>
      <PageTitle>New instance</PageTitle>
      {offers.length === 0 ? (
        <EmptyState>No offers are for sale yet.</EmptyState>
      ) : (
        <Form
          submitLabel="Create instance"
          onSubmit={async (data) => {
            if (!offer) throw new Error("Choose an offer.");
            const imageId = String(data.get("image") ?? "");
            if (!imageId) throw new Error("Choose an image.");
            const sshKeyIds = data.getAll("ssh_key").map(String);
            if (sshKeyIds.length === 0) throw new Error("Select at least one SSH key.");
            const ports = parsePorts(String(data.get("ports") ?? ""));
            const userData = String(data.get("user_data") ?? "");
            const size = new TextEncoder().encode(userData).length;
            if (size > MAX_USER_DATA_BYTES) {
              throw new Error(`The user-data is ${size} bytes; at most ${MAX_USER_DATA_BYTES}.`);
            }
            const request = { offerId: offer.id, imageId, sshKeyIds, ports, userData, name: String(data.get("name") ?? "").trim() };
            // A retry of this exact request reuses the key, so it cannot create twice.
            const fingerprint = JSON.stringify({ ...request, ports: ports.map((p) => [p.guestPort, p.protocol]) });
            const resp = await instances.createInstance({ ...request, idempotencyKey: keyFor(fingerprint) });
            await qc.invalidateQueries({ queryKey: keys.instances.lists });
            navigate(`/app/instances/${resp.instance?.id ?? ""}`);
          }}
        >
          <Field
            as="select"
            label="Offer"
            name="offer"
            value={offerId}
            onChange={(e) => setOfferId(e.currentTarget.value)}
            required
          >
            <option value="">Choose an offer…</option>
            {offers.map((o) => (
              <option key={o.id} value={o.id}>
                {offerLabel(o)}
              </option>
            ))}
          </Field>
          {offerId && !offer && <p role="alert" className="msg err">That offer is not for sale.</p>}

          {offer && offer.archs.length > 1 && (
            <Field as="select" label="Architecture" name="arch" value={arch} onChange={(e) => setChosenArch(e.currentTarget.value)}>
              {offer.archs.map((a) => (
                <option key={a} value={a}>
                  {a}
                </option>
              ))}
            </Field>
          )}
          {offer && offer.archs.length === 0 && <p role="alert" className="msg err">No image can run on this offer yet.</p>}

          {offer && arch && (
            <>
              {images.isPending && <LoadingState label="Loading images…" />}
              {images.isError && <ErrorState error={images.error} onRetry={() => void images.refetch()} />}
              {images.data && (
                <Field as="select" label="Image" name="image" required>
                  {images.data.images.map((img) => (
                    <option key={img.id} value={img.id}>
                      {[`${img.name} ${img.version}`, img.arch, img.accelStack].filter(Boolean).join(" · ")}
                    </option>
                  ))}
                </Field>
              )}
            </>
          )}

          <Field label="Name (optional)" name="name" maxLength={63} hint="Letters, digits and dashes also set the hostname." />

          <fieldset>
            <legend>SSH keys</legend>
            {sshKeys.isPending && <LoadingState label="Loading keys…" />}
            {sshKeys.isError && <ErrorState error={sshKeys.error} onRetry={() => void sshKeys.refetch()} />}
            {sshKeys.data && sshKeys.data.sshKeys.length === 0 && (
              <p>
                You have no SSH keys. <Link to="/app/keys">Add one</Link> to log in to an instance.
              </p>
            )}
            {sshKeys.data?.sshKeys.map((k) => (
              <div key={k.id}>
                <label>
                  <input type="checkbox" name="ssh_key" value={k.id} /> {k.name} <small>({k.fingerprint})</small>
                </label>
              </div>
            ))}
          </fieldset>

          <Field
            as="textarea"
            label="Exposed ports"
            name="ports"
            rows={2}
            placeholder="8080, 53/udp"
            hint="Besides 22/tcp, which is always exposed. Separate with commas or new lines; a bare number is TCP."
          />
          <Field
            as="textarea"
            label="Cloud-init user-data (optional)"
            name="user_data"
            rows={6}
            hint={`At most ${MAX_USER_DATA_BYTES / 1024} KiB.`}
          />
        </Form>
      )}
    </section>
  );
}
