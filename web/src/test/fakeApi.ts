import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { Code, ConnectError, createRouterTransport, type Transport } from "@connectrpc/connect";
import {
  type APIKey,
  APIKeySchema,
  AccountService,
  type SSHKey,
  SSHKeySchema,
} from "@gen/kondukter/api/v1/account_pb";
import { AuthService } from "@gen/kondukter/api/v1/auth_pb";
import {
  CatalogService,
  type Image,
  ImageSchema,
  type ListOffersRequest,
  type Offer,
  OfferSchema,
  ShapeSchema,
} from "@gen/kondukter/api/v1/catalog_pb";
import {
  ConnectionSchema,
  type CreateInstanceRequest,
  type Instance,
  InstanceEventSchema,
  InstanceSchema,
  InstanceService,
  type InstanceEvent,
} from "@gen/kondukter/api/v1/instance_pb";
import { UsageService, type ListUsageRequest } from "@gen/kondukter/api/v1/usage_pb";
import {
  CapacityClass,
  InstancePhase,
  MoneySchema,
  PortMappingSchema,
  Protocol,
  UsageSchema,
} from "@gen/kondukter/api/v1/types_pb";
import { isTerminalPhase } from "../hooks/useWatchInstance";
import { principalOf } from "./fakeServer";

const at = (iso: string) => timestampFromDate(new Date(iso));
const eur = (cents: number) => create(MoneySchema, { amountMinor: BigInt(cents), currency: "EUR" });

/** The seeded demo catalog, shaped like `kondukter seed catalog`. */
export function demoCatalog() {
  const cpu = create(ShapeSchema, { id: "shape-cpu", name: "cpu-2x2", vcpu: 2, ramMib: 2048n, diskGib: 20n });
  const gpu = create(ShapeSchema, {
    id: "shape-gpu",
    name: "gpu-gtx1060-1x",
    vcpu: 8,
    ramMib: 16384n,
    diskGib: 100n,
    acceleratorKind: "nvidia",
    acceleratorModel: "GeForce GTX 1060 6GB",
    acceleratorCount: 1,
  });
  const offers: Offer[] = [
    create(OfferSchema, {
      id: "offer-cpu",
      shape: cpu,
      region: "demo",
      capacityClass: CapacityClass.DEDICATED,
      pricePerHour: eur(5),
      active: true,
      archs: ["amd64", "arm64"],
    }),
    create(OfferSchema, {
      id: "offer-gpu",
      shape: gpu,
      region: "demo",
      capacityClass: CapacityClass.DEDICATED,
      pricePerHour: eur(40),
      active: true,
      archs: ["amd64"],
    }),
  ];
  const images: Image[] = [
    create(ImageSchema, { id: "img-cpu-amd64", name: "ubuntu", version: "24.04", arch: "amd64", status: "active" }),
    create(ImageSchema, { id: "img-cpu-arm64", name: "ubuntu", version: "24.04", arch: "arm64", status: "active" }),
    create(ImageSchema, {
      id: "img-gpu-amd64",
      name: "ubuntu-gpu-pascal",
      version: "24.04-580",
      arch: "amd64",
      accelStack: "nvidia-580-cuda12",
      status: "active",
    }),
  ];
  return { offers, images };
}

function offerMatches(o: Offer, f: ListOffersRequest): boolean {
  const s = o.shape;
  return (
    (!f.region || o.region === f.region) &&
    (f.acceleratorKind === "none" ? (s?.acceleratorCount ?? 0) === 0 : !f.acceleratorKind || s?.acceleratorKind === f.acceleratorKind) &&
    (!f.acceleratorModel || s?.acceleratorModel === f.acceleratorModel) &&
    (!f.arch || o.archs.includes(f.arch)) &&
    (f.capacityClass === CapacityClass.UNSPECIFIED || o.capacityClass === f.capacityClass) &&
    (s?.vcpu ?? 0) >= f.minVcpu &&
    (s?.ramMib ?? 0n) >= f.minRamMib
  );
}

/** Fields of an instance to change; the values are real messages, not init shapes. */
export type InstancePatch = Partial<Omit<Instance, "$typeName" | "$unknown">>;

export interface FakeApiOptions {
  offers?: Offer[];
  images?: Image[];
  sshKeys?: SSHKey[];
  apiKeys?: APIKey[];
  instances?: Instance[];
}

export function sshKey(id: string, name: string): SSHKey {
  return create(SSHKeySchema, {
    id,
    name,
    publicKey: `ssh-ed25519 AAAA${id} ${name}`,
    fingerprint: `SHA256:${id}fp`,
    createdAt: at("2026-11-01T10:00:00Z"),
  });
}

export function instance(id: string, phase: InstancePhase, init: MessageInitShape<typeof InstanceSchema> = {}): Instance {
  return create(InstanceSchema, {
    id,
    name: "",
    offerId: "offer-cpu",
    shape: create(ShapeSchema, { id: "shape-cpu", name: "cpu-2x2", vcpu: 2, ramMib: 2048n, diskGib: 20n }),
    pricePerHour: eur(5),
    region: "demo",
    imageId: "img-cpu-amd64",
    imageName: "ubuntu 24.04",
    arch: "amd64",
    phase,
    phaseChangedAt: at("2026-11-08T09:00:00Z"),
    createdAt: at("2026-11-08T09:00:00Z"),
    usage: create(UsageSchema, { runningSeconds: 0n, runningMinutes: 0n, estimatedCost: eur(0) }),
    ...init,
  });
}

