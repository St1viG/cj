# ADR-0013: Fedora is the demo host OS for Linux nodes; production host OS deferred

- **Status:** Accepted
- **Date:** 2026-11-08
- **Decisions:** D-34
- **Spec:** §9.1, §9.8, §17.2

## Context

The spec proposed Ubuntu 24.04 LTS or a RHEL-family 9.x (Rocky/Alma) for production GPU nodes. Both demo Linux machines already run Fedora, and reinstalling them for a demo buys nothing.

## Decision

Fedora (current release on each machine, recorded in the spike findings) is the host OS for the demo Linux nodes. The production host OS decision (Ubuntu 24.04 vs Rocky/Alma 9) is deferred to Later and will be made before the partner pilot.

## Consequences

- SELinux is enforcing: libvirt sVirt labelling applies to qcow2 overlays and the NoCloud ISO; the agent's image cache and overlay directories need correct contexts (spike S1, P1.1).
- firewalld is active with the nftables backend. The agent's own nftables table (ADR-0016) must coexist with it; spike S1 settles the approach (own table + bridge in a trusted zone vs `firewall-cmd` forward-ports).
- vfio-pci binding at boot is done through dracut (`force_drivers`, `rd.driver.pre=vfio-pci`, `vfio-pci.ids=`) and kernel args via `grubby`, not Ubuntu-style `/etc/modprobe.d` + `update-initramfs`.
- Bootstrap scripts in `deploy/nodes/fedora/` (roadmap P4.1) are Fedora-specific; the Ansible playbook for partners is Later.
- Fedora's short support cycle is acceptable for a demo, not for partner hosts.
