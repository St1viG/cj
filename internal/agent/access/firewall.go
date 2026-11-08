package access

import (
	"context"
	"log/slog"
	"maps"
	"net/netip"
	"sync"
)

// Firewall keeps the node's nftables table (ADR-0016) in step with the port
// allocations and the guests' addresses. The reconciler calls Sync after
// every pass; the table is re-rendered and applied only when it changed, and
// always on the first successful Sync after start, so a restarted agent
// re-asserts its rules without a gap: the table stays in the kernel while
// the agent is down.
type Firewall struct {
	Net   NFTNetwork
	Ports *Allocator
	// Apply loads a rendered ruleset; nil means ApplyNFT at NFTPath.
	Apply func(ctx context.Context, ruleset string) error
	Log   *slog.Logger

	mu sync.Mutex
	// addrs is the last known guest address per instance; an instance whose
	// Ensure failed this pass keeps its rules until its ports are released.
	addrs   map[string]netip.Addr
	applied string
}

// Sync merges this pass's guest addresses, renders the table and applies it
// if it differs from what was last applied.
func (f *Firewall) Sync(ctx context.Context, guestIPs map[string][]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.addrs == nil {
		f.addrs = map[string]netip.Addr{}
	}
	for id, ips := range guestIPs {
		if a, ok := f.pick(ips); ok {
			f.addrs[id] = a
		}
	}
	mappings, err := f.Ports.Mappings()
	if err != nil {
		return err
	}
	// Forget addresses of instances that hold no ports any more.
	maps.DeleteFunc(f.addrs, func(id string, _ netip.Addr) bool { _, ok := mappings[id]; return !ok })

	fw := Forwards(mappings, f.addrs)
	ruleset, err := RenderNFT(f.Net, fw)
	if err != nil {
		return err
	}
	if ruleset == f.applied {
		return nil
	}
	apply := f.Apply
	if apply == nil {
		apply = func(ctx context.Context, r string) error { return ApplyNFT(ctx, NFTPath, r) }
	}
	if err := apply(ctx, ruleset); err != nil {
		return err
	}
	f.applied = ruleset
	if f.Log != nil {
		f.Log.Info("nftables table applied", "forwards", len(fw))
	}
	return nil
}

// pick returns the first IPv4 address inside the guest subnet.
func (f *Firewall) pick(ips []string) (netip.Addr, bool) {
	for _, s := range ips {
		a, err := netip.ParseAddr(s)
		if err == nil && a.Is4() && f.Net.Subnet.Contains(a) {
			return a, true
		}
	}
	return netip.Addr{}, false
}