export function runningConnection(port = 40001) {
  return create(ConnectionSchema, {
    host: "100.64.0.5",
    sshPort: port,
    sshUser: "ubuntu",
    sshCommand: `ssh -p ${port} ubuntu@100.64.0.5`,
    portMappings: [
      create(PortMappingSchema, { guestPort: 22, protocol: Protocol.TCP, hostAddress: "100.64.0.5", hostPort: port }),
      create(PortMappingSchema, { guestPort: 8080, protocol: Protocol.TCP, hostAddress: "100.64.0.5", hostPort: port + 1 }),
    ],
  });
}

/**
 * An in-memory Kondukter for page tests: the catalog, the caller's keys and
 * instances, usage, and a WatchInstance stream the test drives with
 * `setPhase`. Signed in as the demo customer. `calls` records the requests the
 * page made, so a test can assert what was sent.
 */
export function fakeApi(opts: FakeApiOptions = {}) {
  const catalog = demoCatalog();
  const offers = opts.offers ?? catalog.offers;
  const images = opts.images ?? catalog.images;
  const sshKeys = opts.sshKeys ?? [sshKey("k1", "laptop"), sshKey("k2", "ci")];
  const apiKeys: APIKey[] = opts.apiKeys ?? [];
  const store = new Map<string, Instance>((opts.instances ?? []).map((i) => [i.id, i]));
  const events = new Map<string, InstanceEvent[]>();
  const listeners = new Map<string, Set<(i: Instance) => void>>();
  let seq = 0;

  const calls = {
    listOffers: [] as ListOffersRequest[],
    listImages: [] as Array<{ offerId: string; arch: string }>,
    createInstance: [] as CreateInstanceRequest[],
    terminate: [] as string[],
    addSSHKey: [] as Array<{ name: string; publicKey: string }>,
    deleteSSHKey: [] as string[],
    createAPIKey: [] as Array<{ name: string; idempotencyKey: string }>,
    revokeAPIKey: [] as string[],
    listUsage: [] as ListUsageRequest[],
    watches: [] as string[],
    listInstances: [] as boolean[],
    getInstance: [] as string[],
    listSSHKeys: [] as number[],
  };

  /** Make a method fail with this error until the test removes it. */
  const fail: Partial<Record<keyof typeof calls, ConnectError>> = {};
  function maybeFail(method: keyof typeof calls) {
    const err = fail[method];
    if (err) throw err;
  }

  function notFound(): never {
    throw new ConnectError("instance not found", Code.NotFound);
  }
  function get(id: string): Instance {
    return store.get(id) ?? notFound();
  }

  /** Moves an instance to a new phase, records the event and wakes its watchers. */
  function setPhase(id: string, phase: InstancePhase, patch: InstancePatch = {}) {
    const prev = get(id);
    const next: Instance = { ...prev, ...patch, phase, phaseChangedAt: at("2026-11-08T09:30:00Z") };
    store.set(id, next);
    events.set(id, [
      ...(events.get(id) ?? []),
      create(InstanceEventSchema, { fromPhase: prev.phase, toPhase: phase, reason: patch.phaseReason ?? "", at: at("2026-11-08T09:30:00Z") }),
    ]);
    for (const l of listeners.get(id) ?? []) l(next);
  }

  function nextUpdate(id: string, signal: AbortSignal): Promise<Instance | undefined> {
    return new Promise((resolve) => {
      const set = listeners.get(id) ?? new Set();
      listeners.set(id, set);
      const done = (v: Instance | undefined) => {
        set.delete(onUpdate);
        signal.removeEventListener("abort", onAbort);
        resolve(v);
      };
      const onUpdate = (i: Instance) => done(i);
      const onAbort = () => done(undefined);
      set.add(onUpdate);
      signal.addEventListener("abort", onAbort);
    });
  }

  const transport: Transport = createRouterTransport(({ service }) => {
    service(AuthService, {
      getMe: () => ({ principal: principalOf("customer") }),
      login: () => ({ principal: principalOf("customer") }),
      logout: () => ({}),
    });

    service(CatalogService, {
      listOffers: (req) => {
        calls.listOffers.push(req);
        maybeFail("listOffers");
        return { offers: offers.filter((o) => o.active && offerMatches(o, req)) };
      },
      listImages: (req) => {
        calls.listImages.push({ offerId: req.offerId, arch: req.arch });
        const offer = offers.find((o) => o.id === req.offerId);
        if (req.offerId && !offer) throw new ConnectError("offer not found", Code.NotFound);
        const gpu = (offer?.shape?.acceleratorCount ?? 0) > 0;
        return { images: images.filter((i) => (!req.arch || i.arch === req.arch) && (!offer || (i.accelStack !== "") === gpu)) };
      },
    });

    service(AccountService, {
      listSSHKeys: () => {
        calls.listSSHKeys.push(calls.listSSHKeys.length);
        maybeFail("listSSHKeys");
        return { sshKeys: [...sshKeys] };
      },
      addSSHKey: (req) => {
        calls.addSSHKey.push({ name: req.name, publicKey: req.publicKey });
        if (!req.publicKey.startsWith("ssh-")) throw new ConnectError("public_key: not an OpenSSH public key", Code.InvalidArgument);
        const key = sshKey(`k${sshKeys.length + 10}`, req.name);
        sshKeys.push(key);
        return { sshKey: key };
      },
      deleteSSHKey: (req) => {
        calls.deleteSSHKey.push(req.id);
        sshKeys.splice(0, sshKeys.length, ...sshKeys.filter((k) => k.id !== req.id));
        return {};
      },
      listAPIKeys: () => ({ apiKeys: [...apiKeys] }),
      createAPIKey: (req) => {
        calls.createAPIKey.push({ name: req.name, idempotencyKey: req.idempotencyKey });
        const key = create(APIKeySchema, { id: `ak${apiKeys.length + 1}`, name: req.name, prefix: "kdk_newk", createdAt: at("2026-11-08T09:00:00Z") });
        apiKeys.push(key);
        return { apiKey: key, secret: "kdk_newkeysecret0123456789" };
      },
      revokeAPIKey: (req) => {
        calls.revokeAPIKey.push(req.id);
        const i = apiKeys.findIndex((k) => k.id === req.id);
        const found = apiKeys[i];
        if (!found) throw new ConnectError("API key not found", Code.NotFound);
        const revoked: APIKey = { ...found, revokedAt: at("2026-11-08T09:45:00Z") };
        apiKeys[i] = revoked;
        return { apiKey: revoked };
      },
    });

    service(InstanceService, {
      createInstance: (req) => {
        calls.createInstance.push(req);
        maybeFail("createInstance");
        if (req.sshKeyIds.length === 0 && req.sshPublicKeys.length === 0) {
          throw new ConnectError("at least one SSH key is required (ssh_key_ids or ssh_public_keys)", Code.InvalidArgument);
        }
        const offer = offers.find((o) => o.id === req.offerId);
        if (!offer) throw new ConnectError("offer_id: unknown offer", Code.InvalidArgument);
        const created = instance(`inst-${++seq}-0000-0000-0000-000000000000`, InstancePhase.PENDING, {
          name: req.name,
          offerId: offer.id,
          shape: offer.shape,
          imageId: req.imageId,
          ports: req.ports,
        });
        store.set(created.id, created);
        events.set(created.id, [create(InstanceEventSchema, { toPhase: InstancePhase.PENDING, reason: "created", at: at("2026-11-08T09:00:00Z") })]);
        return { instance: created };
      },
      getInstance: (req) => {
        calls.getInstance.push(req.id);
        maybeFail("getInstance");
        return { instance: get(req.id), events: events.get(req.id) ?? [] };
      },
      listInstances: (req) => {
        calls.listInstances.push(req.includeTerminal);
        maybeFail("listInstances");
        const all = [...store.values()].reverse();
        return { instances: req.includeTerminal ? all : all.filter((i) => !isTerminalPhase(i.phase)) };
      },
      terminateInstance: (req) => {
        calls.terminate.push(req.id);
        maybeFail("terminate");
        setPhase(req.id, InstancePhase.TERMINATING, { phaseReason: "terminated by customer@demo.local" });
        return { instance: get(req.id) };
      },
      watchInstance: async function* (req, ctx) {
        calls.watches.push(req.id);
        let current = get(req.id);
        yield { instance: current };
        while (!isTerminalPhase(current.phase) && !ctx.signal.aborted) {
          const next = await nextUpdate(req.id, ctx.signal);
          if (!next) return;
          current = next;
          yield { instance: current };
        }
      },
    });

    service(UsageService, {
      listUsage: (req) => {
        calls.listUsage.push(req);
        const rows = [...store.values()].map((i) => ({
          instanceId: i.id,
          instanceName: i.name,
          offerId: i.offerId,
          shapeName: i.shape?.name ?? "",
          phase: i.phase,
          pricePerHour: i.pricePerHour,
          usage: i.usage,
        }));
        const minutes = rows.reduce((n, r) => n + Number(r.usage?.runningMinutes ?? 0n), 0);
        const cents = rows.reduce((n, r) => n + Number(r.usage?.estimatedCost?.amountMinor ?? 0n), 0);
        return { instances: rows, totalRunningMinutes: BigInt(minutes), totalEstimatedCost: rows.length ? [eur(cents)] : [] };
      },
    });
  });

  return {
    transport,
    calls,
    setPhase,
    fail,
    /** Replaces an instance as the server now has it, without a phase change. */
    update: (id: string, patch: InstancePatch) => {
      const next: Instance = { ...get(id), ...patch };
      store.set(id, next);
      for (const l of listeners.get(id) ?? []) l(next);
    },
    instances: store,
    sshKeys,
    apiKeys,
  };
}

export type FakeApi = ReturnType<typeof fakeApi>;
