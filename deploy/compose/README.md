# deploy/compose

The docker compose stack for local development and the demo control plane on MAC-P ([ADR-0014](../../docs/adr/0014-control-plane-compose-on-presenter-mac.md)).

| Service | Image | Notes |
|---|---|---|
| `postgres` | `postgres:17` | Named volume `pgdata`, healthcheck, published on `127.0.0.1:55432` by default (clear of any host Postgres on 5432). |
| `kondukter` | built from `kondukter.Dockerfile` | Placeholder in the `app` profile until P3 (prints its version and exits). |

Use the Makefile from the repo root; it picks `.env` if present, else [`.env.example`](../../.env.example):

```sh
make db-up      # start Postgres and wait for the healthcheck
make db-psql    # psql inside the container
make db-down    # stop; `make db-down V=1` also drops the volume
```

Item P4.3 extends the stack for the demo (gateway listener, image server, `make demo-up`).
