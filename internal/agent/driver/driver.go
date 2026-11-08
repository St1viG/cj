// Package driver is the agent's hypervisor abstraction (SPEC §7.3). The
// reconcile loop only talks to a Driver; libvirt, lima and mock implement it.
//
// Ownership rule (SPEC §7.8): a driver lists, changes and deletes only
// instances it created itself, recognized by the "kdk-" name prefix and
// Kondukter metadata in the VM definition. Everything else on the host is
// invisible to it.
package driver

import (
	"context"
	"errors"
	"time"
)

// Driver manages Kondukter-owned VMs on one host.
type Driver interface {
	// Name is a stable identifier: "libvirt", "lima", "mock".
	Name() string
	// Capabilities describes what this driver can do on this host.
	Capabilities(ctx context.Context) (Capabilities, error)
	// Inventory discovers host resources: CPU, RAM, disk, accelerators, arch.
	// Totals only; reservations are applied by the agent from its config.
	Inventory(ctx context.Context) (Inventory, error)
	// Ensure converges the instance to spec. Idempotent: calling it again
	// with the same spec is a no-op once the instance is running.
	Ensure(ctx context.Context, spec InstanceSpec) (Observed, error)
	// Delete removes the instance and its disks. Idempotent: deleting a
	// missing instance succeeds.
	Delete(ctx context.Context, instanceID string) error
	// List returns only instances created by Kondukter on this host.
	List(ctx context.Context) ([]Observed, error)
}

// EventSource is implemented by drivers that can report lifecycle changes
// (a VM crashed, finished booting). Each event triggers a reconcile pass.
type EventSource interface {
	Events() <-chan Event
}

// Event says something changed for one instance.
type Event struct {
	InstanceID string
	At         time.Time
}

// NamePrefix marks every VM, domain or Lima instance Kondukter creates.
const NamePrefix = "kdk-"

// ErrUnsupported is returned when a spec asks for something the driver
// cannot do on this host, e.g. an accelerator on a CPU-only driver.
var ErrUnsupported = errors.New("driver: unsupported")

// Capabilities describes what a driver can do on this host.
type Capabilities struct {
	// Arch is "amd64" or "arm64".
	Arch string
	// AcceleratorKinds the driver can attach: "nvidia", "apple-metal", "mock".
	AcceleratorKinds []string
	GPUPassthrough   bool
	// MaxInstances is 0 for unlimited.
	MaxInstances int
}

// Resources is an amount of host capacity.
type Resources struct {
	VCPU    int
	RAMMiB  int64
	DiskGiB int64
}

// Inventory is what the host has.
type Inventory struct {
	Hostname     string
	Arch         string
	OS           string
	Kernel       string
	Total        Resources
	Accelerators []Accelerator
	CachedImages []CachedImage
}

// Health of an accelerator from the agent's point of view.
type Health string

const (
	// HealthOK means bound to the passthrough driver and ready to attach.
	HealthOK Health = "ok"
	// HealthUnavailable means present but not attachable; see HealthReason.
	HealthUnavailable Health = "unavailable"
)

// Accelerator is one attachable device.
type Accelerator struct {
	// ID is stable on this host: the PCI address for passthrough devices.
	ID         string
	Kind       string
	Model      string
	MemMiB     int64
	PCIAddress string
	VendorID   string
	DeviceID   string
	// NUMANode and IOMMUGroup are -1 when unknown.
	NUMANode     int
	IOMMUGroup   int
	HostDriver   string
	Health       Health
	HealthReason string
	// Companions are other PCI functions passed through with the device,
	// e.g. the GPU's HDMI audio function.
	Companions []string
}

// CachedImage is a guest image in the local cache.
type CachedImage struct {
	Name    string
	Version string
	Digest  string
}

// ImageRef points at a curated guest image.
type ImageRef struct {
	Name    string
	Version string
	Arch    string
	// Digest is "sha256:<hex>" of the file at URL.
	Digest    string
	URL       string
	SizeBytes int64
	// Format is "qcow2" or "raw".
	Format string
}

// Protocol of an exposed port.
type Protocol string

const (
	TCP Protocol = "tcp"
	UDP Protocol = "udp"
)

// PortSpec is a guest port to expose.
type PortSpec struct {
	GuestPort int
	Protocol  Protocol
}

// PortMapping is where a guest port is reachable from outside the node.
type PortMapping struct {
	GuestPort   int
	Protocol    Protocol
	HostAddress string
	HostPort    int
}

// InstanceSpec is everything a driver needs to create an instance.
type InstanceSpec struct {
	ID         string
	Generation uint64
	VCPU       int
	RAMMiB     int64
	DiskGiB    int64
	// AcceleratorIDs are concrete devices chosen by the scheduler
	// (Accelerator.ID values).
	AcceleratorIDs []string
	Image          ImageRef
	SSHKeys        []string
	// UserData is optional cloud-init, at most 16 KiB.
	UserData []byte
	// Ports to expose in addition to 22.
	Ports    []PortSpec
	Hostname string
	// PortMappings are filled in by the access module before Ensure on
	// drivers that forward ports themselves (Lima on macOS).
	PortMappings []PortMapping
}

// Phase is a driver-observed instance phase.
type Phase string

const (
	PhaseProvisioning Phase = "provisioning"
	PhaseRunning      Phase = "running"
	PhaseFailed       Phase = "failed"
	PhaseDeleting     Phase = "deleting"
)

// Observed is an instance as the driver sees it.
type Observed struct {
	ID        string
	Phase     Phase
	Reason    string
	GuestIPs  []string
	StartedAt time.Time
}
