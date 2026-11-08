package agent

import (
	"maps"

	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/St1viG/cj/gen/go/kondukter/agent/v1"
	"github.com/St1viG/cj/internal/agent/config"
	"github.com/St1viG/cj/internal/agent/driver"
	"github.com/St1viG/cj/internal/agent/reconcile"
)

func capabilitiesProto(c driver.Capabilities) *agentv1.Capabilities {
	return &agentv1.Capabilities{
		Arch:             c.Arch,
		AcceleratorKinds: c.AcceleratorKinds,
		GpuPassthrough:   c.GPUPassthrough,
		MaxInstances:     clampU32(c.MaxInstances),
	}
}

// InventoryProto converts discovered resources plus the config's
// reservations, labels and advertise address into the protocol message.
func InventoryProto(inv driver.Inventory, cfg config.Config) *agentv1.Inventory {
	out := &agentv1.Inventory{
		Hostname: inv.Hostname,
		Arch:     inv.Arch,
		Os:       inv.OS,
		Kernel:   inv.Kernel,
		Total:    resourcesProto(inv.Total),
		Reserved: resourcesProto(driver.Resources{
			VCPU: cfg.Reserved.VCPU, RAMMiB: cfg.Reserved.RAMMiB, DiskGiB: cfg.Reserved.DiskGiB,
		}),
		Labels:           maps.Clone(cfg.Labels),
		AdvertiseAddress: cfg.AdvertiseAddress,
	}
	for _, a := range inv.Accelerators {
		health := agentv1.AcceleratorHealth_ACCELERATOR_HEALTH_UNAVAILABLE
		if a.Health == driver.HealthOK {
			health = agentv1.AcceleratorHealth_ACCELERATOR_HEALTH_OK
		}
		out.Accelerators = append(out.Accelerators, &agentv1.Accelerator{
			Id: a.ID, Kind: a.Kind, Model: a.Model, MemMib: clampU64(a.MemMiB),
			PciAddress: a.PCIAddress, VendorId: a.VendorID, DeviceId: a.DeviceID,
			NumaNode: clampI32(a.NUMANode), IommuGroup: clampI32(a.IOMMUGroup),
			HostDriver: a.HostDriver, Health: health, HealthReason: a.HealthReason,
			CompanionPciAddresses: a.Companions,
		})
	}
	for _, img := range inv.CachedImages {
		out.CachedImages = append(out.CachedImages, &agentv1.CachedImage{Name: img.Name, Version: img.Version, Digest: img.Digest})
	}
	return out
}

func resourcesProto(r driver.Resources) *agentv1.Resources {
	return &agentv1.Resources{Vcpu: clampU32(r.VCPU), RamMib: clampU64(r.RAMMiB), DiskGib: clampU64(r.DiskGiB)}
}

func statusProto(o driver.Observed, gen uint64) *agentv1.InstanceStatus {
	return &agentv1.InstanceStatus{
		Id:                o.ID,
		Phase:             reconcile.PhaseProto(o.Phase),
		Reason:            o.Reason,
		GuestIps:          o.GuestIPs,
		AppliedGeneration: gen,
		At:                timestamppb.Now(),
	}
}

func clampU32(v int) uint32 {
	if v < 0 {
		return 0
	}
	if v > 1<<32-1 {
		return 1<<32 - 1
	}
	return uint32(v)
}

func clampU64(v int64) uint64 {
	if v < 0 {
		return 0
	}
	return uint64(v)
}

func clampI32(v int) int32 {
	if v < -1<<31 || v > 1<<31-1 {
		return -1
	}
	return int32(v)
}
