import { create } from "@bufbuild/protobuf";
import type { Offer } from "@gen/kondukter/api/v1/catalog_pb";
import { InstancePhase, PortSpecSchema, Protocol } from "@gen/kondukter/api/v1/types_pb";
import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useApi } from "../../api/ApiProvider";
import { keys } from "../../api/keys";
import { Pipeline } from "../../components/Pipeline";
import { Terminal } from "../../components/Terminal";
import { isTerminalPhase, useWatchInstance } from "../../hooks/useWatchInstance";
import { formatMoney } from "../../lib/format";
import { newId } from "../../lib/ids";
import { generateSshKeyPair } from "../../lib/sshKey";
import { useOffers } from "./queries";

/** The guest port the card exposes besides SSH: where the customer's own server will listen. */
const SERVICE_PORT = 8080;
/** The file name suggested for the private key. */
export const KEY_FILE = "kondukter_demo_ed25519";
const STORAGE_KEY = "kondukter.rental";

/** The rental in progress: the instance, and the private key made for it (the only copy). */
interface Rental {
  instanceId: string;
  privateKey: string;
}

// sessionStorage can be missing or full (private windows, blocked storage): the
// card works without it and only loses the "survives a reload" convenience.
function loadRental(): Rental | undefined {
  try {
    const raw = sessionStorage.getItem(STORAGE_KEY);
    return raw ? (JSON.parse(raw) as Rental) : undefined;
  } catch {
    return undefined;
  }
}

function saveRental(r: Rental | undefined): void {
  try {
    if (r) sessionStorage.setItem(STORAGE_KEY, JSON.stringify(r));
    else sessionStorage.removeItem(STORAGE_KEY);
  } catch {
    // see above
  }
}

