# web

The Vite + React + TypeScript single-page app ([ADR-0022](../docs/adr/0022-react-vite-typescript-connect-es.md)). The `kondukter` binary embeds its build and serves it at `/`.

The roadmap builds the **skeleton** and each page's **contents**, not the design (P5, "Owner constraint"). Markup is plain, and the only stylesheet is `src/reset.css`. Every route, its role, its data, its actions and its RPCs are in [docs/WEB_PAGES.md](../docs/WEB_PAGES.md); `src/routes.ts` is the same table as code, and a test keeps the two equal.

```sh
make web        # from the repo root: install, typecheck, test, build into web/dist
make build      # embeds web/dist into bin/kondukter
make web-dev    # Vite on :5173, proxying /api to `kondukter serve` on :8080 (KONDUKTER_API_URL)
```

`web/dist` is build output. Git tracks only `web/dist/.gitkeep`, so Go builds work without Node; a binary built that way answers `503` with a note to run `make web`.

## Layout

| Path | What |
|---|---|
| `src/api/` | The transport (`/api`, cookies), one typed client per service (`useApi()`), query keys, the query client (a 401 from any call signs the user out) |
| `src/auth/` | `useMe()` / `useCurrentUser()`, `RoleRoute` guards, role → home and landing rules |
| `src/hooks/` | `useWatchInstance(id)`: `WatchInstance` as a hook, reconnecting, feeding the query cache |
| `src/components/` | Plain `DataTable`, `Form`, `Field`, `Confirm`, and the loading / empty / error states |
| `src/pages/` | `/login`, the area frame (nav + logout), and the placeholder every page starts as |
| `src/routes.ts` | The route map; the router, the navigation and the placeholders read it |
| `embed.go` | `go:embed` of `dist` and the SPA handler used by `kondukter` |

The generated client is imported from `gen/ts` through the `@gen/*` alias. `@bufbuild/protobuf` is pinned to the version `protoc-gen-es` generated for (2.16.0) in both the root package and `web/`, so `gen/ts` and the app share one copy.

## Filling in a page (P5.2 to P5.5)

Write the page component, then set `element` on its entry in `src/routes.ts`; until then the page renders a placeholder naming its RPCs. Keep `docs/WEB_PAGES.md` in step: `routes.test.ts` fails when a route or its RPC list differs between the two.
