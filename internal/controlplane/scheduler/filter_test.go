package scheduler_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/St1viG/cj/internal/controlplane/scheduler"
	"github.com/St1viG/cj/internal/store/db"
)

const (
	gtx = "GeForce GTX 1060 6GB"
	rtx = "GeForce RTX 3090"
)

func gpuRequest() scheduler.Request {
	return scheduler.Request{
		Region: "demo", CapacityClass: "dedicated", Arch: "amd64",
		VCPU: 4, RAMMiB: 8192, DiskGiB: 40,
		AccelKind: "nvidia", AccelModel: gtx, AccelCount: 1,
		CompatibleModels: []string{gtx}, ImageDigest: "sha256:gpu",
	}
}

func cpuRequest(arch string) scheduler.Request {
	return scheduler.Request{
		Region: "demo", CapacityClass: "dedicated", Arch: arch,
		VCPU: 2, RAMMiB: 2048, DiskGiB: 20, ImageDigest: "sha256:cpu-" + arch,
	}
}

func accel(local, model string) scheduler.Accelerator {
	return scheduler.Accelerator{ID: uuid.New(), LocalID: local, Kind: "nvidia", Model: model}
}

func node(gpus ...scheduler.Accelerator) scheduler.Node {
	n := scheduler.Node{
		ID: uuid.New(), PartnerID: uuid.New(), Hostname: "n", Status: db.NodeStatusReady,
		Region: "demo", CapacityClass: "dedicated", Arch: "amd64",
		FreeVCPU: 16, FreeRAMMiB: 32768, FreeDiskGiB: 500, FreeAccelerators: gpus,
	}
	if len(gpus) > 0 {
		n.AcceleratorKinds = []string{"nvidia"}
	}
	return n
}

