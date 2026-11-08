// Package reconcile converges the host to the last DesiredState (SPEC §7.8,
// ADR-0002).
//
// A pass runs on a new snapshot, every 30 s and on driver events. It Ensures
// every desired instance and, only when the snapshot is fresh (received on
// the live stream), Deletes owned instances the snapshot no longer lists.
// The applied generation is recorded only after a fresh pass attempted
// every instance. With no snapshot ever received, or with a stale one, the
// agent is fail-static: it keeps everything running and deletes nothing.
package reconcile

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/St1viG/cj/gen/go/kondukter/agent/v1"
	"github.com/St1viG/cj/internal/agent/driver"
)

// Interval is the periodic reconcile tick.
const Interval = 30 * time.Second

// State is the part of the agent store the reconciler uses.
type State interface {
	Snapshot() (*agentv1.DesiredState, error)
	SetSnapshot(*agentv1.DesiredState) error
	AppliedGeneration() (uint64, error)
	SetAppliedGeneration(uint64) error
}

// Config wires a Reconciler.
type Config struct {
	Driver driver.Driver
	State  State
	// Report sends an InstanceStatus (through the outbox).
	Report func(*agentv1.InstanceStatus) error
	// Fresh reports whether the stored snapshot came over the live stream;
	// deletions happen only then.
	Fresh func() bool
	// Prepare, if set, completes a spec before Ensure (the access module
	// fills in port mappings).
	Prepare func(*driver.InstanceSpec) error
	// Deleted, if set, is told an instance is gone (the access module frees
	// its ports).
	Deleted func(id string)
	// Retain, if set, is given every instance that is desired or on the host
	// after a fresh pass, to drop leftovers (e.g. ports) of anything else.
	Retain   func(keep map[string]bool)
	Log      *slog.Logger
	Interval time.Duration
}

// Reconciler runs reconcile passes.
type Reconciler struct {
	cfg     Config
	trigger chan struct{}

	mu sync.Mutex // serializes passes
	// reported is the last status sent per instance, to avoid repeats.
	reported map[string]*agentv1.InstanceStatus
	// known are instances the agent has seen in a snapshot or on the host;
	// one that disappears from both is reported deleted once.
	known map[string]bool
}

// New returns a reconciler.
func New(cfg Config) *Reconciler {
	if cfg.Interval == 0 {
		cfg.Interval = Interval
	}
	if cfg.Fresh == nil {
		cfg.Fresh = func() bool { return false }
	}
	return &Reconciler{
		cfg:      cfg,
		trigger:  make(chan struct{}, 1),
		reported: map[string]*agentv1.InstanceStatus{},
		known:    map[string]bool{},
	}
}

// Offer stores a snapshot received from the gateway and triggers a pass.
// Snapshots older than the stored one or the applied generation are
// ignored; a duplicate of an already-applied snapshot is a no-op.
func (r *Reconciler) Offer(ds *agentv1.DesiredState) error {
	applied, err := r.cfg.State.AppliedGeneration()
	if err != nil {
		return err
	}
	cur, err := r.cfg.State.Snapshot()
	if err != nil {
		return err
	}
	gen := ds.GetGeneration()
	switch {
	case gen < applied || (cur != nil && gen < cur.GetGeneration()):
		r.cfg.Log.Warn("ignoring out-of-order snapshot", "generation", gen, "applied", applied, "stored", cur.GetGeneration())
		return nil
	case cur != nil && gen == cur.GetGeneration():
		if !proto.Equal(cur, ds) {
			r.cfg.Log.Warn("snapshot changed without a new generation; keeping the stored one", "generation", gen)
		}
		if gen == applied {
			return nil // duplicate of what is applied
		}
	default:
		if err := r.cfg.State.SetSnapshot(ds); err != nil {
			return err
		}
	}
	r.Trigger()
	return nil
}

// Trigger requests a pass without blocking.
func (r *Reconciler) Trigger() {
	select {
	case r.trigger <- struct{}{}:
	default:
	}
}

// Run executes passes on triggers, ticks and driver events until ctx is
// canceled.
func (r *Reconciler) Run(ctx context.Context) error {
	var events <-chan driver.Event
	if es, ok := r.cfg.Driver.(driver.EventSource); ok {
		events = es.Events()
	}
	tick := time.NewTicker(r.cfg.Interval)
	defer tick.Stop()
	r.Trigger() // converge on start, fail-static until fresh
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-r.trigger:
		case <-tick.C:
		case <-events:
		}
		if err := r.Pass(ctx); err != nil && ctx.Err() == nil {
			r.cfg.Log.Error("reconcile pass", "err", err)
		}
	}
}

// Pass runs one reconcile pass.
func (r *Reconciler) Pass(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	ds, err := r.cfg.State.Snapshot()
	if err != nil {
		return err
	}
	if ds == nil {
		return nil // never received a snapshot: fail-static
	}
	gen := ds.GetGeneration()
	fresh := r.cfg.Fresh()

	observed, err := r.cfg.Driver.List(ctx)
	if err != nil {
		return err
	}
	onHost := map[string]bool{}
	for _, o := range observed {
		onHost[o.ID] = true
		r.known[o.ID] = true
	}

	desired := map[string]bool{}
	attempted := true
	for _, spec := range ds.GetInstances() {
		if ctx.Err() != nil {
			attempted = false
			break
		}
		desired[spec.GetId()] = true
		r.known[spec.GetId()] = true
		r.ensure(ctx, gen, spec)
	}

	if fresh {
		for _, o := range observed {
			if desired[o.ID] {
				continue
			}
			if ctx.Err() != nil {
				attempted = false
				break
			}
			if err := r.cfg.Driver.Delete(ctx, o.ID); err != nil {
				attempted = false
				r.report(&agentv1.InstanceStatus{Id: o.ID, Phase: agentv1.Phase_PHASE_DELETING, Reason: err.Error(), AppliedGeneration: gen})
				r.cfg.Log.Error("delete", "instance", o.ID, "err", err)
				continue
			}
			r.cfg.Log.Info("deleted", "instance", o.ID, "generation", gen)
			r.deleted(o.ID, gen)
		}
		// Instances that left the snapshot without ever reaching the host
		// (their create failed) still need a deletion confirmation.
		for id := range r.known {
			if !desired[id] && !onHost[id] {
				r.deleted(id, gen)
			}
		}
	}

	if fresh && attempted {
		if r.cfg.Retain != nil {
			keep := maps.Clone(desired)
			for _, o := range observed {
				keep[o.ID] = true
			}
			r.cfg.Retain(keep)
		}
		return r.cfg.State.SetAppliedGeneration(gen)
	}
	return nil
}

