// Package libvirt is the Linux driver (ADR-0003, ADR-0019): one QEMU/KVM
// domain per instance on qemu:///system, spoken to over libvirt's RPC
// socket with go-libvirt, a pure-Go client, so the agent stays one static
// binary without cgo or libvirt's development headers.
//
// Ownership (SPEC §7.8): a domain is Kondukter's when its name is
// "kdk-<id>" and its <metadata> holds an instance element in the
// MetadataNS namespace whose id is <id>. The element is part of the XML the
// driver defines, so no domain of Kondukter's exists without it. Every
// other domain, including a "kdk-" one without the element, is never
// listed, started, stopped or undefined.
//
// Files live under the data directory: images/ is the verified image cache
// that instances share, and instances/<id>/ holds an instance's qcow2
// overlay (disk.qcow2), its NoCloud seed (seed.iso) and its UEFI variables
// (nvram). Deleting an instance removes its directory; one left behind by
// an interrupted create or delete is listed, so the reconciler deletes it.
//
// The first create on a node downloads the image, minutes for a GPU image,
// so Ensure creates and starts VMs in the background and reports
// provisioning. The driver fires an event when such an operation ends and
// on every libvirt lifecycle event of a "kdk-" domain.
package libvirt

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	golibvirt "github.com/digitalocean/go-libvirt"

	"github.com/St1viG/cj/internal/agent/driver"
	"github.com/St1viG/cj/internal/agent/inventory"
)

const (
	// MetadataNS is the namespace of the ownership element in a domain's
	// <metadata>.
	MetadataNS = "kondukter://libvirt/instance"
	// DefaultNetwork is the libvirt network guests attach to (ADR-0016).
	DefaultNetwork = "kdk-net"
	// DefaultPidDir is where libvirt's QEMU driver keeps the pid file of
	// each running domain.
	DefaultPidDir = "/run/libvirt/qemu"
)

// Files of an instance, in its directory.
const (
	diskFile  = "disk.qcow2"
	seedFile  = "seed.iso"
	nvramFile = "nvram"
)

// Options configure the driver.
type Options struct {
	// Dial connects to libvirt; nil dials DefaultSocket.
	Dial DialFunc
	// DataDir holds images/ and instances/. Required.
	DataDir string
	// Network is the libvirt network guests attach to.
	Network Network
	// Run runs qemu-img; nil runs it from PATH.
	Run Runner
	// HTTP fetches images; nil means a client without a timeout, as the
	// create's context bounds the download.
	HTTP *http.Client
	// Accelerators lists the host's passthrough devices; nil reads sysfs.
	Accelerators func() ([]driver.Accelerator, error)
	// PidDir is where libvirt writes "<domain>.pid"; its mtime is when the
	// VM started. Empty means DefaultPidDir.
	PidDir       string
	MaxInstances int
	// Arch is the host architecture; empty means runtime.GOARCH.
	Arch string
	Log  *slog.Logger
	Now  func() time.Time
}

// Network is the guest network: a libvirt network in open forward mode on
// its own bridge, whose filtering and NAT the agent's nftables table does
// (ADR-0016).
type Network struct {
	// Name is the libvirt network; empty means DefaultNetwork.
	Name   string
	Bridge string
	Subnet netip.Prefix
	// Gateway is the bridge's address; invalid means the subnet's first.
	Gateway netip.Addr
	// DNS is the resolver DHCP hands to guests; invalid means the gateway,
	// where libvirt's dnsmasq answers.
	DNS netip.Addr
}

// Driver is the libvirt driver.
type Driver struct {
	opts   Options
	images *Images
	events chan driver.Event

	cmu sync.Mutex
	c   Conn

	// netmu serializes defining and starting the network.
	netmu sync.Mutex

	wmu         sync.Mutex
	watching    bool
	closed      bool
	watchCancel context.CancelFunc
	watchDone   chan struct{}

	mu  sync.Mutex
	ops map[string]*op
	// seen records when a running domain without a pid file was first seen.
	seen map[string]time.Time
}

