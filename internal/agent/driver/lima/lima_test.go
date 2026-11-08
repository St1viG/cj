package lima_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/St1viG/cj/internal/agent/driver"
	"github.com/St1viG/cj/internal/agent/driver/lima"
)

// fakeLima is limactl's behavior as seen in spike S3, in memory.
type fakeLima struct {
	home string

	mu         sync.Mutex
	insts      map[string]*fakeInst
	calls      []string
	inMut      int
	maxInMut   int
	createGate chan struct{} // nil: create returns at once
	startGate  chan struct{} // nil: start returns at once
	failStart  int           // fail this many starts
}

type fakeInst struct {
	Name   string            `json:"name"`
	Status string            `json:"status"`
	Dir    string            `json:"dir"`
	Param  map[string]string `json:"param,omitempty"`
	PID    int               `json:"hostAgentPID,omitempty"`
}

func newFake(t *testing.T) *fakeLima {
	return &fakeLima{home: t.TempDir(), insts: map[string]*fakeInst{}}
}

func (f *fakeLima) add(name, status string, param map[string]string) *fakeInst {
	in := &fakeInst{Name: name, Status: status, Dir: filepath.Join(f.home, name), Param: param}
	_ = os.MkdirAll(in.Dir, 0o700)
	f.mu.Lock()
	f.insts[name] = in
	f.mu.Unlock()
	return in
}

// hostAgent writes what a host agent leaves in the instance directory: its
// pid file and its event log, new on each start, with the "running" event
// only once the VM is ready.
func hostAgent(t *testing.T, in *fakeInst, ready bool) {
	t.Helper()
	log := `{"time":"2026-11-08T07:24:18Z","status":{"sshLocalPort":54959}}` + "\n"
	if ready {
		log += `{"time":"2026-11-08T07:24:30Z","status":{"running":true,"sshLocalPort":54959}}` + "\n"
	}
	if err := os.WriteFile(filepath.Join(in.Dir, "ha.stdout.log"), []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(in.Dir, "ha.pid"), []byte("4242\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *fakeLima) count(verb string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, verb+" ") {
			n++
		}
	}
	return n
}