func (r *Reconciler) ensure(ctx context.Context, gen uint64, p *agentv1.InstanceSpec) {
	spec := FromProto(p, gen)
	st := &agentv1.InstanceStatus{Id: spec.ID, AppliedGeneration: gen}
	var err error
	if r.cfg.Prepare != nil {
		err = r.cfg.Prepare(&spec)
	}
	var o driver.Observed
	if err == nil {
		o, err = r.cfg.Driver.Ensure(ctx, spec)
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		st.Phase, st.Reason = agentv1.Phase_PHASE_FAILED, err.Error()
		r.cfg.Log.Warn("ensure failed; retrying next pass", "instance", spec.ID, "err", err)
	} else {
		st.Phase, st.Reason, st.GuestIps = PhaseProto(o.Phase), o.Reason, o.GuestIPs
		for _, m := range spec.PortMappings {
			st.PortMappings = append(st.PortMappings, &agentv1.PortMapping{
				GuestPort: clampPort(m.GuestPort), Protocol: protocolProto(m.Protocol),
				HostAddress: m.HostAddress, HostPort: clampPort(m.HostPort),
			})
		}
	}
	r.report(st)
}

func (r *Reconciler) deleted(id string, gen uint64) {
	r.report(&agentv1.InstanceStatus{Id: id, Phase: agentv1.Phase_PHASE_DELETED, AppliedGeneration: gen})
	if r.cfg.Deleted != nil {
		r.cfg.Deleted(id)
	}
	delete(r.known, id)
	delete(r.reported, id)
}

// report sends st unless it repeats the last status for that instance.
func (r *Reconciler) report(st *agentv1.InstanceStatus) {
	if last := r.reported[st.Id]; last != nil && sameStatus(last, st) {
		return
	}
	st.At = timestamppb.Now()
	if err := r.cfg.Report(st); err != nil {
		r.cfg.Log.Error("report status", "instance", st.Id, "err", err)
		return
	}
	r.reported[st.Id] = st
}

func sameStatus(a, b *agentv1.InstanceStatus) bool {
	return a.Phase == b.Phase && a.Reason == b.Reason && a.AppliedGeneration == b.AppliedGeneration &&
		slices.Equal(a.GuestIps, b.GuestIps) && len(a.PortMappings) == len(b.PortMappings) &&
		slices.EqualFunc(a.PortMappings, b.PortMappings, func(x, y *agentv1.PortMapping) bool { return proto.Equal(x, y) })
}

// FromProto converts a snapshot entry into a driver spec.
func FromProto(p *agentv1.InstanceSpec, gen uint64) driver.InstanceSpec {
	s := driver.InstanceSpec{
		ID:             p.GetId(),
		Generation:     gen,
		VCPU:           int(p.GetVcpu()),
		RAMMiB:         int64(min(p.GetRamMib(), 1<<62)),
		DiskGiB:        int64(min(p.GetDiskGib(), 1<<62)),
		AcceleratorIDs: slices.Clone(p.GetAcceleratorIds()),
		SSHKeys:        slices.Clone(p.GetSshPublicKeys()),
		UserData:       slices.Clone(p.GetUserData()),
		Hostname:       p.GetHostname(),
	}
	if img := p.GetImage(); img != nil {
		s.Image = driver.ImageRef{
			Name: img.GetName(), Version: img.GetVersion(), Arch: img.GetArch(), Digest: img.GetDigest(),
			URL: img.GetUrl(), SizeBytes: int64(min(img.GetSizeBytes(), 1<<62)), Format: img.GetFormat(),
		}
	}
	for _, ps := range p.GetPorts() {
		proto := driver.TCP
		if ps.GetProtocol() == agentv1.Protocol_PROTOCOL_UDP {
			proto = driver.UDP
		}
		s.Ports = append(s.Ports, driver.PortSpec{GuestPort: int(ps.GetGuestPort()), Protocol: proto})
	}
	return s
}

// PhaseProto maps a driver phase to the protocol enum.
func PhaseProto(p driver.Phase) agentv1.Phase {
	switch p {
	case driver.PhaseProvisioning:
		return agentv1.Phase_PHASE_PROVISIONING
	case driver.PhaseRunning:
		return agentv1.Phase_PHASE_RUNNING
	case driver.PhaseFailed:
		return agentv1.Phase_PHASE_FAILED
	case driver.PhaseDeleting:
		return agentv1.Phase_PHASE_DELETING
	}
	return agentv1.Phase_PHASE_UNSPECIFIED
}

func protocolProto(p driver.Protocol) agentv1.Protocol {
	if p == driver.UDP {
		return agentv1.Protocol_PROTOCOL_UDP
	}
	return agentv1.Protocol_PROTOCOL_TCP
}

func clampPort(p int) uint32 {
	if p < 0 || p > 65535 {
		return 0
	}
	return uint32(p)
}
