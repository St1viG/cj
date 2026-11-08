package libvirt

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/St1viG/cj/internal/agent/driver"
	"github.com/St1viG/cj/internal/agent/inventory"
)

// Network is the guest network the driver defines in libvirt: an open-mode
// network (no libvirt firewall rules; the agent owns the nftables table,
// ADR-0016) with dnsmasq DHCP.
type Network struct {
	Bridge  string
	Subnet  netip.Prefix
	Gateway netip.Addr
}

// Options configure the driver.
type Options struct {
	// API talks to libvirt; nil dials the default sockets.
	API API
	// DataDir holds images/ and instances/. QEMU runs as another user, so
	// DataDir and both directories must be traversable by everyone.
	DataDir string
	Network Network
	// MaxInstances is reported in Capabilities; 0 is unlimited.
	MaxInstances int
	// Run runs qemu-img; nil uses ExecRunner.
	Run Runner
	// Inventory discovers the host; nil uses inventory.Discover.
	Inventory func(context.Context) (driver.Inventory, error)
	// RunDir is where libvirt keeps QEMU pid files, used for the start
	// time of a running instance; empty means /run/libvirt/qemu.
	RunDir string
	// Arch is the host architecture; empty means runtime.GOARCH.
	Arch string
	Log  *slog.Logger
	Now  func() time.Time
}

// Driver is the libvirt driver.
type Driver struct {
	opts   Options
	api    API
	cache  *ImageCache
	events chan driver.Event
	stop   context.CancelFunc

	netOnce sync.Mutex
	netDone bool

	mu   sync.Mutex
	ops  map[string]*op
	seen map[string]time.Time
}

// op is a background create of one instance (the image may need minutes to
// download, longer than a reconcile pass should wait).
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

// New returns a libvirt driver and starts watching libvirt's lifecycle
// events. Close stops the watcher.
func New(opts Options) (*Driver, error) {
	if opts.DataDir == "" {
		return nil, errors.New("libvirt: data dir is required")
	}
	if !opts.Network.Subnet.IsValid() || !opts.Network.Gateway.IsValid() || opts.Network.Bridge == "" {
		return nil, errors.New("libvirt: network bridge, subnet and gateway are required")
	}
	if opts.Network.Subnet.Bits() > 24 {
		return nil, fmt.Errorf("libvirt: subnet %s is too small, want /24 or larger", opts.Network.Subnet)
	}
	if opts.Arch == "" {
		opts.Arch = runtime.GOARCH
	}
	if opts.Run == nil {
		opts.Run = ExecRunner
	}
	if opts.RunDir == "" {
		opts.RunDir = "/run/libvirt/qemu"
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	if opts.Inventory == nil {
		dir := opts.DataDir
		opts.Inventory = func(ctx context.Context) (driver.Inventory, error) {
			return inventory.Discover(ctx, inventory.Options{DataDir: dir})
		}
	}
	for _, dir := range []string{opts.DataDir, join(opts.DataDir, "images"), join(opts.DataDir, "instances")} {
		if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // QEMU runs as another user and must traverse the data directory (S1 finding 5)
			return nil, err
		}
	}
	if fi, err := os.Stat(opts.DataDir); err != nil || fi.Mode().Perm()&0o001 == 0 {
		return nil, fmt.Errorf("libvirt: %s must be traversable by all (mode o+x): QEMU runs as another user and reads the disks there (spike S1)", opts.DataDir)
	}
	api := opts.API
	if api == nil {
		var err error
		if api, err = Dial("", ""); err != nil {
			return nil, err
		}
	}
	ctx, stop := context.WithCancel(context.Background())
	dr := &Driver{
		opts: opts, api: api, stop: stop,
		cache:  &ImageCache{Dir: join(opts.DataDir, "images")},
		events: make(chan driver.Event, 64),
		ops:    map[string]*op{}, seen: map[string]time.Time{},
	}
	go dr.watch(ctx)
	return dr, nil
}

func join(parts ...string) string { return filepath.Join(parts...) }

// Close stops the event watcher and closes the libvirt connections.
func (dr *Driver) Close() error {
	dr.stop()
	return dr.api.Close()
}

// Name implements driver.Driver.
func (dr *Driver) Name() string { return "libvirt" }