func (f *fakeLima) names() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for n := range f.insts {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

func (f *fakeLima) wait(ctx context.Context, gate chan struct{}) error {
	if gate == nil {
		return nil
	}
	select {
	case <-gate:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *fakeLima) run(ctx context.Context, args ...string) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, strings.Join(args, " "))
	mutation := args[0] != "list"
	if mutation {
		f.inMut++
		f.maxInMut = max(f.maxInMut, f.inMut)
	}
	f.mu.Unlock()
	if mutation {
		defer func() { f.mu.Lock(); f.inMut--; f.mu.Unlock() }()
	}
	switch args[0] {
	case "list":
		f.mu.Lock()
		defer f.mu.Unlock()
		var b strings.Builder
		for _, n := range slices.Sorted(func(yield func(string) bool) {
			for n := range f.insts {
				if !yield(n) {
					return
				}
			}
		}) {
			raw, _ := json.Marshal(f.insts[n])
			b.Write(raw)
			b.WriteByte('\n')
		}
		return []byte(b.String()), nil
	case "create": // create --name N FILE
		name, file := args[2], args[3]
		raw, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		var tmpl struct {
			Param map[string]string `yaml:"param"`
		}
		if err := yaml.Unmarshal(raw, &tmpl); err != nil {
			return nil, err
		}
		if err := f.wait(ctx, f.createGate); err != nil {
			return nil, err
		}
		f.mu.Lock()
		_, exists := f.insts[name]
		f.mu.Unlock()
		if exists {
			return nil, fmt.Errorf("instance %q already exists", name)
		}
		f.add(name, "Stopped", tmpl.Param)
	case "start":
		if err := f.wait(ctx, f.startGate); err != nil {
			return nil, err
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		in := f.insts[args[1]]
		if in == nil {
			return nil, errors.New("no such instance")
		}
		if f.failStart > 0 {
			f.failStart--
			return nil, errors.New("did not receive an event with the \"running\" status")
		}
		in.Status = "Running"
		_ = os.WriteFile(filepath.Join(in.Dir, "ha.stdout.log"), []byte(`{"status":{"running":true}}`+"\n"), 0o600)
		_ = os.WriteFile(filepath.Join(in.Dir, "ha.pid"), []byte("4242\n"), 0o600)
	case "delete": // delete --force N
		f.mu.Lock()
		defer f.mu.Unlock()
		if in := f.insts[args[2]]; in != nil {
			_ = os.RemoveAll(in.Dir)
			delete(f.insts, args[2])
		}
	default:
		return nil, fmt.Errorf("fake limactl: %v", args)
	}
	return nil, nil
}

func newDriver(t *testing.T, f *fakeLima) *lima.Driver {
	d, err := lima.New(lima.Options{Run: f.run, StateDir: t.TempDir(), Arch: "arm64", PollInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func spec(id string) driver.InstanceSpec {
	s := webSpec()
	s.ID = id
	return s
}

func waitEvent(t *testing.T, d *lima.Driver, id string) {
	t.Helper()
	select {
	case ev := <-d.Events():
		if ev.InstanceID != id {
			t.Fatalf("event for %s, want %s", ev.InstanceID, id)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("no event for %s", id)
	}
}

func TestEnsureCreatesInBackgroundAndIsIdempotent(t *testing.T) {
	f := newFake(t)
	d := newDriver(t, f)
	ctx := context.Background()

	o, err := d.Ensure(ctx, spec("a"))
	if err != nil || o.Phase != driver.PhaseProvisioning {
		t.Fatalf("first Ensure = %+v, %v", o, err)
	}
	waitEvent(t, d, "a")
	o, err = d.Ensure(ctx, spec("a"))
	if err != nil || o.Phase != driver.PhaseRunning || o.StartedAt.IsZero() {
		t.Fatalf("Ensure after start = %+v, %v", o, err)
	}
	again, err := d.Ensure(ctx, spec("a"))
	if err != nil || again.Phase != driver.PhaseRunning || !again.StartedAt.Equal(o.StartedAt) {
		t.Fatalf("second Ensure = %+v, %v", again, err)
	}
	if f.count("create") != 1 || f.count("start") != 1 {
		t.Fatalf("calls = %v", f.calls)
	}
	if _, err := os.Stat(filepath.Join(f.home, "kdk-a", "kondukter-ready")); err != nil {
		t.Fatalf("ready marker: %v", err)
	}
	list, err := d.List(ctx)
	if err != nil || len(list) != 1 || list[0].ID != "a" || list[0].Phase != driver.PhaseRunning {
		t.Fatalf("List = %+v, %v", list, err)
	}

	for range 2 {
		if err := d.Delete(ctx, "a"); err != nil {
			t.Fatal(err)
		}
	}
	if n := f.names(); len(n) != 0 {
		t.Fatalf("left on host: %v", n)
	}
}

func TestForeignInstancesAreNeverTouched(t *testing.T) {
	f := newFake(t)
	f.add("default", "Running", nil)
	f.add("kdk-x", "Running", nil)                                                   // our prefix, no marker
	f.add("kdk-y", "Stopped", map[string]string{lima.ParamInstance: "someone-else"}) // marker for another id
	d := newDriver(t, f)
	ctx := context.Background()

	if list, err := d.List(ctx); err != nil || len(list) != 0 {
		t.Fatalf("List = %+v, %v", list, err)
	}
	for _, id := range []string{"x", "y"} {
		if _, err := d.Ensure(ctx, spec(id)); err == nil || !strings.Contains(err.Error(), "not created by Kondukter") {
			t.Fatalf("Ensure(%s) err = %v", id, err)
		}
		if err := d.Delete(ctx, id); err == nil {
			t.Fatalf("Delete(%s) succeeded", id)
		}
	}
	if err := d.Delete(ctx, "default"); err != nil { // kdk-default does not exist
		t.Fatal(err)
	}
	if got := f.names(); !slices.Equal(got, []string{"default", "kdk-x", "kdk-y"}) {
		t.Fatalf("host instances = %v", got)
	}
	for _, c := range f.calls {
		if !strings.HasPrefix(c, "list") {
			t.Fatalf("mutation on a foreign instance: %q", c)
		}
	}
}

// A VM whose host agent died shows up Stopped (S3 finding 5): Ensure starts
// the same instance again instead of creating a new one.
func TestStoppedInstanceIsStartedAgain(t *testing.T) {
	f := newFake(t)
	in := f.add("kdk-a", "Stopped", map[string]string{lima.ParamInstance: "a"})
	_ = os.WriteFile(filepath.Join(in.Dir, "kondukter-ready"), nil, 0o600)
	d := newDriver(t, f)
	ctx := context.Background()

	if list, _ := d.List(ctx); len(list) != 1 || list[0].Phase != driver.PhaseProvisioning {
		t.Fatalf("List = %+v", list)
	}
	o, err := d.Ensure(ctx, spec("a"))
	if err != nil || o.Phase != driver.PhaseProvisioning || !strings.Contains(o.Reason, "stopped") {
		t.Fatalf("Ensure = %+v, %v", o, err)
	}
	waitEvent(t, d, "a")
	if o, err := d.Ensure(ctx, spec("a")); err != nil || o.Phase != driver.PhaseRunning {
		t.Fatalf("Ensure = %+v, %v", o, err)
	}
	if f.count("create") != 0 || f.count("start") != 1 {
		t.Fatalf("calls = %v", f.calls)
	}
}

func TestFailedStartIsReportedOnceThenRetried(t *testing.T) {
	f := newFake(t)
	f.failStart = 1
	d := newDriver(t, f)
	ctx := context.Background()

	if _, err := d.Ensure(ctx, spec("a")); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, d, "a")
	if _, err := d.Ensure(ctx, spec("a")); err == nil || !strings.Contains(err.Error(), "running") {
		t.Fatalf("err = %v, want the start failure", err)
	}
	// Created but never booted: the next pass re-creates it.
	o, err := d.Ensure(ctx, spec("a"))
	if err != nil || o.Phase != driver.PhaseProvisioning || !strings.Contains(o.Reason, "re-creating") {
		t.Fatalf("retry = %+v, %v", o, err)
	}
	waitEvent(t, d, "a")
	if o, err := d.Ensure(ctx, spec("a")); err != nil || o.Phase != driver.PhaseRunning {
		t.Fatalf("Ensure = %+v, %v", o, err)
	}
	if f.count("delete") != 1 || f.count("create") != 2 || f.count("start") != 2 {
		t.Fatalf("calls = %v", f.calls)
	}
}

// Deleting an instance whose create is still running cancels the create;
// List reports it meanwhile, though Lima does not list it yet.
func TestDeleteCancelsACreateInProgress(t *testing.T) {
	f := newFake(t)
	f.createGate = make(chan struct{})
	d := newDriver(t, f)
	ctx := context.Background()

	if _, err := d.Ensure(ctx, spec("a")); err != nil {
		t.Fatal(err)
	}
	list, err := d.List(ctx)
	if err != nil || len(list) != 1 || list[0].ID != "a" || list[0].Phase != driver.PhaseProvisioning {
		t.Fatalf("List during create = %+v, %v", list, err)
	}
	if err := d.Delete(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if n := f.names(); len(n) != 0 {
		t.Fatalf("left on host: %v", n)
	}
	if list, _ := d.List(ctx); len(list) != 0 {
		t.Fatalf("List after delete = %+v", list)
	}
}

// A start this agent process did not begin (a previous one died during
// it): Lima says Running seconds into the boot, but the VM is running, and
// marked as having run, only once its host agent reports it ready. The
// driver waits for that and fires an event, so the reconciler does not
// learn of it only at its next tick.
func TestRunningInstanceIsReadyOnceItsHostAgentSaysSo(t *testing.T) {
	f := newFake(t)
	in := f.add("kdk-a", "Running", map[string]string{lima.ParamInstance: "a"})
	in.PID = os.Getpid() // a live host agent
	hostAgent(t, in, false)
	d := newDriver(t, f)
	ctx := context.Background()
	if o, err := d.Ensure(ctx, spec("a")); err != nil || o.Phase != driver.PhaseProvisioning || o.Reason != "booting" {
		t.Fatalf("Ensure while booting = %+v, %v", o, err)
	}
	if list, _ := d.List(ctx); len(list) != 1 || list[0].Phase != driver.PhaseProvisioning {
		t.Fatalf("List while booting = %+v", list)
	}
	if _, err := os.Stat(filepath.Join(in.Dir, "kondukter-ready")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ready marker while booting: %v", err)
	}
	hostAgent(t, in, true)
	waitEvent(t, d, "a")
	if o, err := d.Ensure(ctx, spec("a")); err != nil || o.Phase != driver.PhaseRunning || o.StartedAt.IsZero() {
		t.Fatalf("Ensure once ready = %+v, %v", o, err)
	}
	if _, err := os.Stat(filepath.Join(in.Dir, "kondukter-ready")); err != nil {
		t.Fatalf("ready marker: %v", err)
	}
	if f.count("start") != 0 || f.count("create") != 0 {
		t.Fatalf("calls = %v", f.calls)
	}
}

// Such a boot may also fail. The wait ends when the host agent exits, and
// the VM, which never finished booting, is re-created.
func TestBootWaitEndsWhenTheHostAgentExits(t *testing.T) {
	ha := exec.CommandContext(t.Context(), "sleep", "60")
	if err := ha.Start(); err != nil {
		t.Fatal(err)
	}
	f := newFake(t)
	in := f.add("kdk-a", "Running", map[string]string{lima.ParamInstance: "a"})
	in.PID = ha.Process.Pid
	hostAgent(t, in, false)
	d := newDriver(t, f)
	ctx := context.Background()
	if o, err := d.Ensure(ctx, spec("a")); err != nil || o.Reason != "booting" {
		t.Fatalf("Ensure while booting = %+v, %v", o, err)
	}
	_ = ha.Process.Kill()
	_ = ha.Wait()
	waitEvent(t, d, "a")
	// Should Lima still list the dead pid, no new wait starts, so there is
	// no stream of events.
	if o, err := d.Ensure(ctx, spec("a")); err != nil || o.Phase != driver.PhaseProvisioning || o.Reason != "booting" {
		t.Fatalf("Ensure with a dead host agent = %+v, %v", o, err)
	}
	select {
	case ev := <-d.Events():
		t.Fatalf("event %+v without a wait", ev)
	case <-time.After(50 * time.Millisecond):
	}
	f.mu.Lock()
	in.Status, in.PID = "Stopped", 0
	f.mu.Unlock()
	if o, err := d.Ensure(ctx, spec("a")); err != nil || !strings.Contains(o.Reason, "re-creating the stopped VM") {
		t.Fatalf("Ensure once stopped = %+v, %v", o, err)
	}
	waitEvent(t, d, "a")
	if o, err := d.Ensure(ctx, spec("a")); err != nil || o.Phase != driver.PhaseRunning {
		t.Fatalf("Ensure = %+v, %v", o, err)
	}
	if f.count("delete") != 1 || f.count("create") != 1 || f.count("start") != 1 {
		t.Fatalf("calls = %v", f.calls)
	}
}

// Powered off before its first boot finished, a VM's disk may lack what
// that boot wrote (empty SSH host keys, seen in TestLima): it is re-created,
// not started.
func TestStoppedInstanceThatNeverBootedIsRecreated(t *testing.T) {
	f := newFake(t)
	in := f.add("kdk-a", "Stopped", map[string]string{lima.ParamInstance: "a"})
	hostAgent(t, in, false)
	d := newDriver(t, f)
	ctx := context.Background()
	if list, _ := d.List(ctx); len(list) != 1 || !strings.Contains(list[0].Reason, "re-creates") {
		t.Fatalf("List = %+v", list)
	}
	o, err := d.Ensure(ctx, spec("a"))
	if err != nil || o.Phase != driver.PhaseProvisioning || !strings.Contains(o.Reason, "re-creating the stopped VM") {
		t.Fatalf("Ensure = %+v, %v", o, err)
	}
	waitEvent(t, d, "a")
	if o, err := d.Ensure(ctx, spec("a")); err != nil || o.Phase != driver.PhaseRunning {
		t.Fatalf("Ensure = %+v, %v", o, err)
	}
	if f.count("delete") != 1 || f.count("create") != 1 || f.count("start") != 1 {
		t.Fatalf("calls = %v", f.calls)
	}
}

// Lima reports a VM as Broken between its host agent starting and the VM
// running (seen in TestLima after the agent was killed mid-start): a boot in
// progress, waited for without any limactl mutation, which Delete cancels.
func TestBrokenWhileHostAgentRunsIsAStart(t *testing.T) {
	f := newFake(t)
	in := f.add("kdk-a", "Broken", map[string]string{lima.ParamInstance: "a"})
	in.PID = os.Getpid() // a live host agent
	hostAgent(t, in, false)
	_ = os.WriteFile(filepath.Join(in.Dir, "kondukter-ready"), nil, 0o600)
	d := newDriver(t, f)
	ctx := context.Background()
	o, err := d.Ensure(ctx, spec("a"))
	if err != nil || o.Phase != driver.PhaseProvisioning || o.Reason != "booting" {
		t.Fatalf("Ensure = %+v, %v", o, err)
	}
	if list, _ := d.List(ctx); len(list) != 1 || list[0].Phase != driver.PhaseProvisioning {
		t.Fatalf("List = %+v", list)
	}
	if n := f.count("list"); n != 2 || len(f.calls) != n {
		t.Fatalf("calls = %v", f.calls)
	}
	done := make(chan error, 1)
	go func() { done <- d.Delete(ctx, "a") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Delete did not cancel the wait")
	}
	if f.count("delete") != 1 || len(f.names()) != 0 {
		t.Fatalf("calls = %v, left %v", f.calls, f.names())
	}
}

func TestBrokenInstanceRecreatedOnlyIfItNeverRan(t *testing.T) {
	f := newFake(t)
	f.add("kdk-new", "Broken", map[string]string{lima.ParamInstance: "new"})
	old := f.add("kdk-old", "Broken", map[string]string{lima.ParamInstance: "old"})
	_ = os.WriteFile(filepath.Join(old.Dir, "kondukter-ready"), nil, 0o600)
	d := newDriver(t, f)
	ctx := context.Background()

	if _, err := d.Ensure(ctx, spec("old")); err == nil || !strings.Contains(err.Error(), "Broken") {
		t.Fatalf("Ensure(old) err = %v", err)
	}
	if o, err := d.Ensure(ctx, spec("new")); err != nil || !strings.Contains(o.Reason, "re-creating") {
		t.Fatalf("Ensure(new) = %+v, %v", o, err)
	}
	waitEvent(t, d, "new")
	if o, err := d.Ensure(ctx, spec("new")); err != nil || o.Phase != driver.PhaseRunning {
		t.Fatalf("Ensure(new) = %+v, %v", o, err)
	}
	if f.count("delete") != 1 {
		t.Fatalf("calls = %v", f.calls)
	}
}

func TestHomeLeavesRoomForSockets(t *testing.T) {
	short := "/Users/kdk/.lima"
	if _, err := lima.ResolveHome(short); err != nil {
		t.Fatal(err)
	}
	if _, err := lima.ResolveHome("/private/var/folders/z9/kc1mqvr91w577mp58vs402ww0000gn/T/kl1"); err == nil {
		t.Fatal("a $TMPDIR home was accepted")
	}
	f := newFake(t)
	d, err := lima.New(lima.Options{Run: f.run, StateDir: t.TempDir(), Arch: "arm64", Home: short + strings.Repeat("x", lima.MaxHomeLen-len(short))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Ensure(context.Background(), spec(webSpec().ID)); err != nil {
		t.Fatalf("a UUID id under the longest home: %v", err)
	}
	waitEvent(t, d, webSpec().ID) // the background create is done with StateDir
	if _, err := d.Ensure(context.Background(), spec(webSpec().ID+"-x")); !errors.Is(err, driver.ErrUnsupported) {
		t.Fatalf("a longer id: err = %v", err)
	}
}

func TestOneMutationAtATime(t *testing.T) {
	f := newFake(t)
	f.startGate = make(chan struct{})
	d := newDriver(t, f)
	ctx := context.Background()
	for _, id := range []string{"a", "b", "c"} {
		if _, err := d.Ensure(ctx, spec(id)); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(50 * time.Millisecond)
	close(f.startGate)
	for range 3 {
		select {
		case <-d.Events():
		case <-time.After(2 * time.Second):
			t.Fatal("not every start finished")
		}
	}
	if f.maxInMut != 1 {
		t.Fatalf("%d limactl mutations ran at once", f.maxInMut)
	}
	if list, _ := d.List(ctx); len(list) != 3 {
		t.Fatalf("List = %+v", list)
	}
}
