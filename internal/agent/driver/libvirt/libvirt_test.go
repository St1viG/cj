package libvirt_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	golibvirt "github.com/digitalocean/go-libvirt"
	"github.com/digitalocean/go-libvirt/libvirttest"

	"github.com/St1viG/cj/internal/agent/driver"
	"github.com/St1viG/cj/internal/agent/driver/libvirt"
)

const (
	webID   = "0f8fad5b-d9cb-469f-a165-70867728950e"
	otherID = "7c9e6679-7425-40de-944b-e07fc1f90ae7"
)

// imageBytes stand in for a cloud image.
var imageBytes = []byte("QFI\xfb fake qcow2 image for the libvirt driver tests\n")

// env is a driver on a fake libvirt, an image server and a fake qemu-img.
type env struct {
	t      *testing.T
	f      *fakeLibvirt
	d      *libvirt.Driver
	data   string
	pidDir string
	image  driver.ImageRef
	hits   atomic.Int32

	mu sync.Mutex
	// handler, when set, serves the image instead.
	handler func(w http.ResponseWriter, r *http.Request)
	// runGate, when set, holds qemu-img until it is closed.
	runGate chan struct{}
	runs    [][]string
	accs    []driver.Accelerator
}

func newEnv(t *testing.T, opts ...func(*libvirt.Options)) *env {
	t.Helper()
	e := &env{t: t, data: t.TempDir(), pidDir: t.TempDir()}
	e.f = newFakeLibvirt(e.pidDir)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.hits.Add(1)
		e.mu.Lock()
		h := e.handler
		e.mu.Unlock()
		if h != nil {
			h(w, r)
			return
		}
		_, _ = w.Write(imageBytes)
	}))
	t.Cleanup(srv.Close)
	sum := sha256.Sum256(imageBytes)
	e.image = driver.ImageRef{
		Name: "ubuntu-cpu", Version: "24.04-20261017", Arch: "amd64", Format: "qcow2",
		URL: srv.URL + "/ubuntu-24.04-server-cloudimg-amd64.img", Digest: "sha256:" + hex.EncodeToString(sum[:]),
		SizeBytes: int64(len(imageBytes)),
	}
	o := libvirt.Options{
		Dial:    e.f.dial,
		DataDir: e.data,
		Network: libvirt.Network{Bridge: "kdk-br0", Subnet: netip.MustParsePrefix("10.77.0.0/24")},
		Run:     e.run,
		HTTP:    srv.Client(),
		Accelerators: func() ([]driver.Accelerator, error) {
			e.mu.Lock()
			defer e.mu.Unlock()
			return slices.Clone(e.accs), nil
		},
		PidDir: e.pidDir,
		Arch:   "amd64",
	}
	for _, f := range opts {
		f(&o)
	}
	d, err := libvirt.New(o)
	if err != nil {
		t.Fatal(err)
	}
	e.d = d
	t.Cleanup(func() { _ = d.Close() })
	return e
}

// run is qemu-img: it writes the file named before the size.
func (e *env) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	e.mu.Lock()
	e.runs = append(e.runs, append([]string{name}, args...))
	gate := e.runGate
	e.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, os.WriteFile(args[len(args)-2], []byte("QFI\xfb overlay"), 0o644)
}

func (e *env) runCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.runs)
}

func (e *env) spec(id string) driver.InstanceSpec {
	return driver.InstanceSpec{
		ID: id, Generation: 3, VCPU: 2, RAMMiB: 2048, DiskGiB: 20,
		Image:    e.image,
		SSHKeys:  []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyOne demo@mac-p"},
		Hostname: "web-1",
	}
}

func (e *env) instanceDir(id string) string { return filepath.Join(e.data, "instances", id) }

func (e *env) imagePath() string {
	return filepath.Join(e.data, "images", "ubuntu-cpu-24.04-20261017.qcow2")
}

