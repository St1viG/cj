# Web app — route map and page contents

> **Status:** skeleton built (P5.1) · 2026-11-08 · every route below exists and is guarded; each page shows a placeholder that names its route and RPCs until its roadmap item fills it in (P5.2 customer, P5.3 partner, P5.4 operator, P5.5 stage).
> **Purpose:** the hand-off point between the skeleton work (roadmap P5) and the owner's UI design. The roadmap deliberately does **no** visual design: no component library, theme, layout or styling decisions. This file says, per route, who sees it, what data it shows, which actions it offers and which RPCs back them. The owner designs from here.

Conventions: all routes except `/login` require a session cookie (`AuthService.Login`). Role comes from `AuthService.GetMe`. Every page has loading, empty and error states. Live data uses server streams where listed; everything else uses TanStack Query with a 5 s refetch.

Access rules, as built:

- **One role per area.** `/app` is for customers, `/partner` for partners, `/admin` and `/stage` for operators. A signed-in user who opens another role's area is redirected to their own home: customer `/app/instances`, partner `/partner/nodes`, operator `/admin/nodes`. `/` does the same, or goes to `/login` when signed out.
- **Signed out.** Any guarded route, including an unknown path inside an area, redirects to `/login`; after login the user returns to that page if their role may open it, else lands at their home. `/login` itself sends a user who already has a session to their home.
- **Session ends mid-use.** A call that answers Unauthenticated (expired, logged out in another tab) signs the user out and the guards redirect to `/login`. If `GetMe` fails for any other reason, the page shows the error with Retry; the user is not treated as signed out.
- **The API** is reached at `/api` (the Go server answers the same services at the root for `kctl` and agents). Requests carry the session cookie.
- **Unknown paths** outside every area show "Page not found".

## Routes

