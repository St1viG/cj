# hack

Developer and demo scripts, plus throwaway spike code.

- Scripts such as `api-smoke.sh` and `kctl-smoke.sh` live directly in this directory.
- `demo.sh` (the demo stack), `demo-happy.sh` (the happy path) and `demo-failure.sh` (the failure drills) source the helpers in `demo-lib.sh`.
- `preflight.sh` checks that the demo is ready (exit 0, or 1 on any FAIL), and `demo-sleep.sh on|off|status` keeps a machine awake. [`docs/DEMO_CHECKLIST.md`](../docs/DEMO_CHECKLIST.md) says when to run them. `preflight.sh` runs on MAC-P and sources `demo-lib.sh`; `demo-sleep.sh` stands alone and runs on every machine. Neither changes a system setting.
- Spike code lives in `hack/s<n>/` (roadmap phase P1). Each spike directory carries its **own `go.mod`**. That keeps spike code and its dependencies out of the main module, so `go build ./...`, `go vet ./...` and `make lint` never see it. Do not add spike modules to a `go.work`.
