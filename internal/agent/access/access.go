// Package access is the agent's port-map access module (ADR-0016): every
// instance gets host ports from the configured range for 22/tcp and its
// declared ports, persisted in the local store so they survive restarts.
// The Linux nftables and macOS Lima backends that make the ports reachable
// build on these mappings (roadmap P2.7).
package access

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/St1viG/cj/internal/agent/driver"
	"github.com/St1viG/cj/internal/agent/store"
)

// SSHPort is always mapped.
const SSHPort = 22

// ErrExhausted means the port range has no free port left.
var ErrExhausted = errors.New("access: no free host port in range")

// PortStore is the part of the agent store the allocator uses.
type PortStore interface {
	Ports() (map[int]store.PortOwner, error)
	UpdatePorts(assign map[int]store.PortOwner, release []int) error
}

// Allocator hands out host ports in [First, Last].
type Allocator struct {
	store       PortStore
	first, last int
	host        string
	mu          sync.Mutex
}

// New returns an allocator over st for ports first..last, advertising
// host as the address of every mapping (the tailnet IP in the demo).
func New(st PortStore, first, last int, host string) *Allocator {
	return &Allocator{store: st, first: first, last: last, host: host}
}

type guestPort struct {
	port  int
	proto string
}

func protoName(p driver.Protocol) string {
	if p == driver.UDP {
		return "udp"
	}
	return "tcp"
}

func protoOf(s string) driver.Protocol {
	if s == "udp" {
		return driver.UDP
	}
	return driver.TCP
}

// Prepare fills spec.PortMappings: 22/tcp plus spec.Ports, keeping the host
// ports the instance already holds and assigning the lowest free ones for
// the rest. Ports the instance holds but no longer declares are released.
func (a *Allocator) Prepare(spec *driver.InstanceSpec) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	owned, err := a.store.Ports()
	if err != nil {
		return err
	}
	want := []guestPort{{SSHPort, "tcp"}}
	for _, p := range spec.Ports {
		g := guestPort{p.GuestPort, protoName(p.Protocol)}
		if !slices.Contains(want, g) {
			want = append(want, g)
		}
	}
	have := map[guestPort]int{}
	for host, o := range owned {
		if o.InstanceID == spec.ID {
			have[guestPort{o.GuestPort, o.Protocol}] = host
		}
	}
	assign := map[int]store.PortOwner{}
	next := a.first
	for _, g := range want {
		if _, ok := have[g]; ok {
			continue
		}
		for ; next <= a.last; next++ {
			if _, used := owned[next]; !used {
				break
			}
		}
		if next > a.last {
			return fmt.Errorf("%w %d-%d for %s %d/%s", ErrExhausted, a.first, a.last, spec.ID, g.port, g.proto)
		}
		o := store.PortOwner{InstanceID: spec.ID, GuestPort: g.port, Protocol: g.proto}
		assign[next], owned[next], have[g] = o, o, next
		next++
	}
	var release []int
	for g, host := range have {
		if !slices.Contains(want, g) {
			release = append(release, host)
			delete(have, g)
		}
	}
	if len(assign) > 0 || len(release) > 0 {
		if err := a.store.UpdatePorts(assign, release); err != nil {
			return err
		}
	}
	spec.PortMappings = spec.PortMappings[:0]
	for _, g := range want {
		spec.PortMappings = append(spec.PortMappings, driver.PortMapping{
			GuestPort: g.port, Protocol: protoOf(g.proto), HostAddress: a.host, HostPort: have[g],
		})
	}
	return nil
}

// Release frees every port of instance id.
func (a *Allocator) Release(id string) error {
	return a.retain(func(owner string) bool { return owner != id })
}

// Retain frees the ports of every instance not in keep, e.g. after a crash
// between a delete and its release.
func (a *Allocator) Retain(keep map[string]bool) error {
	return a.retain(func(owner string) bool { return keep[owner] })
}

func (a *Allocator) retain(keep func(owner string) bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	owned, err := a.store.Ports()
	if err != nil {
		return err
	}
	var release []int
	for _, host := range slices.Sorted(maps.Keys(owned)) {
		if !keep(owned[host].InstanceID) {
			release = append(release, host)
		}
	}
	if len(release) == 0 {
		return nil
	}
	return a.store.UpdatePorts(nil, release)
}

// Mappings returns every allocated mapping by instance, for the backends.
func (a *Allocator) Mappings() (map[string][]driver.PortMapping, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	owned, err := a.store.Ports()
	if err != nil {
		return nil, err
	}
	out := map[string][]driver.PortMapping{}
	for _, host := range slices.Sorted(maps.Keys(owned)) {
		o := owned[host]
		out[o.InstanceID] = append(out[o.InstanceID], driver.PortMapping{
			GuestPort: o.GuestPort, Protocol: protoOf(o.Protocol), HostAddress: a.host, HostPort: host,
		})
	}
	return out, nil
}
