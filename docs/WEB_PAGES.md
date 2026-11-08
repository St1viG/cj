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
| `/partner/sites` | partner | sites: name, region, country, network mode, node count, nodes by status, created | Open nodes filtered by site; enroll a node at the site | `PartnerService.ListSites` |
| `/partner/nodes` | partner | table: hostname, site, status + reason, arch, driver, agent version, vCPU / RAM / disk free vs total, accelerators (model, health), active instance count, last seen. **Never** customer identities or instance specs. | Site filter; Drain (confirm, optional reason); Undrain (confirm) | `PartnerService.ListNodes`, `PartnerService.DrainNode`, `PartnerService.UndrainNode`, `PartnerService.ListSites` |
| `/partner/enroll` | partner | site picker; last generated token's one-time `install-agent.sh --server … --token …` command with expiry | Create enrollment token; copy command | `PartnerService.CreateEnrollmentToken`, `PartnerService.ListSites` |
| `/partner/utilization` | partner | per-node utilization numbers for a period (allocated vs allocatable vCPU / RAM / disk / accelerators, active instances, instance minutes) as a table; the period and total | Site filter; period filter | `PartnerService.GetUtilization`, `PartnerService.ListSites` |
| `/admin` | operator | redirect to `/admin/nodes` | — | — |
| `/admin/nodes` | operator | all nodes across partners: partner, site, status, inventory summary, active instances (link to them), generation lag (`desired − observed`), last seen | Site and status filters; Quarantine (optional reason); Unquarantine; Drain; Undrain | `AdminService.ListNodes`, `AdminService.QuarantineNode`, `AdminService.UnquarantineNode`, `PartnerService.DrainNode`, `PartnerService.UndrainNode`, `PartnerService.ListSites` |
| `/admin/instances` | operator | all instances: id, name, org, phase + reason, time in phase, node, offer and price, region, arch, created | Org, node and ended filters; Force terminate (confirm, optional reason) | `AdminService.ListInstances`, `AdminService.ForceTerminateInstance`, `AdminService.ListNodes` |
| `/admin/catalog` | operator | offers, shapes, images and regions tables (read-only in the demo) | — | `AdminService.ListCatalog` |
| `/admin/audit` | operator | audit log: time, actor, action, target, expandable payload | Filter by actor / action; older and newest pages | `AdminService.ListAuditLog` |
| `/stage` | operator | fleet overview for the projector: nodes grouped by site with status and last seen; each node's non-terminal instances with phase and phase age; updates live | `?demo=1` shortens ids | `AdminService.ListNodes` + `AdminService.ListInstances`, polled every 2 s (no `WatchFleet`: see "Stage page, as built") |

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

## Partner pages, as built (P5.3)

All four `/partner/*` routes render real pages. Node status and accelerator health read in lower case, as phases do on the customer pages (`draining (kernel update)`).

- **`/partner/sites`**: the table above. Each row links to its nodes (`/partner/nodes?site=…`) and to enrollment with that site chosen (`/partner/enroll?site=…`).
- **`/partner/nodes`**: the table above, refetched every 5 s. Free is total minus reserved minus allocated, shown as `6 of 8`. Accelerators read `GeForce GTX 1060 6GB (ok, in use)`. Last seen is an age (`12 s ago`, the time as a tooltip) that keeps counting between refetches. The site filter is `?site=` in the URL and is sent to `ListNodes`. Drain is offered only on a ready node and Undrain only on a draining or maintenance one, as the server's transitions allow. Both ask in place; Drain takes an optional reason, which the server keeps as the status reason and in the audit log (`drained by <email>` when empty). A drained node with no instances left goes to maintenance on its own.
- **`/partner/enroll`**: the site picker (`?site=` chooses one), then the command exactly as the server words it, with Copy and the expiry. The page says it is shown once and works once; Done removes it. Every Create sends a fresh idempotency key, because the token cannot be shown again.
- **`/partner/utilization`**: the period the server used (with no dates, the last 24 hours) and the total instance minutes. Per node: vCPU, RAM, disk and accelerators allocated now of allocatable (`2 of 8 (25%)`), active instances and instance minutes in the period. The site filter is the same `?site=` as on the nodes page; the period is whole local days, "To" included, as on `/app/usage`.