function downloadText(name: string, text: string): void {
  const url = URL.createObjectURL(new Blob([text], { type: "application/octet-stream" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.click();
  URL.revokeObjectURL(url);
}

/**
 * One block that saves the private key (the heredoc is quoted, so the shell
 * leaves it alone), locks its permissions and logs in. `IdentitiesOnly` keeps
 * ssh from offering every key in the agent first, which makes the server
 * give up with "Too many authentication failures".
 */
export function loginCommand(keyPath: string, privateKey: string, port: number, user: string, host: string): string {
  return [
    `cat > ${keyPath} <<'EOF'`,
    privateKey.trimEnd(),
    "EOF",
    `chmod 600 ${keyPath}`,
    `ssh -i ${keyPath} -o IdentitiesOnly=yes -p ${port} ${user}@${host}`,
  ].join("\n");
}

function errorText(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

/**
 * One click to a GPU machine: makes an SSH key in the browser, creates an
 * instance on the first GPU offer with the public half, follows it until it
 * runs, and then shows the `ssh` command and the private key to download. The
 * private key exists only here, so the card keeps it until the machine is
 * given back.
 */
export function RentGpuCard() {
  const { instances, catalog } = useApi();
  const qc = useQueryClient();
  const offers = useOffers({});
  const [rental, setRental] = useState<Rental | undefined>(loadRental);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const watch = useWatchInstance(rental?.instanceId);

  const gpuOffers = (offers.data?.offers ?? []).filter((o) => (o.shape?.acceleratorCount ?? 0) > 0);
  const instance = watch.instance;
  const phase = instance?.phase ?? InstancePhase.PENDING;
  const over = rental !== undefined && instance !== undefined && isTerminalPhase(phase);

  async function rent(offer: Offer) {
    setBusy(true);
    setError(undefined);
    try {
      const arch = offer.archs[0] ?? "";
      const image = (await catalog.listImages({ offerId: offer.id, arch })).images[0];
      if (!image) throw new Error("No image can run on this GPU yet.");
      const suffix = newId().slice(0, 6);
      const key = generateSshKeyPair(`kondukter-${suffix}`);
      const resp = await instances.createInstance({
        offerId: offer.id,
        imageId: image.id,
        sshPublicKeys: [key.publicKey],
        ports: [create(PortSpecSchema, { guestPort: SERVICE_PORT, protocol: Protocol.TCP })],
        name: `gpu-${suffix}`,
        idempotencyKey: newId(),
      });
      const next = { instanceId: resp.instance?.id ?? "", privateKey: key.privateKey };
      saveRental(next);
      setRental(next);
      await qc.invalidateQueries({ queryKey: keys.instances.lists });
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  }

  async function giveBack() {
    if (!rental) return;
    setBusy(true);
    setError(undefined);
    try {
      await instances.terminateInstance({ id: rental.instanceId });
      await qc.invalidateQueries({ queryKey: keys.instances.lists });
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  }

  function startOver() {
    saveRental(undefined);
    setRental(undefined);
    setError(undefined);
  }

  if (!rental) {
    return (
      <div className="card reg" aria-label="Rent a GPU">
        <h2>Rent a GPU</h2>
        {offers.isPending && <p>Looking for GPUs…</p>}
        {offers.isError && <p role="alert" className="msg err">Could not load the offers: {errorText(offers.error)}</p>}
        {offers.data && gpuOffers.length === 0 && <p>No GPU is for sale right now.</p>}
        {gpuOffers.map((o) => (
          <p key={o.id}>
            <button type="button" className="btn p" disabled={busy} onClick={() => void rent(o)}>
              {busy ? "Starting…" : `Rent a ${o.shape?.acceleratorModel || "GPU"}`}
            </button>{" "}
            <small>
              {o.shape?.vcpu} vCPU · {formatMoney(o.pricePerHour)}/h
            </small>
          </p>
        ))}
        {gpuOffers.length > 0 && (
          <p>
            <small>We make an SSH key for you in this browser, start the machine, and show you how to log in.</small>
          </p>
        )}
        {error && <p role="alert" className="msg err">{error}</p>}
      </div>
    );
  }

  const connection = instance?.connection;
  const service = connection?.portMappings.find((m) => m.guestPort === SERVICE_PORT);
  const keyPath = `~/Downloads/${KEY_FILE}`;

  return (
    <div className="card reg" aria-label="Your GPU">
      <h2>Your GPU</h2>
      <Pipeline phase={phase} />
      {instance?.phaseReason && (
        <p>
          <small>{instance.phaseReason}</small>
        </p>
      )}
      {watch.status === "failed" && <p role="alert" className="msg err">Lost track of the machine: {watch.error?.message}</p>}

      {phase === InstancePhase.RUNNING && connection && (
        <>
          <p>It is running. Copy this block and paste it into a terminal on your computer: it saves your key and logs you in.</p>
          <Terminal title="your terminal" command={loginCommand(keyPath, rental.privateKey, connection.sshPort, connection.sshUser, connection.host)} />
          <p>
            <button type="button" onClick={() => downloadText(KEY_FILE, rental.privateKey)}>
              Download private key
            </button>{" "}
            <small>The key is made in this browser and kept nowhere else. A download can be blocked on a plain-HTTP page; the block above does not need one.</small>
          </p>
          {service && (
            <p>
              Port {SERVICE_PORT} of the machine is reachable at{" "}
              <code>
                http://{service.hostAddress}:{service.hostPort}
              </code>
              , once something listens there.
            </p>
          )}
        </>
      )}

      {over && (
        <p role="status">
          {phase === InstancePhase.TERMINATED ? "The machine is gone." : "The machine did not start."} <button type="button" onClick={startOver}>Rent another</button>
        </p>
      )}
      {!over && (
        <p>
          <button type="button" disabled={busy || phase === InstancePhase.TERMINATING} onClick={() => void giveBack()}>
            Give it back
          </button>
        </p>
      )}
      {error && <p role="alert" className="msg err">{error}</p>}
    </div>
  );
}
