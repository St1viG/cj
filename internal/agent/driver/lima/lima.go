// Package lima is the macOS driver (ADR-0020): one Lima `vz` VM per
// instance, driven through limactl.
//
// Ownership (SPEC §7.8): an instance is Kondukter's when its name is
// "kdk-<id>" and its template parameter KONDUKTER_INSTANCE equals <id>.
// limactl writes that parameter into the instance's lima.yaml as it creates
// it, so there is no window in which a VM exists without the marker. Any
// other Lima instance on the host, including a "kdk-" one without the
// marker, is never listed, changed or deleted.
//
// Starting a VM takes 20–70 s (spike S3), longer than a reconcile pass
// should wait, so Ensure starts the create and start in the background and
// reports provisioning; the driver fires an event when it finishes. Only one
// limactl mutation runs at a time on the host.
package lima

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/St1viG/cj/internal/agent/driver"
	"github.com/St1viG/cj/internal/agent/inventory"
)

// readyMarker is written into the instance directory once Lima has first
// reported the VM ready: booted, provision scripts done, and with them the
// script's sync, which puts what the first boot wrote on disk. An instance
// without it never ran for the customer, and a hard power-off may have cost
// it files of that first boot (empty SSH host keys lock everyone out), so
// a stopped or broken one is re-created rather than started.
const readyMarker = "kondukter-ready"

// Options configure the driver.
type Options struct {
	// Run runs limactl; nil finds limactl with FindLimactl.
	Run Runner
	// StateDir holds rendered templates before `limactl create`.
	StateDir string
	// DataDir is where the inventory measures disk.
	DataDir      string
	MaxInstances int
	// Home is LIMA_HOME as ResolveHome returns it; when set, Ensure
	// refuses ids too long for Lima's socket paths under it.
	Home string
	// Arch is the host architecture; empty means runtime.GOARCH.
	Arch string
	// PollInterval is how often the driver checks on a boot that a previous
	// agent process started; 0 means 1 s.
	PollInterval time.Duration
	Log          *slog.Logger
	Now          func() time.Time
}

