# hack

Developer and demo scripts, plus throwaway spike code.

- Scripts such as `api-smoke.sh`, `kctl-smoke.sh` and `preflight.sh` live directly in this directory.
- `demo.sh` (the demo stack) and `demo-failure.sh` (the failure drills) source the helpers in `demo-lib.sh`.
- Spike code lives in `hack/s<n>/` (roadmap phase P1). Each spike directory carries its **own `go.mod`**. That keeps spike code and its dependencies out of the main module, so `go build ./...`, `go vet ./...` and `make lint` never see it. Do not add spike modules to a `go.work`.
