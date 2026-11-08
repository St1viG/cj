# ADR-0022: Web app is React + Vite + TypeScript with a connect-es client, embedded in the Go binary

- **Status:** Accepted
- **Date:** 2026-11-08
- **Decisions:** D-17
- **Spec:** §5.2, §15, §19 (`web/`)

## Context

Frontend options were React + Vite, Next.js and SvelteKit. The client is generated from the proto contract (ADR-0006), so the framework choice is about tooling and the owner's preference. Server-side rendering is not needed. The owner also stated that they will design the UI themselves.

## Decision

- `web/` is a Vite + React + TypeScript single-page app using the `connect-es` client generated from `proto/kondukter/api/v1`, TanStack Query for data fetching and cache, and a client-side router with role-based route guards.
- `make web` builds to `web/dist`, which the `kondukter` binary serves via `go:embed`, so the compose stack ships one artifact.
- **Scope constraint:** the roadmap (P5) delivers the skeleton (routing, auth, data hooks, generated client) and the **contents** of each page (data shown, actions, backing RPCs, loading/error/empty states) with unstyled markup. No component library, theming or layout work; `docs/WEB_PAGES.md` is the hand-off to the owner's UI design.

## Consequences

- Types flow end to end from proto to browser; `WatchInstance` server-streaming works in the browser without a proxy.
- Dev loop: Vite dev server proxies to the Go API; production serves the embedded build.
- The owner can restyle freely because pages are plain markup bound to typed hooks.
- Roadmap: P5.1 scaffold, P5.2–P5.5 pages.
