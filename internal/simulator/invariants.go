package simulator

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/St1viG/cj/internal/agent/driver"
	"github.com/St1viG/cj/internal/store"
)

// Check returns every convergence invariant (roadmap P3.10) that does not
// hold right now:
//   - every desired, non-terminal instance is RUNNING, and its VM runs on
//     its node and on no other;
//   - no terminal instance has a VM anywhere, and no VM lacks an instance;
//   - no active allocation belongs to a terminal or unplaced instance;
//   - every placed non-terminal instance holds its resources and exactly
//     its shape's accelerators, and no accelerator is held twice;
//   - every node whose agent runs has observed its desired generation.
func Check(ctx context.Context, s *store.Store, f *Fleet) ([]string, error) {
	var bad []string
	add := func(format string, args ...any) { bad = append(bad, fmt.Sprintf(format, args...)) }

	type inst struct {
		phase    string
		desired  bool
		node     *uuid.UUID
		accels   int
		resource int
		wantAcc  int
	}
	insts := map[uuid.UUID]*inst{}
	rows, err := s.Pool.Query(ctx, `
		SELECT i.id, i.phase::text, i.desired, i.node_id, sh.accel_count,
		  (SELECT count(*) FROM allocations a WHERE a.instance_id = i.id AND a.released_at IS NULL AND a.accelerator_id IS NOT NULL),
		  (SELECT count(*) FROM allocations a WHERE a.instance_id = i.id AND a.released_at IS NULL AND a.accelerator_id IS NULL)
		FROM instances i JOIN offers o ON o.id = i.offer_id JOIN shapes sh ON sh.id = o.shape_id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var (
			id uuid.UUID
			in inst
		)
		if err := rows.Scan(&id, &in.phase, &in.desired, &in.node, &in.wantAcc, &in.accels, &in.resource); err != nil {
			rows.Close()
			return nil, err
		}
		insts[id] = &in
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	terminal := func(p string) bool { return p == "terminated" || p == "failed" || p == "lost" }

	for id, in := range insts {
		switch {
		case terminal(in.phase):
			if in.accels+in.resource > 0 {
				add("terminal instance %s (%s) still holds %d allocations", id, in.phase, in.accels+in.resource)
			}
		case in.phase == "pending":
			if in.accels+in.resource > 0 {
				add("pending instance %s holds allocations", id)
			}
			if in.desired {
				add("instance %s is still PENDING", id)
			}
		default:
			if in.resource != 1 || in.accels != in.wantAcc {
				add("instance %s (%s) holds %d resource and %d accelerator allocations, want 1 and %d", id, in.phase, in.resource, in.accels, in.wantAcc)
			}
			if in.desired && in.phase != "running" {
				add("instance %s is %s, want running", id, strings.ToUpper(in.phase))
			}
		}
	}

	var dup int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM (SELECT accelerator_id FROM allocations
		WHERE released_at IS NULL AND accelerator_id IS NOT NULL GROUP BY 1 HAVING count(*) > 1) d`).Scan(&dup); err != nil {
		return nil, err
	}
	if dup > 0 {
		add("%d accelerators are held by two allocations", dup)
	}

	// VMs on the simulated hosts.
	where := map[uuid.UUID][]uuid.UUID{}
	for _, a := range f.Agents {
		vms, err := a.VMs(ctx)
		if err != nil {
			return nil, fmt.Errorf("%s: list VMs: %w", a.Spec.Hostname, err)
		}
		for _, vm := range vms {
			id, err := uuid.Parse(vm.ID)
			if err != nil {
				add("%s runs %q, which is not an instance id", a.Spec.Hostname, vm.ID)
				continue
			}
			where[id] = append(where[id], a.NodeID)
			in := insts[id]
			switch {
			case in == nil:
				add("%s runs VM %s with no instance", a.Spec.Hostname, id)
			case terminal(in.phase):
				add("%s still runs VM %s of a %s instance", a.Spec.Hostname, id, in.phase)
			case in.phase == "running" && vm.Phase != driver.PhaseRunning:
				add("%s has VM %s in phase %s for a RUNNING instance", a.Spec.Hostname, id, vm.Phase)
			}
		}
	}
	for id, in := range insts {
		if in.phase != "running" {
			continue
		}
		nodes := where[id]
		switch {
		case len(nodes) == 0:
			add("RUNNING instance %s has no VM", id)
		case len(nodes) > 1:
			add("instance %s runs on %d nodes", id, len(nodes))
		case in.node == nil || nodes[0] != *in.node:
			add("instance %s runs on %s, not its node %v", id, nodes[0], in.node)
		}
	}

	// Generations of nodes whose agent runs.
	byNode := f.ByNode()
	rows, err = s.Pool.Query(ctx, `SELECT id, hostname, desired_generation, observed_generation FROM nodes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id                uuid.UUID
			host              string
			desired, observed int64
		)
		if err := rows.Scan(&id, &host, &desired, &observed); err != nil {
			return nil, err
		}
		if a := byNode[id]; a != nil && a.Running() && desired != observed {
			add("node %s observed generation %d, desired %d", host, observed, desired)
		}
	}
	slices.Sort(bad)
	return bad, rows.Err()
}

// WaitConverged polls Check until it passes or within elapses, and returns
// how long it took and the last violations.
func WaitConverged(ctx context.Context, s *store.Store, f *Fleet, within time.Duration) (time.Duration, []string, error) {
	start := time.Now()
	for {
		bad, err := Check(ctx, s, f)
		if err != nil {
			return time.Since(start), nil, err
		}
		if len(bad) == 0 || time.Since(start) > within {
			return time.Since(start), bad, nil
		}
		select {
		case <-ctx.Done():
			return time.Since(start), bad, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}