func TestFilter(t *testing.T) {
	gpuNode := func(mut func(*scheduler.Node)) scheduler.Node {
		n := node(accel("0000:01:00.0", gtx))
		if mut != nil {
			mut(&n)
		}
		return n
	}
	tests := []struct {
		name string
		req  scheduler.Request
		node scheduler.Node
		want string
	}{
		{"gpu fits", gpuRequest(), gpuNode(nil), ""},
		{"cpu request on a gpu node", cpuRequest("amd64"), gpuNode(nil), ""},
		{"cpu request on a cpu node", cpuRequest("amd64"), node(), ""},
		{"exactly enough resources", cpuRequest("amd64"), func() scheduler.Node {
			n := node()
			n.FreeVCPU, n.FreeRAMMiB, n.FreeDiskGiB = 2, 2048, 20
			return n
		}(), ""},
		{"draining", gpuRequest(), gpuNode(func(n *scheduler.Node) { n.Status = db.NodeStatusDraining }), scheduler.RejectStatus},
		{"unreachable", gpuRequest(), gpuNode(func(n *scheduler.Node) { n.Status = db.NodeStatusUnreachable }), scheduler.RejectStatus},
		{"other region", gpuRequest(), gpuNode(func(n *scheduler.Node) { n.Region = "eu-1" }), scheduler.RejectRegion},
		{"other capacity class", gpuRequest(), gpuNode(func(n *scheduler.Node) { n.CapacityClass = "preemptible" }), scheduler.RejectCapacityClass},
		{"arm64 image on amd64 node", cpuRequest("arm64"), node(), scheduler.RejectArch},
		{"amd64 image on arm64 node", cpuRequest("amd64"), func() scheduler.Node { n := node(); n.Arch = "arm64"; return n }(), scheduler.RejectArch},
		{"node without inventory", cpuRequest("amd64"), func() scheduler.Node { n := node(); n.Arch = ""; return n }(), scheduler.RejectArch},
		{"driver cannot attach nvidia", gpuRequest(), gpuNode(func(n *scheduler.Node) { n.AcceleratorKinds = []string{"mock"} }), scheduler.RejectAcceleratorKind},
		{"image stack does not support the model", func() scheduler.Request {
			r := gpuRequest()
			r.CompatibleModels = []string{rtx}
			return r
		}(), gpuNode(nil), scheduler.RejectImageModel},
		{"image without model restriction", func() scheduler.Request {
			r := gpuRequest()
			r.CompatibleModels = nil
			return r
		}(), gpuNode(nil), ""},
		{"max instances reached", gpuRequest(), gpuNode(func(n *scheduler.Node) { n.MaxInstances, n.ActiveInstances = 1, 1 }), scheduler.RejectMaxInstances},
		{"max instances not reached", gpuRequest(), gpuNode(func(n *scheduler.Node) { n.MaxInstances, n.ActiveInstances = 2, 1 }), ""},
		{"not enough vcpu", gpuRequest(), gpuNode(func(n *scheduler.Node) { n.FreeVCPU = 3 }), scheduler.RejectVCPU},
		{"not enough ram", gpuRequest(), gpuNode(func(n *scheduler.Node) { n.FreeRAMMiB = 8191 }), scheduler.RejectRAM},
		{"not enough disk", gpuRequest(), gpuNode(func(n *scheduler.Node) { n.FreeDiskGiB = 39 }), scheduler.RejectDisk},
		{"no free gpu", gpuRequest(), gpuNode(func(n *scheduler.Node) { n.FreeAccelerators = nil }), scheduler.RejectAccelerators},
		{"free gpu of another model", gpuRequest(), gpuNode(func(n *scheduler.Node) {
			n.FreeAccelerators = []scheduler.Accelerator{accel("0000:01:00.0", rtx)}
		}), scheduler.RejectAccelerators},
		{"free gpu of another kind", gpuRequest(), gpuNode(func(n *scheduler.Node) {
			n.FreeAccelerators[0].Kind = "mock"
		}), scheduler.RejectAccelerators},
		{"two gpus wanted, one free", func() scheduler.Request {
			r := gpuRequest()
			r.AccelCount = 2
			return r
		}(), gpuNode(nil), scheduler.RejectAccelerators},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := scheduler.Filter(tc.req, tc.node); got != tc.want {
				t.Fatalf("Filter = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPickTakesTheFirstMatchingByLocalID(t *testing.T) {
	r := gpuRequest()
	r.AccelCount = 2
	n := node(accel("0000:01:00.0", rtx), accel("0000:02:00.0", gtx), accel("0000:03:00.0", gtx), accel("0000:04:00.0", gtx))
	got := scheduler.Pick(r, n)
	if len(got) != 2 || got[0].LocalID != "0000:02:00.0" || got[1].LocalID != "0000:03:00.0" {
		t.Fatalf("Pick = %+v", got)
	}
	r.AccelCount = 4
	if got := scheduler.Pick(r, n); got != nil {
		t.Fatalf("Pick with too few = %+v, want nil", got)
	}
	if got := scheduler.Pick(cpuRequest("amd64"), n); got != nil {
		t.Fatalf("Pick for a cpu request = %+v, want nil", got)
	}
}

func TestRank(t *testing.T) {
	older := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	newer := older.Add(time.Hour)
	named := func(name string, n scheduler.Node) scheduler.Node { n.Hostname = name; return n }
	hosts := func(ns []scheduler.Node) string {
		out := make([]string, len(ns))
		for i, n := range ns {
			out[i] = n.Hostname
		}
		return strings.Join(out, ",")
	}

	tests := []struct {
		name  string
		req   scheduler.Request
		nodes []scheduler.Node
		want  string
	}{
		{
			name: "cpu request prefers the node without gpus",
			req:  cpuRequest("amd64"),
			nodes: []scheduler.Node{
				named("gpu", node(accel("a", gtx))),
				named("cpu", node()),
			},
			want: "cpu,gpu",
		},
		{
			name: "gpu request prefers the node with the fewest free gpus left",
			req:  gpuRequest(),
			nodes: []scheduler.Node{
				named("four", node(accel("a", gtx), accel("b", gtx), accel("c", gtx), accel("d", gtx))),
				named("one", node(accel("a", gtx))),
				named("two", node(accel("a", gtx), accel("b", rtx))),
			},
			want: "one,two,four",
		},
		{
			name: "filtered nodes are dropped",
			req:  gpuRequest(),
			nodes: []scheduler.Node{
				named("none", node()),
				named("one", node(accel("a", gtx))),
				named("rtx", node(accel("a", rtx))),
			},
			want: "one",
		},
		{
			name: "then the node that has the image cached",
			req:  cpuRequest("amd64"),
			nodes: []scheduler.Node{
				named("cold", node()),
				named("warm", func() scheduler.Node { n := node(); n.CachedDigests = []string{"sha256:cpu-amd64"}; return n }()),
			},
			want: "warm,cold",
		},
		{
			name: "then the partner that waited longest",
			req:  cpuRequest("amd64"),
			nodes: []scheduler.Node{
				named("recent", func() scheduler.Node { n := node(); n.PartnerLastPlacedAt = newer; return n }()),
				named("never", node()),
				named("earlier", func() scheduler.Node { n := node(); n.PartnerLastPlacedAt = older; return n }()),
			},
			want: "never,earlier,recent",
		},
		{
			name: "best fit outranks a cached image",
			req:  cpuRequest("amd64"),
			nodes: []scheduler.Node{
				named("gpu-warm", func() scheduler.Node {
					n := node(accel("a", gtx))
					n.CachedDigests = []string{"sha256:cpu-amd64"}
					return n
				}()),
				named("cpu-cold", node()),
			},
			want: "cpu-cold,gpu-warm",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := hosts(scheduler.Rank(tc.req, tc.nodes)); got != tc.want {
				t.Fatalf("Rank = %s, want %s", got, tc.want)
			}
		})
	}

	// Equal on every criterion: ordered by id, whatever the input order.
	a, b := node(), node()
	if a.ID.String() > b.ID.String() {
		a, b = b, a
	}
	a.Hostname, b.Hostname = "a", "b"
	if got := hosts(scheduler.Rank(cpuRequest("amd64"), []scheduler.Node{b, a})); got != "a,b" {
		t.Fatalf("tie order = %s, want a,b", got)
	}
}

func TestExplain(t *testing.T) {
	if got := scheduler.Explain(gpuRequest(), nil); !strings.Contains(got, "no ready node") {
		t.Fatalf("Explain(no nodes) = %q", got)
	}
	arm := node()
	arm.Arch = "arm64"
	got := scheduler.Explain(gpuRequest(), []scheduler.Node{node(), node(), arm})
	if got != "3 nodes rejected: accelerator_kind ×2, arch ×1" {
		t.Fatalf("Explain = %q", got)
	}
}
