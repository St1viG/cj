package storetest

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/St1viG/cj/internal/store"
	"github.com/St1viG/cj/internal/store/db"
)

// Fixture is a minimal catalog and tenancy: region "demo", one partner with
// one site, one customer org with one user, a CPU and a GPU offer, and CPU
// images for both architectures plus one GPU image.
type Fixture struct {
	Region   string
	Partner  db.Partner
	Site     db.Site
	Org      db.Org
	User     db.User
	CPUShape db.Shape
	GPUShape db.Shape
	CPUOffer db.Offer
	GPUOffer db.Offer
	// CPUImages is keyed by arch ("amd64", "arm64").
	CPUImages map[string]db.Image
	GPUImage  db.Image
}

// GPUModel is the accelerator model the fixture's GPU shape asks for.
const GPUModel = "GeForce GTX 1060 6GB"

// Seed inserts the fixture. It is idempotent.
func Seed(t testing.TB, s *store.Store) Fixture {
	t.Helper()
	ctx := context.Background()
	f := Fixture{Region: "demo", CPUImages: map[string]db.Image{}}
	must(t, s.UpsertRegion(ctx, db.UpsertRegionParams{Name: f.Region, Description: "Demo fleet"}))

	var err error
	f.Partner, err = s.UpsertPartner(ctx, db.UpsertPartnerParams{Name: "demo-dc"})
	must(t, err)
	f.Site, err = s.UpsertSite(ctx, db.UpsertSiteParams{PartnerID: f.Partner.ID, Name: "demo", Region: f.Region})
	must(t, err)
	f.Org, err = s.UpsertOrg(ctx, db.UpsertOrgParams{Name: "acme", Kind: "customer"})
	must(t, err)
	f.User, err = s.UpsertUser(ctx, db.UpsertUserParams{Email: "customer@demo.local", DisplayName: "Customer"})
	must(t, err)
	must(t, s.AddMembership(ctx, db.AddMembershipParams{OrgID: f.Org.ID, UserID: f.User.ID}))

	f.CPUShape, err = s.UpsertShape(ctx, db.UpsertShapeParams{Name: "cpu-2x2", Vcpu: 2, RamMib: 2048, DiskGib: 20})
	must(t, err)
	f.GPUShape, err = s.UpsertShape(ctx, db.UpsertShapeParams{
		Name: "gpu-gtx1060-1x", Vcpu: 4, RamMib: 8192, DiskGib: 40,
		AccelKind: "nvidia", AccelModel: GPUModel, AccelCount: 1,
	})
	must(t, err)
	f.CPUOffer, err = s.UpsertOffer(ctx, db.UpsertOfferParams{
		ShapeID: f.CPUShape.ID, Region: f.Region, CapacityClass: "dedicated",
		PricePerHourMinor: 6, Currency: "EUR", Active: true,
	})
	must(t, err)
	f.GPUOffer, err = s.UpsertOffer(ctx, db.UpsertOfferParams{
		ShapeID: f.GPUShape.ID, Region: f.Region, CapacityClass: "dedicated",
		PricePerHourMinor: 60, Currency: "EUR", Active: true,
	})
	must(t, err)

	for _, arch := range []string{"amd64", "arm64"} {
		img, err := s.UpsertImage(ctx, db.UpsertImageParams{
			Name: "ubuntu-cpu", Version: "24.04", Arch: arch, Os: "ubuntu 24.04",
			CompatibleModels: []string{}, Digest: "sha256:" + arch,
			Url: "http://images.test/ubuntu-cpu-" + arch + ".img", Format: "qcow2", Status: "active",
		})
		must(t, err)
		f.CPUImages[arch] = img
	}
	f.GPUImage, err = s.UpsertImage(ctx, db.UpsertImageParams{
		Name: "ubuntu-gpu-pascal", Version: "24.04", Arch: "amd64", Os: "ubuntu 24.04",
		AccelStack: "nvidia-580-cuda12", CompatibleModels: []string{GPUModel},
		Digest: "sha256:gpu", Url: "http://images.test/ubuntu-gpu-pascal.qcow2",
		Format: "qcow2", Status: "active",
	})
	must(t, err)
	return f
}

// NodeOpts shapes a test node. Zero values pick a small amd64 READY node
// without accelerators.
type NodeOpts struct {
	Hostname string
	Arch     string
	Status   db.NodeStatus
	VCPU     int32
	RAMMiB   int64
	DiskGiB  int64
	// GPUs adds that many healthy accelerators of GPUModel.
	GPUs int
}

// AddNode inserts a node with capacity and accelerators.
func AddNode(t testing.TB, s *store.Store, siteID uuid.UUID, o NodeOpts) (db.Node, []db.Accelerator) {
	t.Helper()
	ctx := context.Background()
	if o.Hostname == "" {
		o.Hostname = "node-" + uuid.NewString()[:8]
	}
	if o.Arch == "" {
		o.Arch = "amd64"
	}
	if o.Status == "" {
		o.Status = db.NodeStatusReady
	}
	if o.VCPU == 0 {
		o.VCPU = 16
	}
	if o.RAMMiB == 0 {
		o.RAMMiB = 32768
	}
	if o.DiskGiB == 0 {
		o.DiskGiB = 500
	}
	n, err := s.CreateNode(ctx, db.CreateNodeParams{SiteID: siteID, Hostname: o.Hostname})
	must(t, err)
	n, err = s.UpdateNodeInventory(ctx, db.UpdateNodeInventoryParams{
		ID: n.ID, Hostname: o.Hostname, Arch: o.Arch, Os: "test", Driver: "mock",
		Labels: []byte("{}"), CachedImages: []byte("[]"),
	})
	must(t, err)
	n, err = s.SetNodeStatus(ctx, db.SetNodeStatusParams{ID: n.ID, Status: o.Status})
	must(t, err)
	must(t, s.UpsertNodeCapacity(ctx, db.UpsertNodeCapacityParams{
		NodeID: n.ID, VcpuTotal: o.VCPU, RamMibTotal: o.RAMMiB, DiskGibTotal: o.DiskGiB,
	}))
	accels := make([]db.Accelerator, 0, o.GPUs)
	for i := range o.GPUs {
		a, err := s.UpsertAccelerator(ctx, db.UpsertAcceleratorParams{
			NodeID: n.ID, LocalID: fmt.Sprintf("0000:%02x:00.0", i+1), Kind: "nvidia",
			Model: GPUModel, MemMib: 6144, PciAddress: fmt.Sprintf("0000:%02x:00.0", i+1),
			NumaNode: 0, IommuGroup: int32(i + 1), HostDriver: "vfio-pci", Health: "ok",
		})
		must(t, err)
		accels = append(accels, a)
	}
	return n, accels
}

// AddInstance inserts a PENDING instance for the fixture's org.
func AddInstance(t testing.TB, s *store.Store, f Fixture, offer db.Offer, image db.Image) db.Instance {
	t.Helper()
	spec, err := json.Marshal(map[string]any{"image_id": image.ID})
	must(t, err)
	inst, err := s.CreateInstance(context.Background(), db.CreateInstanceParams{
		OrgID: f.Org.ID, OfferID: offer.ID, ImageID: image.ID, Spec: spec, CreatedBy: &f.User.ID,
	})
	must(t, err)
	return inst
}

func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("storetest: %v", err)
	}
}
