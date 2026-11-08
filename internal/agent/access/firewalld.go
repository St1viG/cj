package access

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"slices"
	"strings"
)

// EgressPolicy is the firewalld policy that lets guest traffic through
// firewalld's forward chain (spike S1, finding 3).
const EgressPolicy = "kdk-egress"

// Cmd runs a command and returns its combined output.
type Cmd func(ctx context.Context, name string, args ...string) ([]byte, error)

// ExecCmd runs commands with os/exec.
func ExecCmd(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput() //nolint:gosec // G204: argv, not a shell; the agent chooses name and args
	if err != nil {
		return out, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// EnsureFirewalld arranges for firewalld to leave the guest bridge alone, so
// our own nftables table (ADR-0016) decides what guests may do:
//
//   - the bridge is bound to the "trusted" zone, which accepts the guests'
//     DHCP and DNS requests to dnsmasq (an unbound bridge falls into the
//     default zone, which drops them) and forwards traffic from the tailnet
//     (also "trusted") to the guests;
//   - the EgressPolicy policy accepts traffic from "trusted" to any zone, or
//     firewalld's forward chain drops the guests' outbound traffic however
//     our own table's chains vote, because an accept in one base chain does
//     not stop the next.
//
// Our table still drops guest traffic to private ranges and to the node.
// The settings are permanent, so they survive reboots and firewalld
// reloads. A node without a running firewalld needs none of this and is
// left as it is (changed is false).
func EnsureFirewalld(ctx context.Context, run Cmd, bridge string, log *slog.Logger) (changed bool, err error) {
	if run == nil {
		run = ExecCmd
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	fw := func(args ...string) (string, error) {
		out, err := run(ctx, "firewall-cmd", args...)
		return strings.TrimSpace(string(out)), err
	}
	if _, err := fw("--state"); err != nil {
		var nf *exec.Error
		if errors.As(err, &nf) {
			log.Info("firewalld is not installed; leaving the firewall to the nftables table")
		} else {
			log.Info("firewalld is not running; leaving the firewall to the nftables table")
		}
		return false, nil
	}

	ifaces, err := fw("--permanent", "--zone=trusted", "--list-interfaces")
	if err != nil {
		return false, err
	}
	if !slices.Contains(strings.Fields(ifaces), bridge) {
		if _, err := fw("--permanent", "--zone=trusted", "--add-interface="+bridge); err != nil {
			return false, err
		}
		changed = true
	}

	policies, err := fw("--permanent", "--get-policies")
	if err != nil {
		return false, err
	}
	if !slices.Contains(strings.Fields(policies), EgressPolicy) {
		if _, err := fw("--permanent", "--new-policy="+EgressPolicy); err != nil {
			return false, err
		}
		changed = true
	}
	info, err := fw("--permanent", "--info-policy="+EgressPolicy)
	if err != nil {
		return false, err
	}
	for _, s := range []struct{ want, set string }{
		{"target: ACCEPT", "--set-target=ACCEPT"},
		{"ingress-zones: trusted", "--add-ingress-zone=trusted"},
		{"egress-zones: ANY", "--add-egress-zone=ANY"},
	} {
		if !strings.Contains(info, s.want) {
			if _, err := fw("--permanent", "--policy="+EgressPolicy, s.set); err != nil {
				return false, err
			}
			changed = true
		}
	}
	if changed {
		if _, err := fw("--reload"); err != nil {
			return false, err
		}
		log.Info("firewalld arranged for the guest bridge", "bridge", bridge, "zone", "trusted", "policy", EgressPolicy)
	}
	return changed, nil
}