| Route | Role | Data shown | Actions | RPCs |
|---|---|---|---|---|
| `/login` | anyone | email + password form; seeded demo accounts hint in dev | Login | `AuthService.Login` |
| `/app` | customer | redirect to `/app/instances` | — | — |
| `/app/offers` | customer | table: accelerator kind / model / count, vCPU, RAM, disk, region, capacity class, price per hour; filters for each column | Create from offer → `/app/instances/new?offer=…` | `CatalogService.ListOffers` |
| `/app/instances/new` | customer | form: offer (preselected or picker), image (filtered to the offer's arch and accelerator stack), SSH keys (multi-select from account), exposed ports (list), optional cloud-init user-data (textarea, 16 KiB cap) | Create → redirect to detail | `CatalogService.ListOffers`, `CatalogService.ListImages`, `AccountService.ListSSHKeys`, `InstanceService.CreateInstance` |
| `/app/instances` | customer | table: id, phase (live), offer, region, node arch, running minutes, estimated cost, created at | Open detail; Terminate (confirm) | `InstanceService.ListInstances`, `InstanceService.WatchInstance`, `InstanceService.TerminateInstance` |
| `/app/instances/:id` | customer | phase + reason (live); event timeline; connection info (`ssh -p <port> ubuntu@<host>` with copy); port mappings; image; SSH keys used; running minutes and estimated cost (both from `Instance.usage`, which every read fills in) | Terminate (confirm); copy SSH command | `InstanceService.GetInstance`, `InstanceService.WatchInstance`, `InstanceService.TerminateInstance` |
| `/app/keys` | customer | SSH keys (name, fingerprint, added); API keys (name, prefix, created, last used) | Add SSH key; delete SSH key; create API key (shown once); revoke API key | `AccountService.*` |
| `/app/usage` | customer | per-instance running minutes and estimated cost for a period; totals | Period filter | `UsageService.ListUsage` |
| `/partner` | partner | redirect to `/partner/nodes` | — | — |
| `/partner/sites` | partner | sites: name, region, node count, nodes by status | Open nodes filtered by site | `PartnerService.ListSites` |
| `/partner/nodes` | partner | table: hostname, status, arch, driver, agent version, vCPU / RAM / disk free vs total, accelerators (model, health), active instance count, last seen. **Never** customer identities or instance specs. | Drain; Undrain | `PartnerService.ListNodes`, `PartnerService.DrainNode`, `PartnerService.UndrainNode` |
| `/partner/enroll` | partner | site picker; last generated token's one-time `kondukter-agent enroll --server … --token …` command with expiry | Create enrollment token | `PartnerService.CreateEnrollmentToken` |
| `/partner/utilization` | partner | per-node utilization numbers for a period (allocated vs total vCPU / RAM / accelerators, instance-hours) as a table | Period filter | `PartnerService.GetUtilization` |
| `/admin` | operator | redirect to `/admin/nodes` | — | — |
| `/admin/nodes` | operator | all nodes across partners: site, status, inventory summary, generation lag (`desired − observed`), last seen | Quarantine; Drain; Undrain | `AdminService.ListNodes`, `AdminService.QuarantineNode`, `PartnerService.DrainNode` |
| `/admin/instances` | operator | all instances: id, org, phase, node, offer, created | Force terminate (confirm) | `AdminService.ListInstances`, `AdminService.ForceTerminateInstance` |
| `/admin/catalog` | operator | shapes, offers, images tables (read-only in the demo) | — | `AdminService.ListCatalog` |
| `/admin/audit` | operator | audit log: time, actor, action, target, expandable payload | Filter by actor / action | `AdminService.ListAuditLog` |
| `/stage` | operator | fleet overview for the projector: nodes grouped by site with status and last seen; each node's non-terminal instances with phase and phase age; updates live | `?demo=1` shortens ids | `AdminService.WatchFleet` (added in P5.5 if polling is insufficient) or `AdminService.ListNodes` + `AdminService.ListInstances` on a 2 s interval |

## Shared skeleton pieces (P5.1)

All under `web/src/`; see [web/README.md](../web/README.md).

- `AuthProvider` / `useMe()` / `useCurrentUser()` (`auth/`) — current user, org and role. `useMe().status` is `loading`, `signedOut` (with `login`), `signedIn` (with `me` and `logout`) or `error` (with `retry`). A 401 from any call signs the user out.
- `RoleRoute` (`auth/`) — guards `/app`, `/partner`, `/admin`, `/stage` as described above.
- `useWatchInstance(id)` (`hooks/`) — wraps the `WatchInstance` server stream and returns `{ status, instance, error }`, where `status` is `idle`, `connecting`, `live`, `reconnecting`, `ended` (the instance is terminal) or `failed` (not found, no permission, signed out). It reopens a broken or server-closed stream with a jittered, growing delay, and writes every update into the cached `GetInstance` and `ListInstances` results (`keys.instances.*`). The detail is refetched when the phase changes, so the timeline stays current.
- `DataTable`, `Form`, `Field`, `Confirm`, `QueryState` (`components/`) — plain unstyled primitives with the loading, empty and error states built in; the owner replaces or restyles them. `Confirm` asks in place (no browser dialog).
- Transport (`api/`) — `@connectrpc/connect-web` to `/api` with `credentials: "include"`, JSON on the wire; one typed client per service from `gen/ts` through `useApi()`.
- Route table (`routes.ts`) — this document's table as data. The router, the area navigation and the placeholders read it; `routes.test.ts` fails if a route or its RPC list differs from the table above, or names an RPC that does not exist in the generated services.

## Customer pages, as built (P5.2)

All six `/app/*` routes render real pages; the rows above are what they show.

- **`/app/offers`**: the table plus filters for region, accelerator, model, architecture, capacity, min vCPU and min RAM, each sent to `ListOffers` (apply with the button, clear with "Clear filters"). The dropdown choices come from the unfiltered list, so they stay put while filtering. "Create instance" opens the form with that offer chosen.
- **`/app/instances/new`**: the offer picker, an architecture choice when the offer has more than one, the images that run on that offer and architecture, an optional name, the account's SSH keys as checkboxes (at least one is required, as in the API), exposed ports (`8080`, `8080/tcp`, `53/udp`, separated by commas or new lines; `22/tcp` is refused because it is always exposed), and optional user-data (at most 16 KiB, counted in bytes). A failed submit shows the server's reason; a retry of the identical request reuses its idempotency key, so it cannot create twice.
- **`/app/instances`**: newest 100 instances (a note says so when there are more), ended ones behind a checkbox. Phase, running minutes and estimated cost come from `Instance.usage`. The list polls every 5 s, and follows at most three instances that are mid-transition (pending, scheduled, provisioning, terminating) with `WatchInstance`, so a phase change shows at once: a browser allows about six HTTP/1.1 connections per host and every stream holds one.
- **`/app/instances/:id`**: status, the `ssh` command with a copy button (it falls back to selecting text on plain HTTP, where `navigator.clipboard` does not exist, and says if even that fails), host, port and user, the port-mapping table, details, usage and the timeline. It follows the instance with one `WatchInstance` stream and refetches the timeline when the phase changes. Terminate asks first and is disabled once the instance is ending or ended.
- **`/app/keys`**: SSH keys (add, delete) and API keys (create, revoke). A new API key's secret is shown once, in a box that stays until dismissed; every creation uses a fresh idempotency key because the server's replay of a key returns the key without its secret.
- **`/app/usage`**: per-instance running minutes and estimated cost, with totals. The period is whole days in the browser's time zone, from the start of "From" to the end of "To"; either may be left empty.

Prices are shown as estimates (ADR-0023); nothing here moves money.

## Explicitly not here

Styling, theming, layout, component library, branding, charts. Sign-up and password reset (no sign-up in the demo, ADR-0021). Billing pages beyond usage and estimated cost (ADR-0023).
