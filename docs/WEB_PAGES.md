# Web app — route map and page contents

> **Status:** skeleton built (P5.1) · 2026-11-08 · every route below exists and is guarded; each page shows a placeholder that names its route and RPCs until its roadmap item fills it in (P5.2 customer, P5.3 partner, P5.4 operator, P5.5 stage).
> **Purpose:** the hand-off point between the skeleton work (roadmap P5) and the owner's UI design. The roadmap deliberately does **no** visual design: no component library, theme, layout or styling decisions. This file says, per route, who sees it, what data it shows, which actions it offers and which RPCs back them. The owner designs from here.

Conventions: all routes except `/` and `/login` require a session cookie (`AuthService.Login`). Role comes from `AuthService.GetMe`. Every page has loading, empty and error states. Live data uses server streams where listed; everything else uses TanStack Query with a 5 s refetch.

Access rules, as built:

- **One role per area.** `/app` is for customers, `/partner` for partners, `/admin` and `/stage` for operators. A signed-in user who opens another role's area is redirected to their own home: customer `/app/instances`, partner `/partner/nodes`, operator `/admin/nodes`. `/` does the same, or goes to `/login` when signed out.
- **Signed out.** Any guarded route, including an unknown path inside an area, redirects to `/login`; after login the user returns to that page if their role may open it, else lands at their home. `/login` itself sends a user who already has a session to their home.
- **Session ends mid-use.** A call that answers Unauthenticated (expired, logged out in another tab) signs the user out and the guards redirect to `/login`. If `GetMe` fails for any other reason, the page shows the error with Retry; the user is not treated as signed out.
- **The API** is reached at `/api` (the Go server answers the same services at the root for `kctl` and agents). Requests carry the session cookie.
- **Unknown paths** outside every area show "Page not found".

## Routes

