# ADR-0021: Demo auth is seeded accounts with cookie sessions and scoped API keys; no sign-up

- **Status:** Accepted
- **Date:** 2026-11-08
- **Decisions:** D-16
- **Spec:** §4.1 (C-1), §6.8, §17.2

## Context

The owner chose a full web app for the demo, which needs login. Options: self-built email + password with sign-up, seeded demo accounts without sign-up, or a self-hosted IdP (Ory Kratos, Keycloak, Zitadel). An IdP adds a container and OIDC work; sign-up adds email verification and password-reset flows nobody will use on stage.

## Decision

- A `kondukter seed` command creates the demo principals: a customer org and user, a partner org with one site and a partner user, an operator user, plus one API key per user. Credentials come from a seed config file.
- Web: email + password login against seeded users, server-side cookie sessions.
- CLI/API: scoped API keys (`kctl login` stores one). Node identity is separate (ADR-0024).
- Roles are derived from org membership (customer, partner, operator); partner views are scoped to the partner's sites.
- Not in the demo: sign-up, email verification, password reset, MFA, external IdP. The production auth choice is Later.

## Consequences

- Removes C-1 "sign up" from the demo scope; everything else in C-1 (API keys, SSH keys) stays.
- Auth code stays small (bcrypt, session table or signed cookie, middleware) and lives in `internal/controlplane/auth`.
- `demo-reset` must re-seed or preserve the seeded principals (P4.3).
- Operator actions are still audited (spec O-5) even without MFA.