// Driver is the Lima driver.
type Driver struct {
	opts   Options
	events chan driver.Event

	// mutate serializes limactl create, start, stop and delete.
	mutate sync.Mutex

	mu  sync.Mutex
	ops map[string]*op
	// seen records when a running instance without ha.pid was first seen.
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

// New returns a Lima driver.
func New(opts Options) (*Driver, error) {
	if opts.Run == nil {
		path, err := FindLimactl("")
		if err != nil {
			return nil, err
		}
		opts.Run = Exec(path, "")
	}
	if opts.Arch == "" {
		opts.Arch = runtime.GOARCH
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = time.Second
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	if opts.StateDir != "" {
		if err := os.MkdirAll(opts.StateDir, 0o700); err != nil {
			return nil, err
		}
	}
	return &Driver{
		opts:   opts,
		events: make(chan driver.Event, 64),
		ops:    map[string]*op{},
		seen:   map[string]time.Time{},
	}, nil
}

// Name implements driver.Driver.
func (d *Driver) Name() string { return "lima" }

// Events implements driver.EventSource: one event when a background create
// or start finishes, successfully or not, and when a boot that a previous
// agent process started ends.
func (d *Driver) Events() <-chan driver.Event { return d.events }

// Capabilities implements driver.Driver: CPU-only VMs of the host's
// architecture (ADR-0011).
func (d *Driver) Capabilities(context.Context) (driver.Capabilities, error) {
	return driver.Capabilities{Arch: d.opts.Arch, AcceleratorKinds: []string{}, MaxInstances: d.opts.MaxInstances}, nil
}

// Inventory implements driver.Driver.
func (d *Driver) Inventory(ctx context.Context) (driver.Inventory, error) {
	dir := d.opts.DataDir
	if dir == "" {
		dir = "/"
	}
	return inventory.Discover(ctx, inventory.Options{DataDir: dir})
}

// InstanceName is the Lima instance name of a Kondukter instance.
func InstanceName(id string) string { return driver.NamePrefix + id }

// Ensure implements driver.Driver.
func (d *Driver) Ensure(ctx context.Context, spec driver.InstanceSpec) (driver.Observed, error) {
	if err := Validate(spec, d.opts.Arch); err != nil {
		return driver.Observed{}, err
	}
	if h := d.opts.Home; h != "" && len(h)+1+len(InstanceName(spec.ID))+len(sockSuffix) > maxSocketPath {
		return driver.Observed{}, fmt.Errorf("%w: instance id %q is too long for Lima's socket paths under %s", driver.ErrUnsupported, spec.ID, h)
	}
	if o, busy, err := d.pending(spec.ID); busy || err != nil {
		return o, err
	}
	in, found, err := d.find(ctx, spec.ID)
	if err != nil {
		return driver.Observed{}, err
	}
	switch {
	case !found:
		return d.background(spec.ID, "creating the VM", func(ctx context.Context) error { return d.create(ctx, spec) }), nil
	case in.Status == statusRunning, in.Status != statusStopped && in.HostAgentPID > 0:
		if in.HostAgentPID > 0 && !booted(in) && alive(in.HostAgentPID) {
			// A boot this agent process did not start: a previous one died
			// during `limactl start`, whose host agent lives on. Wait for it
			// in the background, so the next pass comes once the VM is ready
			// rather than at the next reconcile tick.
			return d.background(spec.ID, "booting", func(ctx context.Context) error { return d.awaitBoot(ctx, in) }), nil
		}
		return d.observe(in), nil
	case in.Status == statusStopped && ready(in):
		// Stopped while desired: the host agent died (a hard power-off,
		// S3 finding 5) or the Mac rebooted.
		return d.background(spec.ID, "starting the stopped VM", func(ctx context.Context) error { return d.start(ctx, in.Name) }), nil
	case !ready(in):
		// Stopped or broken before it ever finished booting: nothing to
		// lose, and its disk may lack what the first boot wrote.
		return d.background(spec.ID, "re-creating the "+strings.ToLower(in.Status)+" VM, which never finished booting", func(ctx context.Context) error {
			if _, err := d.run(ctx, true, "delete", "--force", in.Name); err != nil {
				return err
			}
			return d.create(ctx, spec)
		}), nil
	}
	return driver.Observed{}, fmt.Errorf("lima: %s is %s; it ran before, so it is left for an operator", in.Name, in.Status)
}

// Delete implements driver.Driver. A background create or start of the
// instance is canceled first.
func (d *Driver) Delete(ctx context.Context, id string) error {
	if !idRe.MatchString(id) {
		return fmt.Errorf("lima: instance id %q is not valid", id)
	}
	d.mu.Lock()
	o := d.ops[id]
	d.mu.Unlock()
	if o != nil {
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
	}
	in, found, err := d.find(ctx, id)
	if err != nil || !found {
		return err
	}
	if _, err := d.run(ctx, true, "delete", "--force", in.Name); err != nil {
		return err
	}
	d.mu.Lock()
	delete(d.seen, id)
	d.mu.Unlock()
	return nil
}

// List implements driver.Driver: only Kondukter's instances.
func (d *Driver) List(ctx context.Context) ([]driver.Observed, error) {
	all, err := d.list(ctx)
	if err != nil {
		return nil, err
	}
	var out []driver.Observed
	listed := map[string]bool{}
	for _, in := range all {
		id, ok := owner(in)
		if !ok {
			continue
		}
		listed[id] = true
		if o, busy := d.busy(id); busy {
			out = append(out, o)
			continue
		}
		out = append(out, d.observe(in))
	}
	// A create in progress may not be listed by Lima yet; it is on the
	// host all the same, so the reconciler deletes it (canceling the
	// create) rather than forgetting it.
	d.mu.Lock()
	for id, o := range d.ops {
		if !listed[id] && !o.finished() {
			out = append(out, driver.Observed{ID: id, Phase: driver.PhaseProvisioning, Reason: o.what})
		}
	}
	d.mu.Unlock()
	slices.SortFunc(out, func(a, b driver.Observed) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

// owner returns the instance id if in is Kondukter's.
func owner(in instance) (string, bool) {
	id := in.Param[ParamInstance]
	return id, id != "" && in.Name == InstanceName(id)
}

func ready(in instance) bool {
	_, err := os.Stat(filepath.Join(in.Dir, readyMarker))
	return err == nil
}

// booted reports whether the instance's current host agent has reported the
// VM ready, as `limactl start` waits for: the host agent writes one JSON
// event per line to ha.stdout.log, a new file on each start, and one with
// "running": true once the guest booted and the provision scripts ran
// ("degraded" when a non-essential check failed). Lima's own Running status
// comes seconds into the boot, before any of that.
func booted(in instance) bool {
	f, err := os.Open(filepath.Join(in.Dir, "ha.stdout.log"))
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		var ev struct {
			Status struct {
				Running bool `json:"running"`
			} `json:"status"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) == nil && ev.Status.Running {
			return true
		}
	}
	return false
}

// awaitBoot waits until in's host agent reports the VM ready or exits; the
// event that follows brings the pass that reports the outcome.
func (d *Driver) awaitBoot(ctx context.Context, in instance) error {
	t := time.NewTicker(d.opts.PollInterval)
	defer t.Stop()
	for !booted(in) && alive(in.HostAgentPID) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	return nil
}

// alive reports whether process pid exists.
func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// observe maps a listed instance to its Observed phase. A VM is running, and
// has run for the customer, once its host agent reported it ready, even for
// a start begun by an agent process that died: from then on it is never
// re-created automatically.
func (d *Driver) observe(in instance) driver.Observed {
	id, _ := owner(in)
	switch in.Status {
	case statusRunning:
		if !booted(in) {
			// A start in progress that this agent process did not begin
			// (a previous one died during it), or the VM is shutting down.
			return driver.Observed{ID: id, Phase: driver.PhaseProvisioning, Reason: "booting"}
		}
		if !ready(in) {
			_ = d.markReady(in) // logged; the next pass tries again
		}
		return driver.Observed{ID: id, Phase: driver.PhaseRunning, StartedAt: d.startedAt(id, in)}
	case statusStopped:
		if !ready(in) {
			return driver.Observed{ID: id, Phase: driver.PhaseProvisioning, Reason: "stopped before it finished booting; the agent re-creates it"}
		}
		return driver.Observed{ID: id, Phase: driver.PhaseProvisioning, Reason: "stopped; the agent starts it again"}
	}
	if in.HostAgentPID > 0 {
		// Between the host agent starting and the VM running, Lima reports
		// "host agent is running but driver is not" as Broken; that is a
		// start in progress, perhaps one a previous agent process began.
		return driver.Observed{ID: id, Phase: driver.PhaseProvisioning, Reason: "starting; lima reports the VM as " + in.Status + " while its host agent runs"}
	}
	return driver.Observed{ID: id, Phase: driver.PhaseFailed, Reason: "lima reports the VM as " + in.Status}
}

// startedAt is when the VM's host agent started (the mtime of ha.pid,
// which survives an agent restart), else when the agent first saw it run.
func (d *Driver) startedAt(id string, in instance) time.Time {
	if st, err := os.Stat(filepath.Join(in.Dir, "ha.pid")); err == nil {
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

// pending reports a background operation on id: still running (busy), or
// finished with an error, which is returned once so the next pass retries.
func (d *Driver) pending(id string) (driver.Observed, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	o := d.ops[id]
	if o == nil {
		return driver.Observed{}, false, nil
	}
	select {
	case <-o.done:
		delete(d.ops, id)
		return driver.Observed{}, false, o.err
	default:
		return driver.Observed{ID: id, Phase: driver.PhaseProvisioning, Reason: o.what}, true, nil
	}
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

func (o *op) finished() bool {
	select {
	case <-o.done:
		return true
	default:
		return false
	}
}

// background runs fn for id unless an operation is already running, and
// fires an event when it ends. It is not tied to the reconcile pass: a
// start outlives the pass that asked for it.
func (d *Driver) background(id, what string, fn func(context.Context) error) driver.Observed {
	d.mu.Lock()
	defer d.mu.Unlock()
	if o := d.ops[id]; o != nil {
		return driver.Observed{ID: id, Phase: driver.PhaseProvisioning, Reason: o.what}
	}
	ctx, cancel := context.WithCancel(context.Background())
	o := &op{what: what, cancel: cancel, done: make(chan struct{})}
	d.ops[id] = o
	d.opts.Log.Info("lima: "+what, "instance", id)
	go func() {
		defer cancel()
		o.err = fn(ctx)
		if o.err != nil && !errors.Is(ctx.Err(), context.Canceled) {
			d.opts.Log.Warn("lima: "+what+" failed", "instance", id, "err", o.err)
		}
		close(o.done)
		select {
		case d.events <- driver.Event{InstanceID: id, At: d.opts.Now()}:
		default:
		}
	}()
	return driver.Observed{ID: id, Phase: driver.PhaseProvisioning, Reason: what}
}

// create renders the template, creates the instance and starts it.
func (d *Driver) create(ctx context.Context, spec driver.InstanceSpec) error {
	tmpl, err := Render(spec, d.opts.Arch)
	if err != nil {
		return err
	}
	dir := d.opts.StateDir
	if dir == "" {
		dir = os.TempDir()
	}
	f, err := os.CreateTemp(dir, InstanceName(spec.ID)+"-*.yaml")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.Write(tmpl); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	name := InstanceName(spec.ID)
	if _, err := d.run(ctx, true, "create", "--name", name, f.Name()); err != nil {
		return err
	}
	return d.start(ctx, name)
}

// start boots the instance and waits until Lima reports it ready (guest
// booted, provision scripts done), then marks it as having run.
func (d *Driver) start(ctx context.Context, name string) error {
	if _, err := d.run(ctx, true, "start", name); err != nil {
		return err
	}
	in, found, err := d.find(ctx, name[len(driver.NamePrefix):])
	if err != nil || !found {
		return errors.Join(err, fmt.Errorf("lima: %s vanished after start", name))
	}
	return d.markReady(in)
}

// markReady records that the instance has run.
func (d *Driver) markReady(in instance) error {
	err := os.WriteFile(filepath.Join(in.Dir, readyMarker), []byte(d.opts.Now().UTC().Format(time.RFC3339)+"\n"), 0o600)
	if err != nil {
		d.opts.Log.Warn("lima: cannot mark the instance as started", "instance", in.Name, "err", err)
	}
	return err
}

// find returns the Kondukter instance for id. A "kdk-<id>" instance that is
// not Kondukter's is an error: the driver must not create over it.
func (d *Driver) find(ctx context.Context, id string) (instance, bool, error) {
	all, err := d.list(ctx)
	if err != nil {
		return instance{}, false, err
	}
	for _, in := range all {
		if in.Name != InstanceName(id) {
			continue
		}
		if got, ok := owner(in); !ok || got != id {
			return instance{}, false, fmt.Errorf("lima: %s exists but was not created by Kondukter; it is left alone", in.Name)
		}
		return in, true, nil
	}
	return instance{}, false, nil
}

func (d *Driver) list(ctx context.Context) ([]instance, error) {
	out, err := d.run(ctx, false, "list", "--json")
	if err != nil {
		return nil, err
	}
	return parseList(out)
}

// run runs limactl, holding the host-wide mutation lock for mutations.
func (d *Driver) run(ctx context.Context, mutation bool, args ...string) ([]byte, error) {
	if mutation {
		d.mutate.Lock()
		defer d.mutate.Unlock()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	return d.opts.Run(ctx, args...)
}
