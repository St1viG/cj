# ADR-0004: No Kubernetes in the MVP; Kubernetes appears later only as a driver/adapter

- **Status:** Accepted
- **Date:** 2026-11-08
- **Decisions:** spec §16 direction
- **Spec:** §3.3, §7.6

## Context

Kubernetes was considered both as the fleet substrate (KubeVirt + NVIDIA GPU Operator in vfio mode) and as the control-plane runtime. Spec §7.6: Kubernetes does not federate across WAN (etcd and kubelet heartbeats expect LAN latency; a node that drops goes `NotReady` and taint-based eviction kills its pods after ~5 min, fatal for a VM mid-training), so it would mean one cluster per site with Kondukter still needed above them. The product is a VM, not a pod. KubeVirt needs KVM and cannot run on the macOS demo nodes. The control plane is one Go binary plus Postgres.

## Decision

No Kubernetes anywhere in the MVP stack. Kondukter runs as a single binary (docker compose for the demo, ADR-0014). Nodes run the agent directly under systemd/launchd. The ideas are borrowed instead: desired state + reconcile (ADR-0002), leases via allocations (ADR-0009), labels for scheduling (`gpu.model`, `arch`, `accel.kind`).

Revisit when: a partner already runs K8s/OpenShift with GPU Operator (write a `kubevirt`/`k8s-pod` driver), the product shifts to containers/jobs, or per-site node counts make the agent reimplement kubelet.

## Consequences

- One layer to debug, not three; feasible for a one-to-two-person team.
- The agent must implement its own reconcile loop, inventory and health (P2).
- Partners who only offer quota inside existing virtualization (D-02) need adapters later; unscheduled.
