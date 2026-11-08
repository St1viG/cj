package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/St1viG/cj/internal/controlplane/controller"
	"github.com/St1viG/cj/internal/store"
	"github.com/St1viG/cj/internal/store/db"
)

// Defaults.
const (
	DefaultInterval = time.Second
	// DefaultPendingTimeout is how long an instance may wait for capacity
	// before it fails with no_capacity (SPEC §6.3).
	DefaultPendingTimeout = 60 * time.Second
	// ReasonNoCapacity is the phase reason of an instance that timed out.
	ReasonNoCapacity = "no_capacity"

	passBatch   = 100
	maxRetries  = 3
	leaseIndex  = "one_active_lease_per_accelerator"
	defaultNode = "node"
)

// Errors returned by Place.
var (
	// ErrNoCapacity means no node can take the instance right now.
	ErrNoCapacity = errors.New("scheduler: no capacity")
	// ErrNotPending means the instance was placed, terminated or failed by
	// someone else; there is nothing to do.
	ErrNotPending = errors.New("scheduler: instance is not pending")
)

// errNoFit is a capacity race: the candidate no longer fits once locked.
var errNoFit = errors.New("scheduler: candidate no longer fits")

// Hooks are called after a commit. Either may be nil.
type Hooks struct {
	// NodeChanged is told the node's desired state changed (SPEC §6.5
	// commit step 5); the gateway pushes a new snapshot.
	NodeChanged func(nodeID uuid.UUID)
	// InstanceChanged is told an instance changed phase.
	InstanceChanged func(instanceID uuid.UUID)
}

// Scheduler places PENDING instances.
type Scheduler struct {
	Store *store.Store
	Log   *slog.Logger
	Hooks Hooks
	// Now is the clock for the pending timeout; time.Now if nil.
	Now func() time.Time
	// Interval between passes; DefaultInterval if zero.
	Interval time.Duration
	// PendingTimeout; DefaultPendingTimeout if zero.
	PendingTimeout time.Duration

	wake chan struct{}
}

// New returns a scheduler with default timing.
func New(s *store.Store, log *slog.Logger, hooks Hooks) *Scheduler {
	return &Scheduler{Store: s, Log: log, Hooks: hooks, wake: make(chan struct{}, 1)}
}

// Placement is a committed placement.
type Placement struct {
	InstanceID uuid.UUID
	NodeID     uuid.UUID
	Hostname   string
	// Accelerators are the local ids allocated to the instance.
	Accelerators []string
	Generation   int64
}