// ensureUntil calls Ensure, as reconcile passes do, until the phase is want.
func (e *env) ensureUntil(spec driver.InstanceSpec, want driver.Phase) driver.Observed {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		o, err := e.d.Ensure(context.Background(), spec)
		if err != nil {
			e.t.Fatalf("Ensure: %v", err)
		}
		if o.Phase == want {
			return o
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("Ensure: still %s (%s), want %s", o.Phase, o.Reason, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// ensureErr calls Ensure until a background operation's error comes back.
func (e *env) ensureErr(spec driver.InstanceSpec) error {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		o, err := e.d.Ensure(context.Background(), spec)
		if err != nil {
			return err
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("Ensure: still %s (%s), want an error", o.Phase, o.Reason)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (e *env) list() []driver.Observed {
	e.t.Helper()
	l, err := e.d.List(context.Background())
	if err != nil {
		e.t.Fatalf("List: %v", err)
	}
	return l
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return err == nil
}

func TestNewDoesNotConnect(t *testing.T) {
	e := newEnv(t)
	if n := e.f.dialCount(); n != 0 {
		t.Fatalf("New dialed libvirt %d times", n)
	}
	if e.d.Name() != "libvirt" {
		t.Fatalf("Name = %q", e.d.Name())
	}
	if _, err := libvirt.New(libvirt.Options{DataDir: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "network") {
		t.Fatalf("New without a bridge: %v", err)
	}
	if _, err := libvirt.New(libvirt.Options{Network: libvirt.Network{Bridge: "kdk-br0", Subnet: netip.MustParsePrefix("10.77.0.0/24")}}); err == nil {
		t.Fatal("New without a data directory succeeded")
	}
}

func TestEnsureCreatesInBackgroundAndIsIdempotent(t *testing.T) {
	e := newEnv(t)
	spec := e.spec(webID)
	o, err := e.d.Ensure(context.Background(), spec)
	if err != nil || o.Phase != driver.PhaseProvisioning || o.Reason != "creating the VM" {
		t.Fatalf("first Ensure = %+v, %v; want provisioning while it creates", o, err)
	}
	got := e.ensureUntil(spec, driver.PhaseRunning)
	if !slices.Equal(got.GuestIPs, []string{"10.77.0.10"}) {
		t.Fatalf("GuestIPs = %v, want the IPv4 lease only", got.GuestIPs)
	}
	pid, err := os.Stat(filepath.Join(e.pidDir, "kdk-"+webID+".pid"))
	if err != nil {
		t.Fatal(err)
	}
	if !got.StartedAt.Equal(pid.ModTime()) {
		t.Fatalf("StartedAt = %v, want the pid file's mtime %v", got.StartedAt, pid.ModTime())
	}

	// Ensure again, twice: the same instance and no libvirt mutation.
	for range 2 {
		again, err := e.d.Ensure(context.Background(), spec)
		if err != nil || !reflect.DeepEqual(again, got) {
			t.Fatalf("Ensure again = %+v, %v; want %+v", again, err, got)
		}
	}
	if n := e.f.count("DomainDefineXMLFlags"); n != 1 {
		t.Fatalf("defined %d times", n)
	}
	if n := e.f.count("DomainCreateWithFlags"); n != 1 {
		t.Fatalf("started %d times", n)
	}
	if n := e.hits.Load(); n != 1 {
		t.Fatalf("image fetched %d times", n)
	}
	if l := e.list(); !reflect.DeepEqual(l, []driver.Observed{got}) {
		t.Fatalf("List = %+v", l)
	}

	// Files: the image in the cache, the overlay over it, the seed, NVRAM.
	dir := e.instanceDir(webID)
	if !exists(t, e.imagePath()) || !exists(t, e.imagePath()+".json") {
		t.Fatal("the image is not in the cache with its sidecar")
	}
	for _, f := range []string{"disk.qcow2", "seed.iso", "nvram"} {
		st, err := os.Stat(filepath.Join(dir, f))
		if err != nil {
			t.Fatal(err)
		}
		if f != "nvram" && st.Mode().Perm() != 0o600 {
			t.Fatalf("%s has mode %v, want 0600", f, st.Mode().Perm())
		}
	}
	if st, _ := os.Stat(e.imagePath()); st.Mode().Perm() != 0o644 {
		t.Fatalf("image has mode %v, want 0644 so QEMU's user reads it", st.Mode().Perm())
	}
	for _, d := range []string{e.data, filepath.Join(e.data, "instances"), dir, filepath.Join(e.data, "images")} {
		if st, _ := os.Stat(d); st.Mode().Perm()&0o011 != 0o011 {
			t.Fatalf("%s has mode %v; QEMU's user cannot reach its files", d, st.Mode().Perm())
		}
	}
	wantRun := []string{"qemu-img", "create", "-f", "qcow2", "-b", e.imagePath(), "-F", "qcow2", filepath.Join(dir, "disk.qcow2.tmp"), "20G"}
	if e.runCount() != 1 || !slices.Equal(e.runs[0], wantRun) {
		t.Fatalf("qemu-img runs = %q, want one %q", e.runs, wantRun)
	}

	// The definition names those files and carries the ownership element.
	dom, _ := e.f.domain("kdk-" + webID)
	for _, s := range []string{
		"<kdk:id>" + webID + "</kdk:id>", "<kdk:generation>3</kdk:generation>",
		"<source file='" + filepath.Join(dir, "disk.qcow2") + "'/>",
		"<source file='" + filepath.Join(dir, "seed.iso") + "'/>",
		"<nvram>" + filepath.Join(dir, "nvram") + "</nvram>",
		"<source network='kdk-net'/>", "<mac address='" + libvirt.MAC(webID) + "'/>",
		"<memory unit='MiB'>2048</memory>", "<vcpu placement='static'>2</vcpu>",
	} {
		if !strings.Contains(dom.xml, s) {
			t.Errorf("domain XML lacks %s", s)
		}
	}
	if strings.Contains(dom.xml, "<hostdev") {
		t.Error("a CPU instance got a hostdev")
	}

	// The guest network: defined once, autostarted, active.
	n, ok := e.f.network(libvirt.DefaultNetwork)
	if !ok || !n.active || !n.autostart || e.f.count("NetworkDefineXML") != 1 {
		t.Fatalf("network = %+v (%v), defined %d times", n, ok, e.f.count("NetworkDefineXML"))
	}
}

func TestDeleteRemovesEverythingAndIsIdempotent(t *testing.T) {
	e := newEnv(t)
	e.ensureUntil(e.spec(webID), driver.PhaseRunning)
	dir := e.instanceDir(webID)
	if !exists(t, filepath.Join(dir, "nvram")) {
		t.Fatal("no NVRAM after the first start")
	}
	for range 2 {
		if err := e.d.Delete(context.Background(), webID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
	}
	if _, ok := e.f.domain("kdk-" + webID); ok {
		t.Fatal("the domain is still defined")
	}
	for _, p := range []string{dir, filepath.Join(e.pidDir, "kdk-"+webID+".pid")} {
		if exists(t, p) {
			t.Fatalf("%s is left after Delete", p)
		}
	}
	if l := e.list(); len(l) != 0 {
		t.Fatalf("List after Delete = %+v", l)
	}
	if !exists(t, e.imagePath()) {
		t.Fatal("Delete removed the shared image")
	}
	if a, b := e.f.count("DomainDestroyFlags"), e.f.count("DomainUndefineFlags"); a != 1 || b != 1 {
		t.Fatalf("destroyed %d times, undefined %d times", a, b)
	}

	// A stopped domain: undefined without destroying (libvirt refuses).
	e.ensureUntil(e.spec(webID), driver.PhaseRunning)
	e.f.stop("kdk-"+webID, golibvirt.DomainShutoff, int32(golibvirt.DomainShutoffShutdown))
	if err := e.d.Delete(context.Background(), webID); err != nil {
		t.Fatalf("Delete of a stopped domain: %v", err)
	}
	if _, ok := e.f.domain("kdk-" + webID); ok || exists(t, dir) {
		t.Fatal("the stopped domain or its files are left")
	}
}

func TestForeignDomainsAreNeverListedOrTouched(t *testing.T) {
	e := newEnv(t)
	running := golibvirt.DomainRunning
	e.f.add("web-prod", "", running, 1)
	e.f.add("kdk-legacy", "", running, 1) // the prefix, no metadata
	e.f.add("kdk-"+otherID, ownMeta("someone-else"), running, 1)
	e.f.add("kdk-mangled", "<kdk:instance xmlns:kdk='"+libvirt.MetadataNS+"'><kdk:id>", running, 1)
	e.f.add("kdk-bad_name", ownMeta("bad_name"), running, 1)
	if err := os.MkdirAll(e.instanceDir("legacy"), 0o700); err != nil {
		t.Fatal(err)
	}
	before := e.f.snapshot()

	if l := e.list(); len(l) != 0 {
		t.Fatalf("List = %+v, want none of the foreign domains", l)
	}
	for _, id := range []string{"legacy", otherID, "mangled"} {
		_, err := e.d.Ensure(context.Background(), e.spec(id))
		if err == nil || !strings.Contains(err.Error(), "not created by Kondukter") {
			t.Errorf("Ensure(%s) = %v, want a refusal", id, err)
		}
		if err := e.d.Delete(context.Background(), id); err == nil || !strings.Contains(err.Error(), "not created by Kondukter") {
			t.Errorf("Delete(%s) = %v, want a refusal", id, err)
		}
	}
	if !exists(t, e.instanceDir("legacy")) {
		t.Fatal("a refused Delete removed files")
	}
	if n := e.f.mutations(); n != 0 {
		t.Fatalf("%d domain mutations while refusing", n)
	}

	// Kondukter's own instance beside them is the only one listed, and
	// creating and deleting it leaves them alone.
	got := e.ensureUntil(e.spec(webID), driver.PhaseRunning)
	if l := e.list(); !reflect.DeepEqual(l, []driver.Observed{got}) {
		t.Fatalf("List = %+v, want only %s", l, webID)
	}
	if err := e.d.Delete(context.Background(), webID); err != nil {
		t.Fatal(err)
	}
	if after := e.f.snapshot(); !reflect.DeepEqual(after, before) {
		t.Fatalf("foreign domains changed:\nbefore %v\nafter  %v", before, after)
	}
}

func TestStoppedOrCrashedDomainIsStartedAgain(t *testing.T) {
	e := newEnv(t)
	spec := e.spec(webID)
	e.ensureUntil(spec, driver.PhaseRunning)
	name := "kdk-" + webID
	for i, tc := range []struct {
		state  golibvirt.DomainState
		reason int32
		listed string
	}{
		{golibvirt.DomainShutoff, int32(golibvirt.DomainShutoffShutdown), "stopped; the agent starts it again"},
		{golibvirt.DomainShutoff, int32(golibvirt.DomainShutoffCrashed), "crashed; the agent starts it again"},
		{golibvirt.DomainCrashed, int32(golibvirt.DomainCrashedPanicked), "crashed; the agent starts it again"},
	} {
		e.f.stop(name, tc.state, tc.reason)
		if l := e.list(); len(l) != 1 || l[0].Phase != driver.PhaseProvisioning || l[0].Reason != tc.listed {
			t.Fatalf("List = %+v, want provisioning %q", l, tc.listed)
		}
		o, err := e.d.Ensure(context.Background(), spec)
		if err != nil || o.Phase != driver.PhaseProvisioning || o.Reason != "starting the stopped VM" {
			t.Fatalf("Ensure = %+v, %v", o, err)
		}
		e.ensureUntil(spec, driver.PhaseRunning)
		if n := e.f.count("DomainCreateWithFlags"); n != i+2 {
			t.Fatalf("started %d times", n)
		}
	}
	if n := e.f.count("DomainDefineXMLFlags"); n != 1 {
		t.Fatalf("defined %d times; a stopped domain is started, not redefined", n)
	}
}

func TestObservedPhases(t *testing.T) {
	e := newEnv(t)
	for _, tc := range []struct {
		id     string
		state  golibvirt.DomainState
		reason int32
		ip     string
		phase  driver.Phase
		why    string
	}{
		{"booting", golibvirt.DomainRunning, 1, "", driver.PhaseProvisioning, "booting; waiting for the guest's DHCP lease"},
		{"blocked", golibvirt.DomainBlocked, 0, "10.77.0.20", driver.PhaseRunning, ""},
		{"starting", golibvirt.DomainPaused, int32(golibvirt.DomainPausedStartingUp), "", driver.PhaseProvisioning, "starting"},
		{"ioerror", golibvirt.DomainPaused, int32(golibvirt.DomainPausedIoerror), "", driver.PhaseFailed, "paused after an I/O error (is the disk full?)"},
		{"paused", golibvirt.DomainPaused, int32(golibvirt.DomainPausedUser), "", driver.PhaseFailed, "paused (libvirt reason 1); left for an operator"},
		{"shutdown", golibvirt.DomainShutdown, int32(golibvirt.DomainShutdownUser), "", driver.PhaseProvisioning, "shutting down; the agent starts it again"},
		{"suspended", golibvirt.DomainPmsuspended, 0, "", driver.PhaseFailed, "suspended by the guest; left for an operator"},
	} {
		e.f.add("kdk-"+tc.id, ownMeta(tc.id), tc.state, tc.reason)
		e.f.update("kdk-"+tc.id, func(d *fakeDom) { d.ip = tc.ip })
		if tc.state == golibvirt.DomainBlocked {
			// Blocked is running, so it reports a lease too.
			e.f.update("kdk-"+tc.id, func(d *fakeDom) { d.state = golibvirt.DomainRunning })
		}
		t.Run(tc.id, func(t *testing.T) {
			l, err := e.d.List(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			i := slices.IndexFunc(l, func(o driver.Observed) bool { return o.ID == tc.id })
			if i < 0 || l[i].Phase != tc.phase || l[i].Reason != tc.why {
				t.Fatalf("List = %+v, want %s %s %q", l, tc.id, tc.phase, tc.why)
			}
		})
	}
}

func TestGuestIPFromTheNetworksLeases(t *testing.T) {
	e := newEnv(t)
	e.f.set(func(f *fakeLibvirt) { f.noDomainLeases = true })
	o := e.ensureUntil(e.spec(webID), driver.PhaseRunning)
	if !slices.Equal(o.GuestIPs, []string{"10.77.0.10"}) || e.f.count("NetworkGetDhcpLeases") == 0 {
		t.Fatalf("GuestIPs = %v from %d network lease calls", o.GuestIPs, e.f.count("NetworkGetDhcpLeases"))
	}
}

func TestStartedAtWithoutAPidFile(t *testing.T) {
	now := time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	e := newEnv(t, func(o *libvirt.Options) { o.Now = clock })
	e.f.set(func(f *fakeLibvirt) { f.noPid = true })
	spec := e.spec(webID)
	o := e.ensureUntil(spec, driver.PhaseRunning)
	mu.Lock()
	first := now
	now = now.Add(time.Minute)
	mu.Unlock()
	again := e.ensureUntil(spec, driver.PhaseRunning)
	if !o.StartedAt.Equal(first) || !again.StartedAt.Equal(first) {
		t.Fatalf("StartedAt = %v then %v, want when it was first seen running, %v", o.StartedAt, again.StartedAt, first)
	}
	// Stopped and started again: a new start time.
	e.f.stop("kdk-"+webID, golibvirt.DomainShutoff, int32(golibvirt.DomainShutoffShutdown))
	e.ensureUntil(spec, driver.PhaseProvisioning)
	if o := e.ensureUntil(spec, driver.PhaseRunning); !o.StartedAt.Equal(first.Add(time.Minute)) {
		t.Fatalf("StartedAt after a restart = %v", o.StartedAt)
	}
}

func TestLeftoverDirectoryIsListedAndDeleted(t *testing.T) {
	e := newEnv(t)
	dir := e.instanceDir(webID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "disk.qcow2"), []byte("QFI\xfb"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Not instances: a name that is no id, and a file.
	if err := os.MkdirAll(e.instanceDir("not_an_id"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.data, "instances", "notes"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	want := []driver.Observed{{ID: webID, Phase: driver.PhaseProvisioning, Reason: "disks left by an interrupted create or delete"}}
	if l := e.list(); !reflect.DeepEqual(l, want) {
		t.Fatalf("List = %+v, want %+v", l, want)
	}
	if err := e.d.Delete(context.Background(), webID); err != nil {
		t.Fatal(err)
	}
	if exists(t, dir) {
		t.Fatal("the leftover directory is still there")
	}
	if l := e.list(); len(l) != 0 {
		t.Fatalf("List = %+v", l)
	}
}

func TestDeleteCancelsACreateInProgress(t *testing.T) {
	e := newEnv(t)
	gate := make(chan struct{})
	e.mu.Lock()
	e.runGate = gate
	e.mu.Unlock()
	spec := e.spec(webID)
	if o, err := e.d.Ensure(context.Background(), spec); err != nil || o.Phase != driver.PhaseProvisioning {
		t.Fatalf("Ensure = %+v, %v", o, err)
	}
	eventually(t, "qemu-img to run", func() bool { return e.runCount() == 1 })
	want := []driver.Observed{{ID: webID, Phase: driver.PhaseProvisioning, Reason: "creating the VM"}}
	if l := e.list(); !reflect.DeepEqual(l, want) {
		t.Fatalf("List during the create = %+v, want %+v", l, want)
	}
	if o, err := e.d.Ensure(context.Background(), spec); err != nil || !reflect.DeepEqual(o, want[0]) {
		t.Fatalf("Ensure during the create = %+v, %v", o, err)
	}
	if err := e.d.Delete(context.Background(), webID); err != nil {
		t.Fatal(err)
	}
	if e.f.count("DomainDefineXMLFlags") != 0 || exists(t, e.instanceDir(webID)) {
		t.Fatal("the canceled create left a domain or files")
	}
	if l := e.list(); len(l) != 0 {
		t.Fatalf("List after Delete = %+v", l)
	}
	if e.runCount() != 1 {
		t.Fatalf("qemu-img ran %d times", e.runCount())
	}

	// The next Ensure creates it afresh.
	e.mu.Lock()
	e.runGate = nil
	e.mu.Unlock()
	close(gate)
	e.ensureUntil(spec, driver.PhaseRunning)
}

func TestFailedCreateIsReportedOnceThenRetried(t *testing.T) {
	e := newEnv(t)
	e.mu.Lock()
	e.handler = func(w http.ResponseWriter, _ *http.Request) {
		// The first download is corrupted: the right length, one bit off.
		e.mu.Lock()
		e.handler = nil
		e.mu.Unlock()
		corrupted := slices.Clone(imageBytes)
		corrupted[8] ^= 1
		_, _ = w.Write(corrupted)
	}
	e.mu.Unlock()
	spec := e.spec(webID)
	err := e.ensureErr(spec)
	if err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("Ensure = %v, want the digest mismatch", err)
	}
	if exists(t, e.imagePath()) || e.f.count("DomainDefineXMLFlags") != 0 {
		t.Fatal("a corrupted image was cached or a domain defined")
	}
	if o, err := e.d.Ensure(context.Background(), spec); err != nil || o.Phase != driver.PhaseProvisioning {
		t.Fatalf("Ensure after the error = %+v, %v; want a retry", o, err)
	}
	e.ensureUntil(spec, driver.PhaseRunning)
	if n := e.hits.Load(); n != 2 {
		t.Fatalf("image fetched %d times, want 2", n)
	}
}

func TestHostIsFull(t *testing.T) {
	e := newEnv(t, func(o *libvirt.Options) { o.MaxInstances = 1 })
	gate := make(chan struct{})
	e.mu.Lock()
	e.runGate = gate
	e.mu.Unlock()
	e.ensureUntil(e.spec(webID), driver.PhaseProvisioning)
	eventually(t, "qemu-img to run", func() bool { return e.runCount() == 1 })
	// A create whose domain is not defined yet counts.
	if _, err := e.d.Ensure(context.Background(), e.spec(otherID)); err == nil || !strings.Contains(err.Error(), "host is full") {
		t.Fatalf("Ensure of a second instance during a create = %v", err)
	}
	e.mu.Lock()
	e.runGate = nil
	e.mu.Unlock()
	close(gate)
	e.ensureUntil(e.spec(webID), driver.PhaseRunning)
	if _, err := e.d.Ensure(context.Background(), e.spec(otherID)); err == nil || !strings.Contains(err.Error(), "host is full") {
		t.Fatalf("Ensure of a second instance = %v", err)
	}
	if err := e.d.Delete(context.Background(), webID); err != nil {
		t.Fatal(err)
	}
	e.ensureUntil(e.spec(otherID), driver.PhaseRunning)
}

// hostdevDoc is the hostdev part of a domain definition.
type hostdevDoc struct {
	Hostdevs []struct {
		Mode    string `xml:"mode,attr"`
		Type    string `xml:"type,attr"`
		Managed string `xml:"managed,attr"`
		Address struct {
			Domain   string `xml:"domain,attr"`
			Bus      string `xml:"bus,attr"`
			Slot     string `xml:"slot,attr"`
			Function string `xml:"function,attr"`
		} `xml:"source>address"`
	} `xml:"devices>hostdev"`
}

func TestAcceleratorsArePassedThrough(t *testing.T) {
	e := newEnv(t)
	e.accs = []driver.Accelerator{
		{ID: "0000:01:00.0", Kind: "nvidia", Model: "GeForce GTX 1060 6GB", PCIAddress: "0000:01:00.0", Health: driver.HealthOK, Companions: []string{"0000:01:00.1"}},
		{ID: "0000:02:00.0", Kind: "nvidia", PCIAddress: "0000:02:00.0", Health: driver.HealthUnavailable, HealthReason: "bound to nvidia, not vfio-pci"},
	}
	caps, err := e.d.Capabilities(context.Background())
	if err != nil || !caps.GPUPassthrough || !slices.Equal(caps.AcceleratorKinds, []string{"nvidia"}) || caps.Arch != "amd64" {
		t.Fatalf("Capabilities = %+v, %v", caps, err)
	}

	spec := e.spec(webID)
	spec.AcceleratorIDs = []string{"0000:01:00.0"}
	e.ensureUntil(spec, driver.PhaseRunning)
	dom, _ := e.f.domain("kdk-" + webID)
	var doc hostdevDoc
	if err := xml.Unmarshal([]byte(dom.xml), &doc); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, h := range doc.Hostdevs {
		if h.Mode != "subsystem" || h.Type != "pci" || h.Managed != "yes" {
			t.Fatalf("hostdev %+v is not a managed PCI passthrough", h)
		}
		a := h.Address
		got = append(got, a.Domain+":"+a.Bus+":"+a.Slot+"."+a.Function)
	}
	if want := []string{"0x0000:0x01:0x00.0x0", "0x0000:0x01:0x00.0x1"}; !slices.Equal(got, want) {
		t.Fatalf("hostdevs = %v, want the GPU and its companion %v", got, want)
	}

	other := e.spec(otherID)
	other.AcceleratorIDs = []string{"0000:02:00.0"}
	if _, err := e.d.Ensure(context.Background(), other); err == nil || !strings.Contains(err.Error(), "bound to nvidia") {
		t.Fatalf("Ensure with an unavailable GPU = %v", err)
	}
	other.AcceleratorIDs = []string{"0000:03:00.0"}
	if _, err := e.d.Ensure(context.Background(), other); !errors.Is(err, driver.ErrUnsupported) {
		t.Fatalf("Ensure with an unknown GPU = %v, want ErrUnsupported", err)
	}
	if _, ok := e.f.domain("kdk-" + otherID); ok {
		t.Fatal("a refused instance was defined")
	}
}

func TestEnsureValidates(t *testing.T) {
	e := newEnv(t, func(o *libvirt.Options) { o.Arch = "arm64" })
	if _, err := e.d.Ensure(context.Background(), e.spec(webID)); !errors.Is(err, driver.ErrUnsupported) {
		t.Fatalf("Ensure on arm64 = %v, want ErrUnsupported", err)
	}
	if e.f.dialCount() != 0 {
		t.Fatal("an invalid spec reached libvirt")
	}
}

func TestNetworkIsDefinedOrChecked(t *testing.T) {
	good := libvirt.Network{Bridge: "kdk-br0", Subnet: netip.MustParsePrefix("10.77.0.0/24")}
	render := func(n libvirt.Network) string {
		x, err := libvirt.RenderNetwork(n)
		if err != nil {
			t.Fatal(err)
		}
		return x
	}
	for _, tc := range []struct {
		name   string
		xml    string
		active bool
		err    string
	}{
		{"inactive", render(good), false, ""},
		{"netmask", strings.Replace(render(good), "prefix='24'", "netmask='255.255.255.0'", 1), true, ""},
		{"nat mode", strings.Replace(render(good), "mode='open'", "mode='nat'", 1), true, ""},
		{"other bridge", render(libvirt.Network{Bridge: "virbr0", Subnet: good.Subnet}), true, `uses bridge "virbr0"`},
		{"other subnet", render(libvirt.Network{Bridge: "kdk-br0", Subnet: netip.MustParsePrefix("192.168.122.0/24")}), true, "has no address 10.77.0.1/24"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.f.addNetwork(libvirt.DefaultNetwork, tc.xml, tc.active)
			if tc.err == "" {
				e.ensureUntil(e.spec(webID), driver.PhaseRunning)
				if n, _ := e.f.network(libvirt.DefaultNetwork); !n.active || n.xml != tc.xml {
					t.Fatalf("network = %+v", n)
				}
			} else if err := e.ensureErr(e.spec(webID)); err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Fatalf("Ensure = %v, want %q", err, tc.err)
			}
			if n := e.f.count("NetworkDefineXML"); n != 0 {
				t.Fatalf("an existing network was redefined %d times", n)
			}
		})
	}
}

func TestEventsFromLifecycleSurviveReconnects(t *testing.T) {
	e := newEnv(t)
	ch := e.d.Events()
	if e.d.Events() != ch {
		t.Fatal("Events returned another channel")
	}
	recv := func() driver.Event {
		t.Helper()
		select {
		case ev := <-ch:
			return ev
		case <-time.After(10 * time.Second):
			t.Fatal("no event")
		}
		return driver.Event{}
	}
	eventually(t, "the subscription", func() bool { return e.f.subscribers() == 1 })
	e.f.emit("web-prod")
	e.f.emit("kdk-" + webID)
	if ev := recv(); ev.InstanceID != webID || ev.At.IsZero() {
		t.Fatalf("event = %+v, want %s (and none for web-prod)", ev, webID)
	}

	// libvirtd restarts: the watcher subscribes again on a new connection.
	e.f.kill()
	eventually(t, "a new subscription", func() bool { return e.f.subscribers() == 1 && e.f.dialCount() == 2 })
	e.f.emit("kdk-" + otherID)
	if ev := recv(); ev.InstanceID != otherID {
		t.Fatalf("event after the restart = %+v", ev)
	}

	if err := e.d.Close(); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the subscription to end", func() bool { return e.f.subscribers() == 0 })
	e.d.Events()
	time.Sleep(20 * time.Millisecond)
	if n := e.f.subscribers(); n != 0 {
		t.Fatalf("Events after Close subscribed again (%d)", n)
	}
}

func TestBackgroundOperationsFireEvents(t *testing.T) {
	e := newEnv(t)
	ch := e.d.Events()
	if _, err := e.d.Ensure(context.Background(), e.spec(webID)); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.InstanceID != webID {
				t.Fatalf("event %+v", ev)
			}
			if o, err := e.d.Ensure(context.Background(), e.spec(webID)); err == nil && o.Phase == driver.PhaseRunning {
				return
			}
		case <-deadline:
			t.Fatal("no event brought the instance to running")
		}
	}
}

func TestReconnectsAfterLibvirtdRestarts(t *testing.T) {
	e := newEnv(t)
	spec := e.spec(webID)
	got := e.ensureUntil(spec, driver.PhaseRunning)
	if n := e.f.dialCount(); n != 1 {
		t.Fatalf("dialed %d times", n)
	}

	// libvirtd restarted between passes: the next call dials again.
	e.f.kill()
	if o, err := e.d.Ensure(context.Background(), spec); err != nil || !reflect.DeepEqual(o, got) {
		t.Fatalf("Ensure after a restart = %+v, %v", o, err)
	}
	if n := e.f.dialCount(); n != 2 {
		t.Fatalf("dialed %d times, want 2", n)
	}

	// The connection drops during a call: the call runs again on a new one.
	e.f.set(func(f *fakeLibvirt) { f.dropOn = "ConnectListAllDomains" })
	if l := e.list(); !reflect.DeepEqual(l, []driver.Observed{got}) {
		t.Fatalf("List across a drop = %+v", l)
	}
	if n := e.f.dialCount(); n != 3 {
		t.Fatalf("dialed %d times, want 3", n)
	}

	// libvirtd is down: a clear error, then the next pass connects.
	e.f.kill()
	e.f.set(func(f *fakeLibvirt) {
		f.dialErr = &net.OpError{Op: "dial", Net: "unix", Err: os.ErrNotExist}
	})
	_, err := e.d.List(context.Background())
	if err == nil || !strings.Contains(err.Error(), "connect to qemu:///system") || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("List with libvirtd down = %v", err)
	}
	e.f.set(func(f *fakeLibvirt) { f.dialErr = nil })
	if l := e.list(); len(l) != 1 {
		t.Fatalf("List after libvirtd is back = %+v", l)
	}
}

// TestGoLibvirtAgainstItsMockServer runs the real RPC client against
// go-libvirt's libvirttest server: it connects, lists (its two canned
// domains are not Kondukter's), and reconnects when the server goes away.
func TestGoLibvirtAgainstItsMockServer(t *testing.T) {
	var (
		mu    sync.Mutex
		mocks []*libvirttest.MockLibvirt
	)
	dial := libvirt.DialWith(func(context.Context) (net.Conn, error) {
		// One mock per connection: a mock serves a single client.
		m := libvirttest.New()
		mu.Lock()
		mocks = append(mocks, m)
		mu.Unlock()
		return m.Dial()
	})
	d, err := libvirt.New(libvirt.Options{
		Dial: dial, DataDir: t.TempDir(), Arch: "amd64",
		Network: libvirt.Network{Bridge: "kdk-br0", Subnet: netip.MustParsePrefix("10.77.0.0/24")},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if l, err := d.List(ctx); err != nil || len(l) != 0 {
		t.Fatalf("List = %+v, %v", l, err)
	}
	mu.Lock()
	first := mocks[0]
	mu.Unlock()
	if err := first.Test.Close(); err != nil {
		t.Fatal(err)
	}
	if l, err := d.List(ctx); err != nil || len(l) != 0 {
		t.Fatalf("List after the server went away = %+v, %v", l, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(mocks) != 2 {
		t.Fatalf("dialed %d times, want 2", len(mocks))
	}
}
