# web

The Vite + React + TypeScript single-page app ([ADR-0022](../docs/adr/0022-react-vite-typescript-connect-es.md)). The `kondukter` binary embeds its build and serves it at `/`.

The product brand is **Warden**; Kondukter is the backend component and keeps its name in code. The pages carry the owner's design system ([`design.html.html`](design.html.html)): `src/theme.css` (tokens, components, dark and light) on top of `src/reset.css`, with the fonts in `src/fonts/`. See "Design system and stage view, as built" in [docs/WEB_PAGES.md](../docs/WEB_PAGES.md). Every route, its role, its data, its actions and its RPCs are in [docs/WEB_PAGES.md](../docs/WEB_PAGES.md); `src/routes.ts` is the same table as code, and a test keeps the two equal.

```sh
make web        # from the repo root: install, typecheck, test, build into web/dist
make build      # embeds web/dist into bin/kondukter
make web-dev    # Vite on :5173, proxying /api to `kondukter serve` on :8080 (KONDUKTER_API_URL)
VITE_MOCK_API=1 pnpm --dir web dev   # no backend: every page on sample data (logins in docs/WEB_PAGES.md)
```

`web/dist` is build output. Git tracks only `web/dist/.gitkeep`, so Go builds work without Node; a binary built that way answers `503` with a note to run `make web`.

## Layout

| Path | What |
|---|---|
| `src/api/` | The transport (`/api`, cookies), one typed client per service (`useApi()`), query keys, the query client (a 401 from any call signs the user out) |
| `src/auth/` | `useMe()` / `useCurrentUser()`, `RoleRoute` guards, role → home and landing rules |
| `src/hooks/` | `useWatchInstance(id)`: `WatchInstance` as a hook, reconnecting, feeding the query cache |
| `src/components/` | `DataTable`, `Form`, `Field`, `Confirm`, the loading / empty / error states, and the design's `StatusChip`, `Pipeline`, `Terminal`, `Stat`, `ResourceBar`, `FleetMap` |
| `src/pages/` | `/login`, the area frame (nav + logout), and the placeholder every page starts as |
| `src/routes.ts` | The route map; the router, the navigation and the placeholders read it |
| `embed.go` | `go:embed` of `dist` and the SPA handler used by `kondukter` |

The generated client is imported from `gen/ts` through the `@gen/*` alias. `@bufbuild/protobuf` is pinned to the version `protoc-gen-es` generated for (2.16.0) in both the root package and `web/`, so `gen/ts` and the app share one copy.

## Filling in a page (P5.2 to P5.5)

Write the page component under `src/pages/<area>/` and add it to that area's map (`pages/customer/index.tsx` is the example), which `pages/registry.tsx` merges; until a route has an entry it renders a placeholder naming its RPCs. Keep `docs/WEB_PAGES.md` in step: `routes.test.ts` fails when a route or its RPC list differs between the two. Test a page against `test/fakeApi.ts`, an in-memory Kondukter (catalog, keys, instances with a stream you drive with `setPhase`, usage) that records the requests the page made.

Things the demo's setup makes easy to get wrong, all handled in `src/lib/`:

- The app is served over plain HTTP on the tailnet, which browsers treat as an insecure context: `crypto.randomUUID()` and `navigator.clipboard` do not exist there. Use `newId()` and `copyText()`.
- A browser allows about six HTTP/1.1 connections per host and each `WatchInstance` stream holds one. Watch at most a few instances at a time (see `MAX_LIST_STREAMS`).
- TanStack Query hashes keys as JSON, which cannot hold a `bigint`. Keep filters in query keys as plain numbers and convert in the query function.