// Trigger asks for a pass soon, e.g. after an instance was created or
// capacity was freed. It never blocks.
func (s *Scheduler) Trigger() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Run makes a pass every Interval, and on Trigger, until ctx is canceled.
// Pass errors are logged, not returned: the next pass retries.
func (s *Scheduler) Run(ctx context.Context) error {
	every := s.Interval
	if every <= 0 {
		every = DefaultInterval
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if _, err := s.Pass(ctx); err != nil && ctx.Err() == nil {
			s.Log.Error("scheduler pass failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		case <-s.wake:
		}
	}
}

// Report counts what a pass did.
type Report struct {
	Placed  int
	Failed  int // timed out with no_capacity
	Waiting int // still PENDING
}

// Pass tries to place every PENDING instance, oldest first, and fails those
// that waited longer than PendingTimeout without finding capacity.
func (s *Scheduler) Pass(ctx context.Context) (Report, error) {
	var rep Report
	pending, err := s.Store.ListPendingInstances(ctx, passBatch)
	if err != nil {
		return rep, err
	}
	var errs []error
	pc := &passCache{fleet: map[string][]Node{}, offers: map[uuid.UUID]db.GetOfferRow{}, images: map[uuid.UUID]db.Image{}}
	for _, inst := range pending {
		_, err := s.place(ctx, inst, pc)
		switch {
		case err == nil:
			rep.Placed++
		case errors.Is(err, ErrNotPending):
		case errors.Is(err, ErrNoCapacity):
			if s.now().Sub(inst.PhaseChangedAt) < s.pendingTimeout() {
				rep.Waiting++
				continue
			}
			failed, ferr := s.failNoCapacity(ctx, inst.ID)
			if ferr != nil {
				errs = append(errs, ferr)
				continue
			}
			if failed {
				rep.Failed++
				s.Log.Info("instance failed: no capacity", "instance", inst.ID, "detail", err)
			}
		default:
			errs = append(errs, fmt.Errorf("place %s: %w", inst.ID, err))
		}
	}
	return rep, errors.Join(errs...)
}

// Place tries to place one PENDING instance: candidates best first, the next
// one on a capacity race. It returns ErrNoCapacity (wrapped with the reasons)
// when no node fits and ErrNotPending when the instance is no longer waiting.
func (s *Scheduler) Place(ctx context.Context, instanceID uuid.UUID) (Placement, error) {
	inst, err := s.Store.GetInstance(ctx, instanceID)
	if err != nil {
		return Placement{}, store.NotFound(err)
	}
	return s.place(ctx, inst, nil)
}

// passCache shares reads across one pass: the fleet snapshot per region
// (updated in memory after each placement; every commit re-checks its
// node under the row lock, so a stale entry only costs a retry) and the
// offers and images of the pending instances.
type passCache struct {
	fleet  map[string][]Node
	offers map[uuid.UUID]db.GetOfferRow
	images map[uuid.UUID]db.Image
}

func (s *Scheduler) place(ctx context.Context, inst db.Instance, pc *passCache) (Placement, error) {
	if inst.Phase != db.InstancePhasePending || !inst.Desired {
		return Placement{}, ErrNotPending
	}
	req, err := s.request(ctx, inst, pc)
	if err != nil {
		return Placement{}, err
	}
	var nodes []Node
	if pc != nil {
		nodes = pc.fleet[req.Region]
	}
	if nodes == nil {
		if nodes, err = loadNodes(ctx, s.Store.Queries, req.Region, nil); err != nil {
			return Placement{}, err
		}
		if pc != nil {
			if nodes == nil {
				nodes = []Node{}
			}
			pc.fleet[req.Region] = nodes
		}
	}
	ranked := Rank(req, nodes)
	for _, cand := range ranked {
		p, err := s.commitWithRetry(ctx, inst.ID, req, cand.ID)
		switch {
		case err == nil:
			s.Log.Info("instance placed", "instance", inst.ID, "node", p.NodeID, "hostname", p.Hostname,
				"accelerators", p.Accelerators, "generation", p.Generation)
			if pc != nil {
				pc.applied(req, p)
			}
			if s.Hooks.NodeChanged != nil {
				s.Hooks.NodeChanged(p.NodeID)
			}
			if s.Hooks.InstanceChanged != nil {
				s.Hooks.InstanceChanged(p.InstanceID)
			}
			return p, nil
		case errors.Is(err, errNoFit), store.IsUniqueViolation(err, leaseIndex):
			continue // raced with another placement; try the next candidate
		default:
			return Placement{}, err
		}
	}
	if len(ranked) > 0 {
		return Placement{}, fmt.Errorf("%w: every candidate was taken concurrently", ErrNoCapacity)
	}
	return Placement{}, fmt.Errorf("%w: %s", ErrNoCapacity, Explain(req, nodes))
}

// applied updates the pass's fleet snapshot after a placement.
func (pc *passCache) applied(req Request, p Placement) {
	nodes := pc.fleet[req.Region]
	var partner uuid.UUID
	for i := range nodes {
		n := &nodes[i]
		if n.ID != p.NodeID {
			continue
		}
		partner = n.PartnerID
		n.FreeVCPU -= req.VCPU
		n.FreeRAMMiB -= req.RAMMiB
		n.FreeDiskGiB -= req.DiskGiB
		n.ActiveInstances++
		n.FreeAccelerators = slices.DeleteFunc(slices.Clone(n.FreeAccelerators), func(a Accelerator) bool {
			return slices.Contains(p.Accelerators, a.LocalID)
		})
	}
	now := time.Now()
	for i := range nodes {
		if nodes[i].PartnerID == partner {
			nodes[i].PartnerLastPlacedAt = now
		}
	}
}

// request builds what inst needs from its spec, offer, shape and image.
func (s *Scheduler) request(ctx context.Context, inst db.Instance, pc *passCache) (Request, error) {
	spec, err := store.ParseSpec(inst.Spec)
	if err != nil {
		return Request{}, err
	}
	offer, ok := db.GetOfferRow{}, false
	if pc != nil {
		offer, ok = pc.offers[inst.OfferID]
	}
	if !ok {
		if offer, err = s.Store.GetOffer(ctx, inst.OfferID); err != nil {
			return Request{}, fmt.Errorf("scheduler: offer %s: %w", inst.OfferID, store.NotFound(err))
		}
		if pc != nil {
			pc.offers[inst.OfferID] = offer
		}
	}
	img, ok := db.Image{}, false
	if pc != nil {
		img, ok = pc.images[inst.ImageID]
	}
	if !ok {
		if img, err = s.Store.GetImage(ctx, inst.ImageID); err != nil {
			return Request{}, fmt.Errorf("scheduler: image %s: %w", inst.ImageID, store.NotFound(err))
		}
		if pc != nil {
			pc.images[inst.ImageID] = img
		}
	}
	arch := spec.Image.Arch
	if arch == "" {
		arch = img.Arch
	}
	digest := spec.Image.Digest
	if digest == "" {
		digest = img.Digest
	}
	return Request{
		Region: offer.Offer.Region, CapacityClass: offer.Offer.CapacityClass, Arch: arch,
		VCPU: int64(spec.VCPU), RAMMiB: spec.RAMMiB, DiskGiB: spec.DiskGiB,
		AccelKind: offer.Shape.AccelKind, AccelModel: offer.Shape.AccelModel, AccelCount: int(offer.Shape.AccelCount),
		CompatibleModels: img.CompatibleModels, ImageDigest: digest,
	}, nil
}

func (s *Scheduler) commitWithRetry(ctx context.Context, instanceID uuid.UUID, req Request, nodeID uuid.UUID) (Placement, error) {
	for attempt := 1; ; attempt++ {
		p, err := s.commit(ctx, instanceID, req, nodeID)
		if err == nil || !store.IsRetryable(err) || attempt == maxRetries {
			return p, err
		}
	}
}

// commit is SPEC §6.5's commit transaction for one candidate.
func (s *Scheduler) commit(ctx context.Context, instanceID uuid.UUID, req Request, nodeID uuid.UUID) (Placement, error) {
	var p Placement
	err := s.Store.WithTx(ctx, func(tx store.Tx) error {
		inst, err := tx.GetInstanceForUpdate(ctx, instanceID)
		if err != nil {
			return store.NotFound(err)
		}
		if _, ok := controller.Next(inst.Phase, controller.EvPlaced); !ok || !inst.Desired {
			return ErrNotPending
		}
		// 1. Lock the node: placements on one node are serialized.
		if _, err := tx.GetNodeForUpdate(ctx, nodeID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errNoFit
			}
			return err
		}
		// 2. Recompute its free capacity under the lock.
		nodes, err := loadNodes(ctx, tx.Queries, req.Region, &nodeID)
		if err != nil {
			return err
		}
		if len(nodes) != 1 || Filter(req, nodes[0]) != "" {
			return errNoFit
		}
		n := nodes[0]
		accels := Pick(req, n)

		// 3. Allocations with the concrete accelerators.
		if _, err := tx.CreateAllocation(ctx, db.CreateAllocationParams{
			InstanceID: inst.ID, NodeID: n.ID,
			Vcpu: int32(req.VCPU), RamMib: req.RAMMiB, DiskGib: req.DiskGiB, //nolint:gosec // bounded by the node's free vCPU (int32)
		}); err != nil {
			return err
		}
		ids := make([]string, 0, len(accels))
		for _, a := range accels {
			if _, err := tx.CreateAllocation(ctx, db.CreateAllocationParams{
				InstanceID: inst.ID, NodeID: n.ID, AcceleratorID: &a.ID,
			}); err != nil {
				return err
			}
			ids = append(ids, a.LocalID)
		}

		// 4. node_id, SCHEDULED, and a new desired generation.
		gen, err := tx.BumpNodeDesiredGeneration(ctx, n.ID)
		if err != nil {
			return err
		}
		if _, err := tx.PlaceInstance(ctx, db.PlaceInstanceParams{ID: inst.ID, NodeID: &n.ID, PlacementGeneration: gen}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotPending
			}
			return err
		}
		host := n.Hostname
		if host == "" {
			host = defaultNode + " " + n.ID.String()
		}
		from := inst.Phase
		if err := tx.InsertInstanceEvent(ctx, db.InsertInstanceEventParams{
			InstanceID: inst.ID, FromPhase: &from, ToPhase: db.InstancePhaseScheduled,
			Reason: "placed on " + host,
		}); err != nil {
			return err
		}
		p = Placement{InstanceID: inst.ID, NodeID: n.ID, Hostname: n.Hostname, Accelerators: ids, Generation: gen}
		return nil
	})
	return p, err
}

