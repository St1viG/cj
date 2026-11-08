# Web app — route map and page contents

> **Status:** stub · 2026-11-08 · filled in during roadmap P5.
> **Purpose:** the hand-off point between the skeleton work (roadmap P5) and the owner's UI design. The roadmap deliberately does **no** visual design: no component library, theme, layout or styling decisions. This file says, per route, who sees it, what data it shows, which actions it offers and which RPCs back them. The owner designs from here.

Conventions: all routes except `/login` require a session cookie (`AuthService.Login`). Role comes from `AuthService.GetMe`. Every page has loading, empty and error states. Live data uses server streams where listed; everything else uses TanStack Query with a 5 s refetch.

## Routes

| Route | Role | Data shown | Actions | RPCs |
|---|---|---|---|---|
| `/login` | anyone | email + password form; seeded demo accounts hint in dev | Login | `AuthService.Login` |
| `/app` | customer | redirect to `/app/instances` | — | — |
| `/app/offers` | customer | table: accelerator kind / model / count, vCPU, RAM, disk, region, capacity class, price per hour; filters for each column | Create from offer → `/app/instances/new?offer=…` | `CatalogService.ListOffers` |
| `/app/instances/new` | customer | form: offer (preselected or picker), image (filtered to the offer's arch and accelerator stack), SSH keys (multi-select from account), exposed ports (list), optional cloud-init user-data (textarea, 16 KiB cap) | Create → redirect to detail | `CatalogService.ListOffers`, `CatalogService.ListImages`, `AccountService.ListSSHKeys`, `InstanceService.CreateInstance` |
| `/app/instances` | customer | table: id, phase (live), offer, region, node arch, running minutes, estimated cost, created at | Open detail; Terminate (confirm) | `InstanceService.ListInstances`, `InstanceService.WatchInstance`, `InstanceService.TerminateInstance` |
| `/app/instances/:id` | customer | phase + reason (live); event timeline; connection info (`ssh -p <port> ubuntu@<host>` with copy); port mappings; image; SSH keys used; running minutes and estimated cost | Terminate (confirm); copy SSH command | `InstanceService.GetInstance`, `InstanceService.WatchInstance`, `UsageService.ListUsage`, `InstanceService.TerminateInstance` |
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
| `/stage` | operator | fleet overview for the projector: nodes grouped by site with status and last seen; each node's non-terminal instances with phase and phase age; updates live | `?demo=1` shortens ids | `AdminService.WatchFleet` (added in P5.5 if polling is insufficient) or `AdminService.ListNodes` + `ListInstances` on a 2 s interval |

## Shared skeleton pieces (P5.1)

- `AuthProvider` / `useMe()` — current user, org, role; redirects on 401.
- `RoleRoute` — guards `/app`, `/partner`, `/admin`, `/stage`.
- `useWatchInstance(id)` — wraps the `WatchInstance` server stream, reconnects on error, updates the query cache.
- `DataTable`, `Form`, `Field`, `Confirm` — plain unstyled primitives; the owner replaces or restyles them.
- Transport — `@connectrpc/connect-web` to `/api`, cookies included, generated clients from `gen/ts`.

## Explicitly not here

Styling, theming, layout, component library, branding, charts. Sign-up and password reset (no sign-up in the demo, ADR-0021). Billing pages beyond usage and estimated cost (ADR-0023).