| Route | Role | Data shown | Actions | RPCs |
|---|---|---|---|---|
| `/` | anyone | the guest home page: what Kondukter does, how it works, the demo catalog and who it is for; the animated lifecycle preview | Log in, or Open dashboard when signed in | — |
| `/products` | anyone | the product catalog in the manner of a GPU price-comparison list: type tabs, filters (GPU model, architecture, availability, max price), price per hour / day / month, sort, table or card view. A visitor sees the demo catalog; a signed-in customer sees the live offers | Launch (log in first for a visitor; the create form for a customer) | `CatalogService.ListOffers` (customers only) |
| `/login` | anyone | email + password form; seeded demo accounts hint in dev | Login | `AuthService.Login` |
| `/app` | customer | redirect to `/app/instances` | — | — |
| `/app/offers` | customer | table: accelerator kind / model / count, vCPU, RAM, disk, region, capacity class, price per hour; filters for each column | Create from offer → `/app/instances/new?offer=…` | `CatalogService.ListOffers` |
| `/app/instances/new` | customer | form: offer (preselected or picker), image (filtered to the offer's arch and accelerator stack), SSH keys (multi-select from account), exposed ports (list), optional cloud-init user-data (textarea, 16 KiB cap) | Create → redirect to detail | `CatalogService.ListOffers`, `CatalogService.ListImages`, `AccountService.ListSSHKeys`, `InstanceService.CreateInstance` |
| `/app/instances` | customer | table: id, phase (live), offer, region, node arch, running minutes, estimated cost, created at | Open detail; Terminate (confirm) | `InstanceService.ListInstances`, `InstanceService.WatchInstance`, `InstanceService.TerminateInstance` |
| `/app/instances/:id` | customer | phase + reason (live); event timeline; connection info (`ssh -p <port> ubuntu@<host>` with copy); port mappings; image; SSH keys used; running minutes and estimated cost (both from `Instance.usage`, which every read fills in) | Terminate (confirm); copy SSH command | `InstanceService.GetInstance`, `InstanceService.WatchInstance`, `InstanceService.TerminateInstance` |
| `/app/keys` | customer | SSH keys (name, fingerprint, added); API keys (name, prefix, created, last used) | Add SSH key; delete SSH key; create API key (shown once); revoke API key | `AccountService.*` |
| `/app/billing` | customer | demo data generated in the browser: balance, spent in 30 days, daily burn and runway, a balance-history chart and a daily-spend chart, the ledger (top-ups, usage, bonus, refund) with filters, invoices, a pretend payment method | Add credit (a pretend top-up, kept in localStorage) | — |
| `/app/usage` | customer | per-instance running minutes and estimated cost for a period; totals | Period filter | `UsageService.ListUsage` |
| `/partner` | partner | redirect to `/partner/nodes` | — | — |
| `/partner/earnings` | partner | demo data generated in the browser: earned in 30 days, next payout, average utilization, lifetime, a daily-earnings chart, earnings by node, payout history; an illustrative 70% revenue share | — | — |
| `/partner/sites` | partner | sites: name, region, country, network mode, node count, nodes by status, created | Open nodes filtered by site; enroll a node at the site | `PartnerService.ListSites` |
| `/partner/nodes` | partner | table: hostname, site, status + reason, arch, driver, agent version, vCPU / RAM / disk free vs total, accelerators (model, health), active instance count, last seen. **Never** customer identities or instance specs. | Site filter; Drain (confirm, optional reason); Undrain (confirm) | `PartnerService.ListNodes`, `PartnerService.DrainNode`, `PartnerService.UndrainNode`, `PartnerService.ListSites` |
| `/partner/enroll` | partner | site picker; last generated token's one-time `install-agent.sh --server … --token …` command with expiry | Create enrollment token; copy command | `PartnerService.CreateEnrollmentToken`, `PartnerService.ListSites` |
| `/partner/utilization` | partner | per-node utilization numbers for a period (allocated vs allocatable vCPU / RAM / disk / accelerators, active instances, instance minutes) as a table; the period and total | Site filter; period filter | `PartnerService.GetUtilization`, `PartnerService.ListSites` |
| `/admin` | operator | redirect to `/admin/nodes` | — | — |
| `/admin/nodes` | operator | all nodes across partners: partner, site, status, inventory summary, active instances (link to them), generation lag (`desired − observed`), last seen | Site and status filters; Quarantine (optional reason); Unquarantine; Drain; Undrain | `AdminService.ListNodes`, `AdminService.QuarantineNode`, `AdminService.UnquarantineNode`, `PartnerService.DrainNode`, `PartnerService.UndrainNode`, `PartnerService.ListSites` |
| `/admin/instances` | operator | all instances: id, name, org, phase + reason, time in phase, node, offer and price, region, arch, created | Org, node and ended filters; Force terminate (confirm, optional reason) | `AdminService.ListInstances`, `AdminService.ForceTerminateInstance`, `AdminService.ListNodes` |
| `/admin/catalog` | operator | offers, shapes, images and regions tables (read-only in the demo) | — | `AdminService.ListCatalog` |
| `/admin/audit` | operator | audit log: time, actor, action, target, expandable payload | Filter by actor / action; older and newest pages | `AdminService.ListAuditLog` |
| `/stage` | operator | fleet overview for the projector: nodes grouped by site with status and last seen; each node's non-terminal instances with phase and phase age; updates live | `?demo=1` shortens ids | `AdminService.WatchFleet` (added in P5.5 if polling is insufficient) or `AdminService.ListNodes` + `AdminService.ListInstances` on a 2 s interval |

## Shared skeleton pieces (P5.1)

All under `web/src/`; see [web/README.md](../web/README.md).

- `AuthProvider` / `useMe()` / `useCurrentUser()` (`auth/`) — current user, org and role. `useMe().status` is `loading`, `signedOut` (with `login`), `signedIn` (with `me` and `logout`) or `error` (with `retry`). A 401 from any call signs the user out.
- `RoleRoute` (`auth/`) — guards `/app`, `/partner`, `/admin`, `/stage` as described above.
- `useWatchInstance(id)` (`hooks/`) — wraps the `WatchInstance` server stream and returns `{ status, instance, error }`, where `status` is `idle`, `connecting`, `live`, `reconnecting`, `ended` (the instance is terminal) or `failed` (not found, no permission, signed out). It reopens a broken or server-closed stream with a jittered, growing delay, and writes every update into the cached `GetInstance` and `ListInstances` results (`keys.instances.*`). The detail is refetched when the phase changes, so the timeline stays current.
- `DataTable`, `Form`, `Field`, `Confirm`, `QueryState` (`components/`) — primitives with the loading, empty and error states built in; the owner replaces or restyles them. `Confirm` asks in place (no browser dialog).
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

## Design system and stage view, as built

**Brand:** the product is called **Warden** everywhere a user can see it (wordmark, page title, copy). **Kondukter** is the name of the control-plane component, so it stays in the backend: the binaries, protobuf packages, `kctl`, `kondukter seed accounts` and the commands the server prints.

The owner's design ([`web/design.html.html`](../web/design.html.html), a token sheet from Claude Design) is applied across the app.

- **Tokens and components:** `web/src/theme.css` holds the whole system as CSS custom properties on `:root` (dark first; `<html data-theme="light">` swaps the set) and the design's component classes: `.btn`, `.card`, `.tbl`, `.chip`/`.led`, `.pipe`, `.tl`, `.term`, `.stat`, `.cell`, `.conf`, `.empty`/`.errp`. Fraunces (display), Geist (UI) and Geist Mono (data) are bundled from `web/src/fonts/`. The theme choice is kept in `localStorage` (`kdk-theme`), with a switch in the rail.
- **One navigation bar:** `SiteNav` is the single top bar on every page except `/stage`: wordmark, Home, Products, and (signed in) Dashboard, then the theme switch and either Log in or the account controls (LIVE/SYNC indicator, email, role, Log out). Dashboard pages add a tab row under it (`AreaLayout`, numbered, one `<nav>` per area). The home page is open to signed-in users too; its buttons turn into Open dashboard.
- **Page headers:** `PageTitle` renders each dashboard page's `<h1>` in the home page's hero treatment: an area label, the serif title and a one-line description from `lib/pageBlurbs.ts`, on a soft glow.
- **`/products`:** guests see the demo catalog (`lib/products.ts`: cpu-2x2, gpu-gtx1060-1x, and the laptop GPU marked soon); a signed-in customer sees the live `ListOffers` and launches into `/app/instances/new?offer=…`. Filters, sort, price per hour/day/month and a table or card view are client-side.
- **Components added:** `StatusChip` (glyph + colour + word for instance phases and node statuses), `Pipeline` (the lifecycle stepper on the instance page), `Terminal` (the copyable `ssh` line), `Stat`, `ResourceBar`, `FleetMap` and `Logo`. Decorative words (pipeline steps, map legend, stat labels) are drawn from `data-*` attributes, so the page's text holds a phase or status once.
- **Nodes:** `/partner/nodes` and `/admin/nodes` show three telemetry tiles and a Table / Map switch (`?view=map`); the map is one tile per node, filled to its vCPU use, with a dot per GPU.
- **`/stage` (P5.5):** nodes grouped by site as large cards, each with its status and its live instances with the time in phase. It polls `AdminService.ListNodes` and `ListInstances` every 2 s (no `WatchFleet`), pulses a node that goes unreachable, and `?demo=1` shortens ids to 4 characters.
- **Fake money (demo only):** the backend has no ledger (ADR-0023), so `/app/billing` and `/partner/earnings` run on data generated in the browser (`lib/fakeBilling.ts`, `lib/fakeEarnings.ts`): thirty days of top-ups, usage, a refund and invoices that end on a sensible balance, and a fixed-seed set of partner earnings and payouts. The pages carry no on-screen label saying so; this note is the record. A pretend top-up is added to the history and kept in `localStorage` (`kdk-fake-topups`). The customer's balance also shows in the navigation bar. Charts are the single-series `TimeChart` (line or bars, hover/arrow-key readout).
- **Home page price pitch:** a banner, price chips in the hero and a "Cheap by design" section with a cost calculator over the real demo prices (€0.05/hr CPU, €0.40/hr GPU).
- **Dev preview:** `VITE_MOCK_API=1 pnpm dev` runs every page against an in-memory backend (`src/devMock.ts`, built from the test fakes) with the three seeded logins and the password `cj`; it is excluded from builds.

## Explicitly not here

Charts beyond the resource bars and the fleet map, branding assets beyond the wordmark. Sign-up and password reset (no sign-up in the demo, ADR-0021). Billing pages beyond usage and estimated cost (ADR-0023).
