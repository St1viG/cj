// Dev-only mock backend: lets `pnpm dev` show every page with no `kondukter
// serve` behind it. It reuses the in-memory servers the page tests use, with
// a little sample data, and sends each call to the one that owns the signed-in
// role. Loaded only when VITE_MOCK_API=1 (see main.tsx); never in a build.
import { Code, ConnectError, createRouterTransport, type Transport } from "@connectrpc/connect";
import { AuthService } from "@gen/kondukter/api/v1/auth_pb";
import { create } from "@bufbuild/protobuf";
import { InstancePhase, MoneySchema, NodeStatus, Role, UsageSchema } from "@gen/kondukter/api/v1/types_pb";
import { fakeApi, instance, runningConnection } from "./test/fakeApi";
import { auditEntry, fakeFleet, gpu, node, site } from "./test/fakeFleet";
import { type AccountName, accounts, principalOf, PASSWORD } from "./test/fakeServer";

export { PASSWORD };

export function createDevMockTransport(): Transport {
  const customer = fakeApi({
    instances: [
      instance("i-7f3c1a2b", InstancePhase.RUNNING, {
        name: "web",
        connection: runningConnection(40001),
        usage: create(UsageSchema, {
          runningSeconds: 7500n,
          runningMinutes: 125n,
          estimatedCost: create(MoneySchema, { amountMinor: 10n, currency: "EUR" }),
        }),
      }),
      instance("i-2b9d4e6c", InstancePhase.PROVISIONING, { name: "training" }),
      instance("i-90aa11ce", InstancePhase.TERMINATED, { name: "old" }),
    ],
  });

  const nodes = [
    node("n-1", "mac-a", { arch: "arm64", driver: "lima", allocatedVcpu: 2 }),
    node("n-2", "mac-b", { arch: "arm64", driver: "lima" }),
    node("n-3", "fed-d", { driver: "libvirt", allocatedVcpu: 4, accelerators: [gpu("GeForce GTX 1060 6GB", true)] }),
    node("n-4", "fed-l", { driver: "libvirt", status: NodeStatus.UNREACHABLE }),
  ];
  const audit = [
    auditEntry("1", "partner.drain_node", "node/n-2"),
    auditEntry("2", "instance.terminate", "instance/i-90aa11ce"),
    auditEntry("3", "admin.quarantine_node", "node/n-4", { actorDisplay: accounts.operator.email }),
  ];
  const mk = (as: "partner" | "operator") =>
    fakeFleet({
      as,
      sites: [site("site-demo", "demo")],
      nodes,
      instances: [
        instance("i-7f3c1a2b", InstancePhase.RUNNING, { name: "web", nodeId: "n-1", orgName: "acme", connection: runningConnection(40001) }),
        instance("i-2b9d4e6c", InstancePhase.RUNNING, { name: "training", nodeId: "n-3", orgName: "acme" }),
        instance("i-55e0aa19", InstancePhase.UNREACHABLE, { name: "batch", nodeId: "n-4", orgName: "acme" }),
        instance("i-0c41b7d2", InstancePhase.PROVISIONING, { name: "dev", nodeId: "n-1", orgName: "acme" }),
      ],
      audit,
    });
  const fleets = { partner: mk("partner"), operator: mk("operator") };

  // The session is the one thing the fakes do not share, so it is handled here.
  let session: AccountName | undefined;
  const auth: Transport = createRouterTransport(({ service }) => {
    service(AuthService, {
      getMe: () => {
        if (!session) throw new ConnectError("missing credentials", Code.Unauthenticated);
        return { principal: principalOf(session) };
      },
      login: (req) => {
        const name = (Object.keys(accounts) as AccountName[]).find((n) => accounts[n].email === req.email);
        if (!name || req.password !== PASSWORD) throw new ConnectError("wrong email or password", Code.Unauthenticated);
        session = name;
        return { principal: principalOf(name) };
      },
      logout: () => {
        session = undefined;
        return {};
      },
    });
  });

  const pick = (service: string): Transport => {
    if (service === AuthService.typeName) return auth;
    if (!session) throw new ConnectError("missing credentials", Code.Unauthenticated);
    const role = accounts[session].role;
    if (role === Role.OPERATOR) return fleets.operator.transport;
    if (role === Role.PARTNER) return fleets.partner.transport;
    return customer.transport;
  };

  return {
    unary: (method, ...rest) => pick(method.parent.typeName).unary(method, ...rest),
    stream: (method, ...rest) => pick(method.parent.typeName).stream(method, ...rest),
  };
}
