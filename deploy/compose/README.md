# deploy/compose

The docker compose stack for local development and the demo control plane on MAC-P ([ADR-0014](../../docs/adr/0014-control-plane-compose-on-presenter-mac.md)).

| Service | Image | Ports | Notes |
|---|---|---|---|
| `postgres` | `postgres:17` | `127.0.0.1:55432` | Named volume `pgdata` and a healthcheck. Port 55432 stays clear of any host Postgres on 5432. |
| `kondukter` | built from `kondukter.Dockerfile` | `8080` API and web, `8443` agent gateway (mTLS) | Runs `kondukter serve`, which migrates on start. Healthcheck: `kondukter healthcheck`. |
| `images` | `caddy:2.11.7-alpine` | `8081` | Serves `deploy/images/dist` (built images and their `.sha256`) to agents, with range requests. |

The Makefile at the repo root picks `.env` if present, else [`.env.example`](../../.env.example).

## Development

```sh
make db-up      # start Postgres only and wait for the healthcheck
make db-psql    # psql inside the container
make db-down    # stop; `make db-down V=1` also drops the volume
```

## Demo

```sh
make demo-up      # build and start all three, seed, print logins and an enroll command
make demo-reset   # before each run: terminate every instance, undo the drills, empty the instance tables
make demo-down    # stop; `make demo-down V=1` also drops the database
```

[`hack/demo.sh`](../../hack/demo.sh) does the work.

`demo-up`:

- creates `.env` from `.env.example` on first run, with a random `KONDUKTER_MASTER_KEY` and `SEED_PASSWORD`;
- reads this Mac's MagicDNS name and tailnet IP from `tailscale status`:
  - the API URL in enroll commands is `http://<name>:8080`;
  - agents dial `<name>:8443`;
  - the gateway certificate covers the name, the IP, `localhost` and `127.0.0.1`;
  - image URLs in the catalog start with `http://<name>:8081`.

`hack/demo.sh env` prints these addresses.

Docker Desktop publishes the ports on every host interface, the tailnet's included. `KONDUKTER_BIND=127.0.0.1` keeps them local.

The master key seals the CA, which lives in the Postgres volume. Keep `.env` and the volume together: a new key with the old volume fails to start, and a new volume means re-enrolling every node.

`demo-reset` signs in through the API and, in this order:

1. unquarantines every quarantined node (as `operator@demo.local`). This comes first because the gateway ignores a quarantined node's reports, so its instances could never confirm termination;
2. undrains every node that is `DRAINING`, or in `MAINTENANCE` because a drain emptied it (as `partner@demo.local`);
3. force-terminates every live instance, so the agents delete the VMs, and waits up to `RESET_TIMEOUT` (180 s) for all of them to reach `TERMINATED`;
4. truncates `instances`, `allocations`, `instance_events`, `usage_records`, `idempotency_keys` and `audit_log`.

Nodes, their identities, the catalog, accounts and browser sessions stay. It exits non-zero if an instance does not terminate, which leaves the tables untouched, or if a node ends up anything but `READY`.