// failNoCapacity moves a still-PENDING instance to FAILED(no_capacity). It
// reports false if the instance had already left PENDING.
func (s *Scheduler) failNoCapacity(ctx context.Context, instanceID uuid.UUID) (bool, error) {
	failed := false
	err := s.Store.WithTx(ctx, func(tx store.Tx) error {
		inst, err := tx.GetInstanceForUpdate(ctx, instanceID)
		if err != nil {
			return store.NotFound(err)
		}
		if inst.Phase != db.InstancePhasePending {
			return nil
		}
		if _, _, err := controller.Transition(ctx, tx, inst, controller.EvScheduleTimeout, ReasonNoCapacity); err != nil {
			return err
		}
		failed = true
		return nil
	})
	if err == nil && failed && s.Hooks.InstanceChanged != nil {
		s.Hooks.InstanceChanged(instanceID)
	}
	return failed, err
}

// loadNodes reads candidates in region (just nodeID if set) with their free
// accelerators.
func loadNodes(ctx context.Context, q *db.Queries, region string, nodeID *uuid.UUID) ([]Node, error) {
	rows, err := q.ListPlacementNodes(ctx, db.ListPlacementNodesParams{Region: region, NodeID: nodeID})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	ids := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	free, err := q.ListFreeAccelerators(ctx, ids)
	if err != nil {
		return nil, err
	}
	byNode := map[uuid.UUID][]Accelerator{}
	for _, a := range free {
		byNode[a.NodeID] = append(byNode[a.NodeID], Accelerator{ID: a.ID, LocalID: a.LocalID, Kind: a.Kind, Model: a.Model})
	}
	nodes := make([]Node, len(rows))
	for i, r := range rows {
		nodes[i] = Node{
			ID: r.ID, PartnerID: r.PartnerID, Hostname: r.Hostname, Status: db.NodeStatusReady,
			Region: r.Region, CapacityClass: r.CapacityClass, Arch: r.Arch, AcceleratorKinds: r.AcceleratorKinds,
			MaxInstances: int64(r.MaxInstances), ActiveInstances: r.ActiveInstances,
			FreeVCPU: r.FreeVcpu, FreeRAMMiB: r.FreeRamMib, FreeDiskGiB: r.FreeDiskGib,
			FreeAccelerators: byNode[r.ID], CachedDigests: cachedDigests(r.CachedImages),
			PartnerLastPlacedAt: r.PartnerLastPlacedAt,
		}
	}
	return nodes, nil
}

func cachedDigests(raw []byte) []string {
	var imgs []struct {
		Digest string `json:"digest"`
	}
	if json.Unmarshal(raw, &imgs) != nil {
		return nil
	}
	out := make([]string, 0, len(imgs))
	for _, i := range imgs {
		if i.Digest != "" {
			out = append(out, i.Digest)
		}
	}
	return out
}

func (s *Scheduler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Scheduler) pendingTimeout() time.Duration {
	if s.PendingTimeout > 0 {
		return s.PendingTimeout
	}
	return DefaultPendingTimeout
}
