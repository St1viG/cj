// Package scheduler places PENDING instances on nodes (SPEC §6.5): a pure
// filter and score over a snapshot of the fleet, then a commit transaction
// that locks the chosen node, recomputes its free capacity, and writes the
// allocations with concrete accelerator ids.
package scheduler

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/St1viG/cj/internal/controlplane/controller"
	"github.com/St1viG/cj/internal/store/db"
)

// Request is what one instance needs.
type Request struct {
	Region        string
	CapacityClass string
	Arch          string // the image's
	VCPU          int64
	RAMMiB        int64
	DiskGiB       int64
	AccelKind     string
	AccelModel    string
	AccelCount    int
	// CompatibleModels are the accelerator models the image's driver stack
	// supports; empty means the image does not restrict them.
	CompatibleModels []string
	ImageDigest      string
}

// Node is a candidate as the scheduler sees it.
type Node struct {
	ID               uuid.UUID
	PartnerID        uuid.UUID
	Hostname         string
	Status           db.NodeStatus
	Region           string
	CapacityClass    string
	Arch             string
	AcceleratorKinds []string
	MaxInstances     int64 // 0 is unlimited
	ActiveInstances  int64
	FreeVCPU         int64
	FreeRAMMiB       int64
	FreeDiskGiB      int64
	// FreeAccelerators are healthy and held by no active allocation, any
	// model, ordered by local id.
	FreeAccelerators []Accelerator
	CachedDigests    []string
	// PartnerLastPlacedAt is the newest placement on any node of the same
	// partner, for round-robin across partners.
	PartnerLastPlacedAt time.Time
}

// Accelerator is a free device on a candidate node.
type Accelerator struct {
	ID      uuid.UUID
	LocalID string
	Kind    string
	Model   string
}

// Reasons a node is filtered out.
const (
	RejectStatus          = "status"
	RejectRegion          = "region"
	RejectCapacityClass   = "capacity_class"
	RejectArch            = "arch"
	RejectAcceleratorKind = "accelerator_kind"
	RejectImageModel      = "image_incompatible"
	RejectMaxInstances    = "max_instances"
	RejectVCPU            = "vcpu"
	RejectRAM             = "ram"
	RejectDisk            = "disk"
	RejectAccelerators    = "accelerators"
)

// Filter returns "" if n can take r, otherwise the first reason it cannot.
func Filter(r Request, n Node) string {
	switch {
	case !controller.Schedulable(n.Status):
		return RejectStatus
	case n.Region != r.Region:
		return RejectRegion
	case n.CapacityClass != r.CapacityClass:
		return RejectCapacityClass
	case n.Arch == "" || n.Arch != r.Arch:
		return RejectArch
	}
	if r.AccelCount > 0 {
		if !slices.Contains(n.AcceleratorKinds, r.AccelKind) {
			return RejectAcceleratorKind
		}
		if len(r.CompatibleModels) > 0 && !slices.Contains(r.CompatibleModels, r.AccelModel) {
			return RejectImageModel
		}
	}
	switch {
	case n.MaxInstances > 0 && n.ActiveInstances >= n.MaxInstances:
		return RejectMaxInstances
	case n.FreeVCPU < r.VCPU:
		return RejectVCPU
	case n.FreeRAMMiB < r.RAMMiB:
		return RejectRAM
	case n.FreeDiskGiB < r.DiskGiB:
		return RejectDisk
	case len(Matching(r, n)) < r.AccelCount:
		return RejectAccelerators
	}
	return ""
}

// Matching returns n's free accelerators of the requested kind and model.
func Matching(r Request, n Node) []Accelerator {
	if r.AccelCount == 0 {
		return nil
	}
	var out []Accelerator
	for _, a := range n.FreeAccelerators {
		if a.Kind == r.AccelKind && a.Model == r.AccelModel {
			out = append(out, a)
		}
	}
	return out
}

// Pick returns the accelerators to allocate on n: the first AccelCount
// matching ones by local id. Any N on the node will do in the MVP;
// topology-aware choice is Later (ADR-0025).
func Pick(r Request, n Node) []Accelerator {
	m := Matching(r, n)
	if len(m) < r.AccelCount {
		return nil
	}
	return m[:r.AccelCount]
}

// Rank filters nodes and orders the candidates best first:
//  1. best fit: fewest free accelerators left after the placement, so GPU
//     hosts stay free for GPU requests and big hosts for big requests;
//  2. the image is already cached on the node;
//  3. round-robin across partners: the partner that least recently got a
//     placement first;
//  4. node id, so the order is deterministic.
//
// SPEC §6.5's NUMA preference is Later (ADR-0025).
func Rank(r Request, nodes []Node) []Node {
	out := make([]Node, 0, len(nodes))
	for _, n := range nodes {
		if Filter(r, n) == "" {
			out = append(out, n)
		}
	}
	slices.SortStableFunc(out, func(a, b Node) int {
		if c := cmp.Compare(len(a.FreeAccelerators), len(b.FreeAccelerators)); c != 0 {
			return c
		}
		if ac, bc := cached(r, a), cached(r, b); ac != bc {
			if ac {
				return -1
			}
			return 1
		}
		if c := a.PartnerLastPlacedAt.Compare(b.PartnerLastPlacedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID.String(), b.ID.String())
	})
	return out
}

func cached(r Request, n Node) bool {
	return r.ImageDigest != "" && slices.Contains(n.CachedDigests, r.ImageDigest)
}

// Explain summarizes why no node fits, for logs and errors.
func Explain(r Request, nodes []Node) string {
	if len(nodes) == 0 {
		return fmt.Sprintf("no ready node in region %q", r.Region)
	}
	counts := map[string]int{}
	for _, n := range nodes {
		if reason := Filter(r, n); reason != "" {
			counts[reason]++
		}
	}
	parts := make([]string, 0, len(counts))
	for _, k := range slices.Sorted(maps.Keys(counts)) {
		parts = append(parts, fmt.Sprintf("%s ×%d", k, counts[k]))
	}
	return fmt.Sprintf("%d nodes rejected: %s", len(nodes), strings.Join(parts, ", "))
}