`nodeColumns` and `DrainActions` (`pages/nodes.tsx`) are shared with `/admin/nodes`; `SiteFilter` and `useSites` are in `pages/partner/sites.tsx`; `useParam` (`hooks/`) binds one search param to a filter; `Confirm` takes an optional `reason` field.

## Operator pages, as built (P5.4)

All four `/admin/*` routes render real pages; a customer or partner who opens one lands at their own home.

- **`/admin/nodes`**: the partner node table plus partner, generation lag (`1 behind (desired 5, observed 4)`, else `0 (4)`) and the active count as a link to `/admin/instances?node=…`. Filters `?site=` (every partner's sites) and `?status=` (`quarantined`), both sent to `ListNodes`. Quarantine is offered on every node but a quarantined or retired one, with an optional reason (`quarantined by <email>` when empty); it stops placements and the server ignores the node's reports, but running instances are not stopped. Unquarantine returns the node to ready, or to unreachable until its agent connects. Drain and Undrain are the partner page's.
- **`/admin/instances`**: every org's instances, newest 200 (a note says when there are more). `?all=1` adds terminated, failed and lost ones; the org and node cells are buttons that set `?org=` and `?node=`, with "Any org" and "Any node" to clear them. Force terminate asks in place with an optional reason, stored as `force-terminated by <email>: <reason>` and in the audit log; it is disabled once the instance is ending or ended.
- **`/admin/catalog`**: offers (with "for sale" and the architectures that have an image), shapes, images (with status, so deprecated ones show) and regions.
- **`/admin/audit`**: 50 entries a page, newest first, with "Older entries" and "Newest entries" (`?page=` is the server's token). `?action=` is a select of the actions the server writes; the actor cell is a button that sets `?actor=`. Changing a filter goes back to the newest page. The target is shortened (`node/7f3c1a2b`), the payload is one line that opens to the whole JSON.

## Stage page, as built (P5.5)

`/stage` (operator, no area header) is the projector view for the failure drills. Plain nested lists: each site, under it each node with its status (and the reason, such as "unreachable (missed heartbeats)") and when it was last heard from, and under each node the instances that are not finished, each with its phase and how long it has been in it. The ages count up every second between polls.

- **Polling, not a stream.** It polls `AdminService.ListNodes` and `ListInstances` every 2 s, and `AdminService.WatchFleet` was never added to the proto. A drill's changes last tens of seconds, so a poll shows each one within 2 s. Measured against a real `kondukter serve` with mock-driver agents, running failure drill 1 (SIGKILL the agent, then restart it): the node and its instance showed UNREACHABLE after 40 s, within the 45 s the drill expects, and were READY and RUNNING 2 s after the restart, with no reload. A tab that is hidden stops polling, so keep the projector tab in front.
- **Contact lost.** If a poll fails (the control plane is restarting, drill 2), the last fleet stays on screen under "Lost contact with Kondukter … Showing what it said at 14:05:09, 12 s ago. Still trying." The banner clears on the next successful poll, with no reload and without signing the operator out.
- **What is left out.** Retired nodes, and instances that ended, failed or were lost. An instance on no node yet (pending), or on a node the list does not show, appears under "Not on a listed node". If there are more than 200 unfinished instances, the newest 200 are shown, with a note.
- **Reasons.** An instance's phase is followed by the control plane's reason when it is a problem or a wait ("pending (no_capacity)", "unreachable (node heartbeats lost)"). For `running` it is left out: the reason there is only ever the normal one, `agent_running`.
- **`?demo=1`** shows an instance by its name, or by the first 8 characters of its id when it has none; without it, the name with the whole id. Nodes always show their hostname.
- Order is stable (sites and hostnames by name, instances oldest first), so a poll that changes a status changes that line and moves nothing else.

## Explicitly not here

Styling, theming, layout, component library, branding, charts. Sign-up and password reset (no sign-up in the demo, ADR-0021). Billing pages beyond usage and estimated cost (ADR-0023).