// Events implements driver.EventSource: libvirt lifecycle events of
// Kondukter's domains, and the end of a background create.
func (dr *Driver) Events() <-chan driver.Event { return dr.events }

func (dr *Driver) emit(id string) {
	select {
	case dr.events <- driver.Event{InstanceID: id, At: dr.opts.Now()}:
	default:
	}
}

// watch relays libvirt lifecycle events, resubscribing with a pause when the
// connection drops. Events lost in the gap are caught by the periodic
// reconcile pass.
func (dr *Driver) watch(ctx context.Context) {
	for ctx.Err() == nil {
		ch, err := dr.api.Events(ctx)
		if err != nil {
			dr.opts.Log.Warn("libvirt: subscribe to lifecycle events", "err", err)
		} else {
			for ev := range ch {
				if id, ok := strings.CutPrefix(ev.Name, driver.NamePrefix); ok && id != "" {
					dr.emit(id)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

// Capabilities implements driver.Driver.
func (dr *Driver) Capabilities(context.Context) (driver.Capabilities, error) {
	return driver.Capabilities{
		Arch: dr.opts.Arch, AcceleratorKinds: []string{"nvidia"},
		GPUPassthrough: true, MaxInstances: dr.opts.MaxInstances,
	}, nil
}

// Inventory implements driver.Driver.
func (dr *Driver) Inventory(ctx context.Context) (driver.Inventory, error) {
	inv, err := dr.opts.Inventory(ctx)
	if err != nil {
		return inv, err
	}
	inv.CachedImages = dr.cache.List()
	return inv, nil
}

func (dr *Driver) instanceDir(id string) string { return join(dr.opts.DataDir, "instances", id) }

// Validate checks that the spec is something this driver can run.
func Validate(spec driver.InstanceSpec, hostArch string) error {
	switch {
	case !idRe.MatchString(spec.ID):
		return fmt.Errorf("libvirt: instance id %q: want letters, digits and dashes, at most 63", spec.ID)
	case hostArch != "amd64":
		return fmt.Errorf("%w: the libvirt driver runs amd64 guests, this node is %s", driver.ErrUnsupported, hostArch)
	case spec.Image.Arch != "" && spec.Image.Arch != "amd64":
		return fmt.Errorf("%w: image %s is %s, this node is amd64", driver.ErrUnsupported, spec.Image.Name, spec.Image.Arch)
	case spec.VCPU < 1 || spec.RAMMiB < 1 || spec.DiskGiB < 1:
		return fmt.Errorf("libvirt: %d vCPU, %d MiB, %d GiB: every size must be positive", spec.VCPU, spec.RAMMiB, spec.DiskGiB)
	}
	if err := check(spec.Image); err != nil {
		return err
	}
	if _, err := MetaData(spec.ID, spec.Hostname); err != nil {
		return err
	}
	if _, err := BuildUserData(spec.SSHKeys, spec.UserData); err != nil {
		return err
	}
	return nil
}

// Ensure implements driver.Driver.
func (dr *Driver) Ensure(ctx context.Context, spec driver.InstanceSpec) (driver.Observed, error) {
	if err := Validate(spec, dr.opts.Arch); err != nil {
		return driver.Observed{}, err
	}
	if o, busy, err := dr.pending(spec.ID); busy || err != nil {
		return o, err
	}
	info, found, err := dr.find(ctx, spec.ID)
	if err != nil {
		return driver.Observed{}, err
	}
	switch {
	case found && info.Running:
		return dr.observe(ctx, spec.ID, info), nil
	case found:
		// Defined but not running: the node rebooted or the guest was
		// powered off. The customer's disk is intact; boot it again.
		if err := dr.api.Start(ctx, info.Name); err != nil {
			return driver.Observed{}, fmt.Errorf("libvirt: start %s: %w", info.Name, err)
		}
		dr.forget(spec.ID)
		return driver.Observed{ID: spec.ID, Phase: driver.PhaseProvisioning, Reason: "starting the stopped VM"}, nil
	}
	hostdevs, err := dr.hostdevs(ctx, spec)
	if err != nil {
		return driver.Observed{}, err
	}
	if dr.cache.Has(spec.Image) {
		if err := dr.create(ctx, spec, hostdevs); err != nil {
			return driver.Observed{}, err
		}
		return driver.Observed{ID: spec.ID, Phase: driver.PhaseProvisioning, Reason: "booting"}, nil
	}
	return dr.background(spec.ID, "fetching image "+spec.Image.Name, func(ctx context.Context) error {
		if _, err := dr.cache.Fetch(ctx, spec.Image); err != nil {
			return err
		}
		return dr.create(ctx, spec, hostdevs)
	}), nil
}

// hostdevs resolves the accelerators the scheduler chose into the PCI
// functions to pass through: each device and the other endpoints of its
// IOMMU group (the GPU's audio function).
func (dr *Driver) hostdevs(ctx context.Context, spec driver.InstanceSpec) ([]PCIAddr, error) {
	if len(spec.AcceleratorIDs) == 0 {
		return nil, nil
	}
	inv, err := dr.opts.Inventory(ctx)
	if err != nil {
		return nil, fmt.Errorf("libvirt: inventory: %w", err)
	}
	var out []PCIAddr
	for _, id := range spec.AcceleratorIDs {
		i := slices.IndexFunc(inv.Accelerators, func(a driver.Accelerator) bool { return a.ID == id })
		if i < 0 {
			return nil, fmt.Errorf("%w: accelerator %s is not on this node", driver.ErrUnsupported, id)
		}
		acc := inv.Accelerators[i]
		if acc.Health != driver.HealthOK {
			return nil, fmt.Errorf("%w: accelerator %s is %s: %s", driver.ErrUnsupported, id, acc.Health, acc.HealthReason)
		}
		for _, s := range append([]string{acc.PCIAddress}, acc.Companions...) {
			a, err := ParsePCI(s)
			if err != nil {
				return nil, err
			}
			if !slices.Contains(out, a) {
				out = append(out, a)
			}
		}
	}
	return out, nil
}

// create builds the instance's files and domain, and starts it. On failure
// it removes what it made, so the next attempt starts clean.
func (dr *Driver) create(ctx context.Context, spec driver.InstanceSpec, hostdevs []PCIAddr) (err error) {
	if err := dr.ensureNetwork(ctx); err != nil {
		return err
	}
	dir := dr.instanceDir(spec.ID)
	// A directory without a domain is the leftover of an earlier attempt.
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // QEMU runs as another user and must traverse the data directory (S1 finding 5)
		return err
	}
	name := DomainName(spec.ID)
	defined := false
	defer func() {
		if err == nil {
			return
		}
		// Cleanup must not depend on the caller's context being alive.
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if defined {
			_ = dr.api.Destroy(cctx, name)
			_ = dr.api.Undefine(cctx, name)
		}
		_ = os.RemoveAll(dir)
	}()

	disk, seed := join(dir, "disk.qcow2"), join(dir, "seed.iso")
	format := spec.Image.Format
	if format == "" {
		format = "qcow2"
	}
	if err = CreateOverlay(ctx, dr.opts.Run, dr.cache.Path(spec.Image), format, disk, spec.DiskGiB); err != nil {
		return fmt.Errorf("libvirt: create the disk: %w", err)
	}
	if err = WriteSeed(seed, spec.ID, spec.Hostname, spec.SSHKeys, spec.UserData); err != nil {
		return fmt.Errorf("libvirt: write the seed ISO: %w", err)
	}
	xml, err := RenderDomain(DomainParams{
		ID: spec.ID, Generation: spec.Generation, VCPU: spec.VCPU, RAMMiB: spec.RAMMiB,
		Disk: disk, Seed: seed, Hostdevs: hostdevs,
	})
	if err != nil {
		return err
	}
	if err = dr.api.Define(ctx, xml); err != nil {
		return fmt.Errorf("libvirt: define %s: %w", name, err)
	}
	defined = true
	if err = dr.api.Start(ctx, name); err != nil {
		return fmt.Errorf("libvirt: start %s: %w", name, err)
	}
	dr.forget(spec.ID)
	return nil
}

// ensureNetwork makes sure the guest network exists, once per process.
func (dr *Driver) ensureNetwork(ctx context.Context) error {
	dr.netOnce.Lock()
	defer dr.netOnce.Unlock()
	if dr.netDone {
		return nil
	}
	x, err := NetworkXML(dr.opts.Network)
	if err != nil {
		return err
	}
	if err := dr.api.EnsureNetwork(ctx, NetworkName, x); err != nil {
		return fmt.Errorf("libvirt: network %s: %w", NetworkName, err)
	}
	dr.netDone = true
	return nil
}

// NetworkXML is the definition of the guest network: forward mode "open"
// (libvirt adds no firewall rules), the bridge, and DHCP for .100–.200.
func NetworkXML(n Network) (string, error) {
	if n.Subnet.Bits() > 24 {
		return "", fmt.Errorf("libvirt: subnet %s is too small, want /24 or larger", n.Subnet)
	}
	base := n.Subnet.Masked().Addr().As4()
	host := func(last byte) netip.Addr { b := base; b[3] = last; return netip.AddrFrom4(b) }
	mask := net4Mask(n.Subnet.Bits())
	return fmt.Sprintf(`<network>
  <name>%s</name>
  <forward mode='open'/>
  <bridge name='%s' stp='off' delay='0'/>
  <ip address='%s' netmask='%s'>
    <dhcp><range start='%s' end='%s'/></dhcp>
  </ip>
</network>`, NetworkName, n.Bridge, n.Gateway, mask, host(100), host(200)), nil
}

func net4Mask(bits int) string {
	m := ^uint32(0) << (32 - bits)
	return fmt.Sprintf("%d.%d.%d.%d", byte(m>>24), byte(m>>16), byte(m>>8), byte(m)) //nolint:gosec // each shift leaves one octet, so the byte conversion is exact
}

// Delete implements driver.Driver. A background create is canceled first.
func (dr *Driver) Delete(ctx context.Context, id string) error {
	if !idRe.MatchString(id) {
		return fmt.Errorf("libvirt: instance id %q is not valid", id)
	}
	dr.mu.Lock()
	o := dr.ops[id]
	dr.mu.Unlock()
	if o != nil {
		o.cancel()
		select {
		case <-o.done:
		case <-ctx.Done():
			return ctx.Err()
		}
		dr.mu.Lock()
		if dr.ops[id] == o {
			delete(dr.ops, id)
		}
		dr.mu.Unlock()
	}
	info, found, err := dr.find(ctx, id)
	if err != nil {
		return err
	}
	if found {
		if err := dr.api.Destroy(ctx, info.Name); err != nil {
			return fmt.Errorf("libvirt: destroy %s: %w", info.Name, err)
		}
		if err := dr.api.Undefine(ctx, info.Name); err != nil {
			return fmt.Errorf("libvirt: undefine %s: %w", info.Name, err)
		}
	}
	if err := os.RemoveAll(dr.instanceDir(id)); err != nil {
		return err
	}
	dr.forget(id)
	return nil
}

// List implements driver.Driver: only Kondukter's instances.
func (dr *Driver) List(ctx context.Context) ([]driver.Observed, error) {
	doms, err := dr.api.Domains(ctx, driver.NamePrefix)
	if err != nil {
		return nil, err
	}
	var out []driver.Observed
	listed := map[string]bool{}
	for _, info := range doms {
		m, ok := ParseMarker(info.XML)
		if !ok || info.Name != DomainName(m.ID) {
			continue
		}
		listed[m.ID] = true
		if o, busy := dr.busy(m.ID); busy {
			out = append(out, o)
			continue
		}
		out = append(out, dr.observe(ctx, m.ID, info))
	}
	// A create in progress has no domain yet; it is on the host all the
	// same, so the reconciler deletes it (canceling the create).
	dr.mu.Lock()
	for id, o := range dr.ops {
		if !listed[id] && !o.finished() {
			out = append(out, driver.Observed{ID: id, Phase: driver.PhaseProvisioning, Reason: o.what})
		}
	}
	dr.mu.Unlock()
	slices.SortFunc(out, func(a, b driver.Observed) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

// find returns the Kondukter domain for id. A "kdk-<id>" domain that is not
// Kondukter's is an error: the driver must not create over it or touch it.
func (dr *Driver) find(ctx context.Context, id string) (DomainInfo, bool, error) {
	info, ok, err := dr.api.Domain(ctx, DomainName(id))
	if err != nil || !ok {
		return DomainInfo{}, false, err
	}
	if m, owned := ParseMarker(info.XML); !owned || m.ID != id {
		return DomainInfo{}, false, fmt.Errorf("libvirt: domain %s exists but was not created by Kondukter; it is left alone", info.Name)
	}
	return info, true, nil
}

// observe maps a domain to its phase. A running VM counts as running once
// it holds a DHCP lease in the guest subnet: the guest has booted far enough
// to configure its network.
func (dr *Driver) observe(ctx context.Context, id string, info DomainInfo) driver.Observed {
	if !info.Running {
		return driver.Observed{ID: id, Phase: driver.PhaseProvisioning, Reason: "stopped; the agent starts it again"}
	}
	ips, err := dr.api.GuestIPs(ctx, info.Name)
	if err != nil {
		dr.opts.Log.Warn("libvirt: read guest addresses", "instance", id, "err", err)
	}
	var guest []string
	for _, s := range ips {
		if a, err := netip.ParseAddr(s); err == nil && dr.opts.Network.Subnet.Contains(a) {
			guest = append(guest, s)
		}
	}
	if len(guest) == 0 {
		return driver.Observed{ID: id, Phase: driver.PhaseProvisioning, Reason: "booting; no address yet"}
	}
	return driver.Observed{ID: id, Phase: driver.PhaseRunning, GuestIPs: guest, StartedAt: dr.startedAt(id, info.Name)}
}

// startedAt is the mtime of QEMU's pid file, which survives an agent
// restart, else when the agent first saw the VM running.
func (dr *Driver) startedAt(id, name string) time.Time {
	if st, err := os.Stat(join(dr.opts.RunDir, name+".pid")); err == nil {
		return st.ModTime()
	}
	dr.mu.Lock()
	defer dr.mu.Unlock()
	t, ok := dr.seen[id]
	if !ok {
		t = dr.opts.Now()
		dr.seen[id] = t
	}
	return t
}

func (dr *Driver) forget(id string) {
	dr.mu.Lock()
	delete(dr.seen, id)
	dr.mu.Unlock()
}

// pending reports a background operation on id: still running (busy), or
// finished with an error, which is returned once so the next pass retries.
func (dr *Driver) pending(id string) (driver.Observed, bool, error) {
	dr.mu.Lock()
	defer dr.mu.Unlock()
	o := dr.ops[id]
	if o == nil {
		return driver.Observed{}, false, nil
	}
	select {
	case <-o.done:
		delete(dr.ops, id)
		return driver.Observed{}, false, o.err
	default:
		return driver.Observed{ID: id, Phase: driver.PhaseProvisioning, Reason: o.what}, true, nil
	}
}

// busy reports a still-running operation without consuming a finished
// one's error.
func (dr *Driver) busy(id string) (driver.Observed, bool) {
	dr.mu.Lock()
	defer dr.mu.Unlock()
	if o := dr.ops[id]; o != nil && !o.finished() {
		return driver.Observed{ID: id, Phase: driver.PhaseProvisioning, Reason: o.what}, true
	}
	return driver.Observed{}, false
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
// fires an event when it ends. It outlives the reconcile pass that asked
// for it.
func (dr *Driver) background(id, what string, fn func(context.Context) error) driver.Observed {
	dr.mu.Lock()
	defer dr.mu.Unlock()
	if o := dr.ops[id]; o != nil {
		return driver.Observed{ID: id, Phase: driver.PhaseProvisioning, Reason: o.what}
	}
	ctx, cancel := context.WithCancel(context.Background())
	o := &op{what: what, cancel: cancel, done: make(chan struct{})}
	dr.ops[id] = o
	dr.opts.Log.Info("libvirt: "+what, "instance", id)
	go func() {
		defer cancel()
		o.err = fn(ctx)
		if o.err != nil && !errors.Is(ctx.Err(), context.Canceled) {
			dr.opts.Log.Warn("libvirt: "+what+" failed", "instance", id, "err", o.err)
		}
		close(o.done)
		dr.emit(id)
	}()
	return driver.Observed{ID: id, Phase: driver.PhaseProvisioning, Reason: what}
}