// op is a background create or start of one instance.
type op struct {
	what   string
	cancel context.CancelFunc
	done   chan struct{}
	err    error
}

var (
	_ driver.Driver      = (*Driver)(nil)
	_ driver.EventSource = (*Driver)(nil)
)

// New returns a libvirt driver. It does not connect: the first call that
// needs libvirt does, and every later one reconnects if libvirtd restarted.
func New(opts Options) (*Driver, error) {
	if opts.DataDir == "" {
		return nil, errors.New("libvirt: a data directory is required")
	}
	dir, err := filepath.Abs(opts.DataDir)
	if err != nil {
		return nil, fmt.Errorf("libvirt: %w", err)
	}
	opts.DataDir = dir
	if err := opts.Network.normalize(); err != nil {
		return nil, fmt.Errorf("libvirt: network: %w", err)
	}
	if opts.Dial == nil {
		opts.Dial = DialSocket(DefaultSocket)
	}
	if opts.Run == nil {
		opts.Run = Exec
	}
	if opts.HTTP == nil {
		opts.HTTP = &http.Client{}
	}
	if opts.Accelerators == nil {
		opts.Accelerators = func() ([]driver.Accelerator, error) { return inventory.NVIDIAGPUs("/") }
	}
	if opts.PidDir == "" {
		opts.PidDir = DefaultPidDir
	}
	if opts.Arch == "" {
		opts.Arch = runtime.GOARCH
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	return &Driver{
		opts:   opts,
		images: &Images{Dir: filepath.Join(dir, "images"), HTTP: opts.HTTP, Log: opts.Log},
		events: make(chan driver.Event, 64),
		ops:    map[string]*op{},
		seen:   map[string]time.Time{},
	}, nil
}

// Name implements driver.Driver.
func (d *Driver) Name() string { return "libvirt" }

// Events implements driver.EventSource: libvirt lifecycle events of "kdk-"
// domains, and the end of every background create or start. The first call
// subscribes; the subscription survives libvirtd restarts.
func (d *Driver) Events() <-chan driver.Event {
	d.wmu.Lock()
	defer d.wmu.Unlock()
	if !d.watching && !d.closed {
		d.watching = true
		ctx, cancel := context.WithCancel(context.Background())
		d.watchCancel, d.watchDone = cancel, make(chan struct{})
		go func(done chan struct{}) {
			defer close(done)
			d.watch(ctx)
		}(d.watchDone)
	}
	return d.events
}

func (d *Driver) stopWatch() {
	d.wmu.Lock()
	d.closed = true
	cancel, done := d.watchCancel, d.watchDone
	d.wmu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

// Capabilities implements driver.Driver.
func (d *Driver) Capabilities(context.Context) (driver.Capabilities, error) {
	accs, err := d.opts.Accelerators()
	if err != nil {
		return driver.Capabilities{}, err
	}
	kinds := []string{}
	for _, a := range accs {
		if !slices.Contains(kinds, a.Kind) {
			kinds = append(kinds, a.Kind)
		}
	}
	return driver.Capabilities{Arch: d.opts.Arch, AcceleratorKinds: kinds, GPUPassthrough: len(accs) > 0, MaxInstances: d.opts.MaxInstances}, nil
}

// Inventory implements driver.Driver: the host from /proc and sysfs, the
// passthrough devices, and the image cache.
func (d *Driver) Inventory(ctx context.Context) (driver.Inventory, error) {
	inv, err := inventory.Discover(ctx, inventory.Options{DataDir: d.opts.DataDir})
	if err != nil {
		return inv, err
	}
	if inv.Accelerators, err = d.opts.Accelerators(); err != nil {
		return inv, err
	}
	inv.CachedImages, err = d.images.List()
	return inv, err
}

// InstanceName is the domain name of a Kondukter instance.
func InstanceName(id string) string { return driver.NamePrefix + id }

// Ensure implements driver.Driver: a running domain is reported, a stopped
// one started, and a missing one created and started, the last two in the
// background.
func (d *Driver) Ensure(ctx context.Context, spec driver.InstanceSpec) (driver.Observed, error) {
	if err := Validate(spec, d.opts.Arch); err != nil {
		return driver.Observed{}, err
	}
	if o, busy, err := d.pending(spec.ID); busy || err != nil {
		return o, err
	}
	var (
		o     driver.Observed
		state golibvirt.DomainState
		found bool
	)
	err := d.call(ctx, func(c Conn) error {
		dom, ok, err := d.find(c, spec.ID)
		if found = ok; err != nil || !ok {
			return err
		}
		o, state, err = d.observe(c, spec.ID, dom)
		return err
	})
	switch {
	case err != nil:
		return driver.Observed{}, err
	case !found:
		hostdevs, err := d.admit(ctx, spec)
		if err != nil {
			return driver.Observed{}, err
		}
		return d.background(spec.ID, "creating the VM", func(ctx context.Context) error { return d.create(ctx, spec, hostdevs) }), nil
	case state == golibvirt.DomainShutoff || state == golibvirt.DomainCrashed:
		// Stopped while desired: the guest powered off, QEMU crashed, or
		// the host rebooted.
		return d.background(spec.ID, "starting the stopped VM", func(ctx context.Context) error { return d.start(ctx, spec.ID) }), nil
	}
	return o, nil
}

// admit checks that a new instance fits on the host and returns the PCI
// functions to pass through: each accelerator and its IOMMU group
// companions.
func (d *Driver) admit(ctx context.Context, spec driver.InstanceSpec) ([]PCIAddress, error) {
	var hostdevs []PCIAddress
	if len(spec.AcceleratorIDs) > 0 {
		accs, err := d.opts.Accelerators()
		if err != nil {
			return nil, fmt.Errorf("libvirt: list accelerators: %w", err)
		}
		for _, id := range spec.AcceleratorIDs {
			i := slices.IndexFunc(accs, func(a driver.Accelerator) bool { return a.ID == id })
			if i < 0 {
				return nil, fmt.Errorf("%w: no accelerator %q on this host", driver.ErrUnsupported, id)
			}
			a := accs[i]
			if a.Health != driver.HealthOK {
				return nil, fmt.Errorf("libvirt: accelerator %s cannot be attached: %s", id, a.HealthReason)
			}
			addr := a.PCIAddress
			if addr == "" {
				addr = a.ID
			}
			for _, s := range append([]string{addr}, a.Companions...) {
				p, err := ParsePCIAddress(s)
				if err != nil {
					return nil, fmt.Errorf("libvirt: accelerator %s: %w", id, err)
				}
				if !slices.Contains(hostdevs, p) {
					hostdevs = append(hostdevs, p)
				}
			}
		}
	}
	if d.opts.MaxInstances <= 0 {
		return hostdevs, nil
	}
	n := 0
	err := d.call(ctx, func(c Conn) error {
		owned, _, err := d.owned(c)
		n = len(owned)
		return err
	})
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	for id, o := range d.ops {
		if id != spec.ID && !o.finished() {
			n++ // a create whose domain is not defined yet
		}
	}
	d.mu.Unlock()
	if n >= d.opts.MaxInstances {
		return nil, fmt.Errorf("libvirt: host is full (%d instances)", d.opts.MaxInstances)
	}
	return hostdevs, nil
}

// Delete implements driver.Driver: a background operation on the instance
// is canceled, the domain destroyed and undefined with its NVRAM, and the
// instance directory removed. A missing instance is not an error.
func (d *Driver) Delete(ctx context.Context, id string) error {
	if !idRe.MatchString(id) {
		return fmt.Errorf("libvirt: instance id %q is not valid", id)
	}
	if err := d.cancelOp(ctx, id); err != nil {
		return err
	}
	name := InstanceName(id)
	err := d.call(ctx, func(c Conn) error {
		dom, found, err := d.find(c, id)
		if err != nil || !found {
			return err
		}
		if err := c.DomainDestroyFlags(dom, golibvirt.DomainDestroyDefault); err != nil &&
			!isCode(err, golibvirt.ErrOperationInvalid) && !isCode(err, golibvirt.ErrNoDomain) {
			return fmt.Errorf("libvirt: destroy %s: %w", name, err)
		}
		flags := golibvirt.DomainUndefineNvram | golibvirt.DomainUndefineManagedSave |
			golibvirt.DomainUndefineSnapshotsMetadata | golibvirt.DomainUndefineCheckpointsMetadata
		if err := c.DomainUndefineFlags(dom, flags); err != nil && !isCode(err, golibvirt.ErrNoDomain) {
			return fmt.Errorf("libvirt: undefine %s: %w", name, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := os.RemoveAll(d.instanceDir(id)); err != nil {
		return fmt.Errorf("libvirt: %w", err)
	}
	d.mu.Lock()
	delete(d.seen, id)
	d.mu.Unlock()
	return nil
}

// List implements driver.Driver: Kondukter's domains, creates in progress,
// and instance directories without a domain.
func (d *Driver) List(ctx context.Context) ([]driver.Observed, error) {
	// Directories first: a create that defines its domain after this
	// read is then seen in the domain list or as an operation.
	dirs, err := d.instanceDirs()
	if err != nil {
		return nil, err
	}
	var (
		out    []driver.Observed
		listed map[string]bool
		named  map[string]bool
	)
	err = d.call(ctx, func(c Conn) error {
		out, listed = nil, map[string]bool{}
		owned, names, err := d.owned(c)
		if err != nil {
			return err
		}
		named = names
		for _, od := range owned {
			if o, busy := d.busy(od.id); busy {
				out, listed[od.id] = append(out, o), true
				continue
			}
			o, _, err := d.observe(c, od.id, od.dom)
			if isCode(err, golibvirt.ErrNoDomain) {
				continue // undefined since the list call
			}
			if err != nil {
				return err
			}
			out, listed[od.id] = append(out, o), true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// A create in progress may not have defined its domain yet; it is on
	// the host all the same, so the reconciler deletes it (canceling the
	// create) rather than forgetting it.
	d.mu.Lock()
	for id, o := range d.ops {
		if !listed[id] && !o.finished() {
			out, listed[id] = append(out, driver.Observed{ID: id, Phase: driver.PhaseProvisioning, Reason: o.what}), true
		}
	}
	d.mu.Unlock()
	for _, id := range dirs {
		if !listed[id] && !named[id] {
			out = append(out, driver.Observed{ID: id, Phase: driver.PhaseProvisioning, Reason: "disks left by an interrupted create or delete"})
		}
	}
	slices.SortFunc(out, func(a, b driver.Observed) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

type ownedDomain struct {
	id  string
	dom golibvirt.Domain
}

// owned lists Kondukter's domains, and the ids of all "kdk-<id>" domains,
// Kondukter's or not.
func (d *Driver) owned(c Conn) ([]ownedDomain, map[string]bool, error) {
	doms, _, err := c.ConnectListAllDomains(1, golibvirt.ConnectListDomainsActive|golibvirt.ConnectListDomainsInactive)
	if err != nil {
		return nil, nil, fmt.Errorf("libvirt: list domains: %w", err)
	}
	var out []ownedDomain
	names := map[string]bool{}
	for _, dom := range doms {
		id, ok := candidate(dom.Name)
		if !ok {
			continue
		}
		names[id] = true
		meta, ok, err := d.metadata(c, dom)
		switch {
		case isCode(err, golibvirt.ErrNoDomain):
			continue
		case err != nil:
			return nil, nil, err
		case !ok || meta.ID != id:
			continue
		}
		out = append(out, ownedDomain{id: id, dom: dom})
	}
	return out, names, nil
}

// candidate returns the id of a "kdk-<id>" domain name.
func candidate(name string) (string, bool) {
	id, ok := strings.CutPrefix(name, driver.NamePrefix)
	return id, ok && idRe.MatchString(id)
}

// find returns Kondukter's domain for id. A "kdk-<id>" domain that is not
// Kondukter's is an error: the driver must not create over it or delete it.
func (d *Driver) find(c Conn, id string) (golibvirt.Domain, bool, error) {
	name := InstanceName(id)
	dom, err := c.DomainLookupByName(name)
	if isCode(err, golibvirt.ErrNoDomain) {
		return dom, false, nil
	}
	if err != nil {
		return dom, false, fmt.Errorf("libvirt: look up %s: %w", name, err)
	}
	meta, ok, err := d.metadata(c, dom)
	switch {
	case isCode(err, golibvirt.ErrNoDomain):
		return dom, false, nil
	case err != nil:
		return dom, false, err
	case !ok || meta.ID != id:
		return dom, false, final(fmt.Errorf("libvirt: %s exists but was not created by Kondukter; it is left alone", name))
	}
	return dom, true, nil
}

// metadata reads the ownership element; ok is false when there is none.
func (d *Driver) metadata(c Conn, dom golibvirt.Domain) (Metadata, bool, error) {
	raw, err := c.DomainGetMetadata(dom, int32(golibvirt.DomainMetadataElement), golibvirt.OptString{MetadataNS}, golibvirt.DomainAffectCurrent)
	if isCode(err, golibvirt.ErrNoDomainMetadata) {
		return Metadata{}, false, nil
	}
	if err != nil {
		return Metadata{}, false, fmt.Errorf("libvirt: metadata of %s: %w", dom.Name, err)
	}
	m, err := ParseMetadata(raw)
	if err != nil {
		d.opts.Log.Warn("libvirt: unreadable kondukter metadata; the domain is left alone", "domain", dom.Name, "err", err)
		return Metadata{}, false, nil
	}
	return m, true, nil
}

// observe maps a domain's state to its Observed phase. A domain is running
// once the guest holds a DHCP lease, so the port map has an address.
func (d *Driver) observe(c Conn, id string, dom golibvirt.Domain) (driver.Observed, golibvirt.DomainState, error) {
	st, reason, err := c.DomainGetState(dom, 0)
	if err != nil {
		return driver.Observed{}, 0, err
	}
	state := golibvirt.DomainState(st)
	o := driver.Observed{ID: id, Phase: driver.PhaseProvisioning}
	switch state {
	case golibvirt.DomainRunning, golibvirt.DomainBlocked:
		ips, err := d.guestIPs(c, id, dom)
		if err != nil {
			return driver.Observed{}, 0, err
		}
		if len(ips) == 0 {
			o.Reason = "booting; waiting for the guest's DHCP lease"
			return o, state, nil
		}
		o.Phase, o.GuestIPs, o.StartedAt = driver.PhaseRunning, ips, d.startedAt(id)
		return o, state, nil
	case golibvirt.DomainPaused:
		switch golibvirt.DomainPausedReason(reason) {
		case golibvirt.DomainPausedStartingUp:
			o.Reason = "starting"
		case golibvirt.DomainPausedShuttingDown:
			o.Reason = "shutting down; the agent starts it again"
		case golibvirt.DomainPausedIoerror:
			o.Phase, o.Reason = driver.PhaseFailed, "paused after an I/O error (is the disk full?)"
		default:
			o.Phase, o.Reason = driver.PhaseFailed, fmt.Sprintf("paused (libvirt reason %d); left for an operator", reason)
		}
	case golibvirt.DomainShutdown:
		o.Reason = "shutting down; the agent starts it again"
	case golibvirt.DomainShutoff, golibvirt.DomainCrashed:
		o.Reason = "stopped; the agent starts it again"
		if golibvirt.DomainShutoffReason(reason) == golibvirt.DomainShutoffCrashed || state == golibvirt.DomainCrashed {
			o.Reason = "crashed; the agent starts it again"
		}
	case golibvirt.DomainPmsuspended:
		o.Phase, o.Reason = driver.PhaseFailed, "suspended by the guest; left for an operator"
	default:
		o.Reason = "starting"
	}
	d.mu.Lock()
	delete(d.seen, id)
	d.mu.Unlock()
	return o, state, nil
}

// guestIPs returns the guest's IPv4 addresses from libvirt's DHCP lease
// table: the domain's own view first, then the network's leases for its
// MAC (ADR-0019).
func (d *Driver) guestIPs(c Conn, id string, dom golibvirt.Domain) ([]string, error) {
	var ips []string
	ifaces, err := c.DomainInterfaceAddresses(dom, uint32(golibvirt.DomainInterfaceAddressesSrcLease), 0)
	if err != nil && !isLibvirtError(err) {
		return nil, err
	}
	for _, iface := range ifaces {
		for _, a := range iface.Addrs {
			if a.Type == int32(golibvirt.IPAddrTypeIpv4) {
				ips = append(ips, a.Addr)
			}
		}
	}
	if len(ips) == 0 {
		net, err := c.NetworkLookupByName(d.opts.Network.Name)
		if err != nil && !isLibvirtError(err) {
			return nil, err
		}
		if err == nil {
			leases, _, err := c.NetworkGetDhcpLeases(net, golibvirt.OptString{MAC(id)}, 1, 0)
			if err != nil && !isLibvirtError(err) {
				return nil, err
			}
			for _, l := range leases {
				if l.Type == int32(golibvirt.IPAddrTypeIpv4) {
					ips = append(ips, l.Ipaddr)
				}
			}
		}
	}
	ips = slices.DeleteFunc(ips, func(s string) bool { _, err := netip.ParseAddr(s); return err != nil })
	slices.Sort(ips)
	return slices.Compact(ips), nil
}

// startedAt is when QEMU started (the mtime of libvirt's pid file, which
// survives an agent restart), else when the agent first saw it run.
func (d *Driver) startedAt(id string) time.Time {
	if st, err := os.Stat(filepath.Join(d.opts.PidDir, InstanceName(id)+".pid")); err == nil {
		return st.ModTime()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	t, ok := d.seen[id]
	if !ok {
		t = d.opts.Now()
		d.seen[id] = t
	}
	return t
}

// create builds the instance's files, then defines and starts its domain.
// Every step is safe to repeat after a crash.
func (d *Driver) create(ctx context.Context, spec driver.InstanceSpec, hostdevs []PCIAddress) error {
	if err := d.ensureNetwork(ctx); err != nil {
		return err
	}
	if err := mkdirSearchable(d.opts.DataDir); err != nil {
		return err
	}
	base, err := d.images.Ensure(ctx, spec.Image)
	if err != nil {
		return err
	}
	dir := d.instanceDir(spec.ID)
	if err := d.mkdirs(dir); err != nil {
		return err
	}
	if err := CreateOverlay(ctx, d.opts.Run, base, imageFormat(spec.Image), filepath.Join(dir, diskFile), spec.DiskGiB); err != nil {
		return err
	}
	seed, err := SeedISO(spec)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(dir, seedFile), seed, 0o600); err != nil {
		return err
	}
	xml, err := RenderDomain(DomainParams{
		Name: InstanceName(spec.ID), ID: spec.ID, Generation: spec.Generation,
		VCPU: spec.VCPU, RAMMiB: spec.RAMMiB,
		Disk: filepath.Join(dir, diskFile), Seed: filepath.Join(dir, seedFile), NVRAM: filepath.Join(dir, nvramFile),
		MAC: MAC(spec.ID), Network: d.opts.Network.Name, Hostdevs: hostdevs,
	})
	if err != nil {
		return err
	}
	return d.call(ctx, func(c Conn) error {
		dom, err := c.DomainDefineXMLFlags(xml, golibvirt.DomainDefineValidate)
		if err != nil {
			return fmt.Errorf("libvirt: define %s: %w", InstanceName(spec.ID), err)
		}
		if err := ctx.Err(); err != nil {
			return err // a Delete is waiting; it undefines the domain
		}
		return startDomain(c, dom)
	})
}

// start boots a defined, stopped domain.
func (d *Driver) start(ctx context.Context, id string) error {
	if err := d.ensureNetwork(ctx); err != nil {
		return err
	}
	return d.call(ctx, func(c Conn) error {
		dom, found, err := d.find(c, id)
		if err != nil {
			return err
		}
		if !found {
			return final(fmt.Errorf("libvirt: %s was undefined before it could start", InstanceName(id)))
		}
		return startDomain(c, dom)
	})
}

// startDomain starts dom; one already running, as after a retried call, is
// not an error.
func startDomain(c Conn, dom golibvirt.Domain) error {
	_, err := c.DomainCreateWithFlags(dom, 0)
	if isCode(err, golibvirt.ErrOperationInvalid) {
		if st, _, serr := c.DomainGetState(dom, 0); serr == nil && golibvirt.DomainState(st) == golibvirt.DomainRunning {
			return nil
		}
	}
	if err != nil {
		return fmt.Errorf("libvirt: start %s: %w", dom.Name, err)
	}
	return nil
}

func (d *Driver) instanceDir(id string) string {
	return filepath.Join(d.opts.DataDir, "instances", id)
}

// instanceDirs lists the ids that have an instance directory.
func (d *Driver) instanceDirs() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(d.opts.DataDir, "instances"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("libvirt: %w", err)
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() && idRe.MatchString(e.Name()) {
			ids = append(ids, e.Name())
		}
	}
	return ids, nil
}

// pending reports a background operation on id: still running (busy), or
// finished with an error, which is returned once so the next pass retries.
func (d *Driver) pending(id string) (driver.Observed, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	o := d.ops[id]
	if o == nil {
		return driver.Observed{}, false, nil
	}
	if !o.finished() {
		return driver.Observed{ID: id, Phase: driver.PhaseProvisioning, Reason: o.what}, true, nil
	}
	delete(d.ops, id)
	return driver.Observed{}, false, o.err
}

// busy reports a still-running operation on id without consuming a
// finished one's error.
func (d *Driver) busy(id string) (driver.Observed, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if o := d.ops[id]; o != nil && !o.finished() {
		return driver.Observed{ID: id, Phase: driver.PhaseProvisioning, Reason: o.what}, true
	}
	return driver.Observed{}, false
}

// cancelOp cancels a background operation on id and waits for it to end.
func (d *Driver) cancelOp(ctx context.Context, id string) error {
	d.mu.Lock()
	o := d.ops[id]
	d.mu.Unlock()
	if o == nil {
		return nil
	}
	o.cancel()
	select {
	case <-o.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	d.mu.Lock()
	if d.ops[id] == o {
		delete(d.ops, id)
	}
	d.mu.Unlock()
	return nil
}

func (o *op) finished() bool {
	select {
	case <-o.done:
		return true
	default:
		return false
	}
}

// background runs fn for id unless an operation is already running, and
// fires an event when it ends. It is not tied to the reconcile pass: an
// image download outlives the pass that asked for it.
func (d *Driver) background(id, what string, fn func(context.Context) error) driver.Observed {
	d.mu.Lock()
	defer d.mu.Unlock()
	if o := d.ops[id]; o != nil {
		return driver.Observed{ID: id, Phase: driver.PhaseProvisioning, Reason: o.what}
	}
	ctx, cancel := context.WithCancel(context.Background())
	o := &op{what: what, cancel: cancel, done: make(chan struct{})}
	d.ops[id] = o
	d.opts.Log.Info("libvirt: "+what, "instance", id)
	go func() {
		defer cancel()
		o.err = fn(ctx)
		if o.err != nil && !errors.Is(ctx.Err(), context.Canceled) {
			d.opts.Log.Warn("libvirt: "+what+" failed", "instance", id, "err", o.err)
		}
		close(o.done)
		d.notify(id)
	}()
	return driver.Observed{ID: id, Phase: driver.PhaseProvisioning, Reason: what}
}
