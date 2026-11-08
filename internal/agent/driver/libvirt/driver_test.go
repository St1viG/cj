package libvirt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/St1viG/cj/internal/agent/driver"
)

// fakeAPI is an in-memory libvirt.
type fakeAPI struct {
	mu        sync.Mutex
	doms      map[string]*fakeDom
	ips       map[string][]string
	calls     []string
	xmlReads  []string // domains whose XML was read
	startErr  error
	defineErr error
	events    chan LifecycleEvent
}

type fakeDom struct {
	xml     string
	running bool
}

func newFake() *fakeAPI {
	return &fakeAPI{doms: map[string]*fakeDom{}, ips: map[string][]string{}, events: make(chan LifecycleEvent, 8)}
}

func (f *fakeAPI) rec(s string) { f.calls = append(f.calls, s) }

func (f *fakeAPI) callList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeAPI) Domains(_ context.Context, prefix string) ([]DomainInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []DomainInfo
	for n, d := range f.doms {
		if strings.HasPrefix(n, prefix) {
			f.xmlReads = append(f.xmlReads, n)
			out = append(out, DomainInfo{Name: n, Running: d.running, XML: d.xml})
		}
	}
	return out, nil
}

func (f *fakeAPI) Domain(_ context.Context, name string) (DomainInfo, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.doms[name]
	if !ok {
		return DomainInfo{}, false, nil
	}
	return DomainInfo{Name: name, Running: d.running, XML: d.xml}, true, nil
}

func nameOf(xml string) string {
	i := strings.Index(xml, "<name>")
	j := strings.Index(xml, "</name>")
	return xml[i+len("<name>") : j]
}

func (f *fakeAPI) Define(_ context.Context, xml string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rec("define")
	if f.defineErr != nil {
		return f.defineErr
	}
	f.doms[nameOf(xml)] = &fakeDom{xml: xml}
	return nil
}

func (f *fakeAPI) Start(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rec("start " + name)
	if f.startErr != nil {
		return f.startErr
	}
	d, ok := f.doms[name]
	if !ok {
		return errors.New("no such domain")
	}
	d.running = true
	return nil
}

func (f *fakeAPI) Destroy(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rec("destroy " + name)
	if d, ok := f.doms[name]; ok {
		d.running = false
	}
	return nil
}

func (f *fakeAPI) Undefine(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rec("undefine " + name)
	delete(f.doms, name)
	return nil
}

func (f *fakeAPI) GuestIPs(_ context.Context, name string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ips[name], nil
}

func (f *fakeAPI) EnsureNetwork(_ context.Context, name, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rec("network " + name)
	return nil
}

func (f *fakeAPI) Events(context.Context) (<-chan LifecycleEvent, error) { return f.events, nil }
func (f *fakeAPI) Close() error                                          { return nil }

// harness builds a driver over a fake libvirt, a fake qemu-img and an image
// server.
type harness struct {
	t     *testing.T
	api   *fakeAPI
	dr    *Driver
	image driver.ImageRef
	inv   driver.Inventory
	dir   string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "pretend qcow2"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
	t.Cleanup(srv.Close)
	sum := sha256.Sum256([]byte(body))
	h := &harness{t: t, api: newFake(), dir: dir, image: driver.ImageRef{
		Name: "ubuntu", Version: "1", Format: "qcow2", Digest: "sha256:" + hex.EncodeToString(sum[:]), URL: srv.URL,
	}}
	h.inv = driver.Inventory{Arch: "amd64", Total: driver.Resources{VCPU: 12, RAMMiB: 16000, DiskGiB: 200}}
	run := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		return nil, os.WriteFile(args[len(args)-2], []byte("overlay"), 0o600)
	}
	dr, err := New(Options{
		API: h.api, DataDir: dir, Run: run, Arch: "amd64", RunDir: filepath.Join(dir, "run"),
		Network:   Network{Bridge: "kdk-br0", Subnet: netip.MustParsePrefix("10.77.0.0/24"), Gateway: netip.MustParseAddr("10.77.0.1")},
		Inventory: func(context.Context) (driver.Inventory, error) { return h.inv, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dr.Close() })
	h.dr = dr
	return h
}

func (h *harness) spec(id string) driver.InstanceSpec {
	return driver.InstanceSpec{ID: id, Generation: 3, VCPU: 2, RAMMiB: 2048, DiskGiB: 10, Image: h.image,
		SSHKeys: []string{"ssh-ed25519 AAAA test"}}
}

func (h *harness) cached() {
	if _, err := h.dr.cache.Fetch(context.Background(), h.image); err != nil {
		h.t.Fatal(err)
	}
}

func TestEnsureCreatesOnceAndConverges(t *testing.T) {
	h := newHarness(t)
	h.cached()
	ctx := context.Background()
	spec := h.spec("abc")

	o, err := h.dr.Ensure(ctx, spec)
	if err != nil || o.Phase != driver.PhaseProvisioning || o.ID != "abc" {
		t.Fatalf("first Ensure = %+v, %v", o, err)
	}
	want := []string{"network kdk-net", "define", "start kdk-abc"}
	if got := strings.Join(h.api.callList(), ","); got != strings.Join(want, ",") {
		t.Fatalf("calls %v, want %v", got, want)
	}
	for _, f := range []string{"disk.qcow2", "seed.iso"} {
		if _, err := os.Stat(filepath.Join(h.dir, "instances", "abc", f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
	m, ok := ParseMarker(h.api.doms["kdk-abc"].xml)
	if !ok || m.ID != "abc" || m.Generation != 3 {
		t.Fatalf("marker %+v %v", m, ok)
	}

	// Running, no lease yet.
	o, err = h.dr.Ensure(ctx, spec)
	if err != nil || o.Phase != driver.PhaseProvisioning || !strings.Contains(o.Reason, "no address") {
		t.Fatalf("no lease: %+v, %v", o, err)
	}
	// A lease outside the guest subnet does not count.
	h.api.ips["kdk-abc"] = []string{"192.168.1.5"}
	if o, _ = h.dr.Ensure(ctx, spec); o.Phase != driver.PhaseProvisioning {
		t.Fatalf("foreign address: %+v", o)
	}
	h.api.ips["kdk-abc"] = []string{"10.77.0.136"}
	o, err = h.dr.Ensure(ctx, spec)
	if err != nil || o.Phase != driver.PhaseRunning || len(o.GuestIPs) != 1 || o.GuestIPs[0] != "10.77.0.136" || o.StartedAt.IsZero() {
		t.Fatalf("running: %+v, %v", o, err)
	}
	defines := 0
	for _, c := range h.api.callList() {
		if c == "define" {
			defines++
		}
	}
	if defines != 1 {
		t.Fatalf("%d defines over four Ensure calls, want 1", defines)
	}
}

func TestEnsureStartedAtComesFromPidFile(t *testing.T) {
	h := newHarness(t)
	h.cached()
	ctx := context.Background()
	if _, err := h.dr.Ensure(ctx, h.spec("abc")); err != nil {
		t.Fatal(err)
	}
	h.api.ips["kdk-abc"] = []string{"10.77.0.136"}
	pid := filepath.Join(h.dir, "run", "kdk-abc.pid")
	_ = os.MkdirAll(filepath.Dir(pid), 0o755)
	_ = os.WriteFile(pid, []byte("1"), 0o644)
	when := time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC)
	_ = os.Chtimes(pid, when, when)
	o, _ := h.dr.Ensure(ctx, h.spec("abc"))
	if !o.StartedAt.Equal(when) {
		t.Fatalf("StartedAt = %v, want %v", o.StartedAt, when)
	}
}

func TestEnsureStartsAStoppedDomain(t *testing.T) {
	h := newHarness(t)
	h.cached()
	ctx := context.Background()
	if _, err := h.dr.Ensure(ctx, h.spec("abc")); err != nil {
		t.Fatal(err)
	}
	h.api.doms["kdk-abc"].running = false // the node rebooted
	o, err := h.dr.Ensure(ctx, h.spec("abc"))
	if err != nil || o.Phase != driver.PhaseProvisioning {
		t.Fatalf("%+v, %v", o, err)
	}
	if !h.api.doms["kdk-abc"].running {
		t.Fatal("domain was not started")
	}
	for _, c := range h.api.callList() {
		if strings.HasPrefix(c, "undefine") {
			t.Fatalf("a stopped domain must keep its disk: %v", h.api.callList())
		}
	}
}

func TestEnsureFetchesImageInBackground(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	spec := h.spec("abc")
	o, err := h.dr.Ensure(ctx, spec)
	if err != nil || o.Phase != driver.PhaseProvisioning || !strings.Contains(o.Reason, "fetching image") {
		t.Fatalf("%+v, %v", o, err)
	}
	select {
	case ev := <-h.dr.Events():
		if ev.InstanceID != "abc" {
			t.Fatalf("event %+v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no event when the background create finished")
	}
	if _, ok := h.api.doms["kdk-abc"]; !ok {
		t.Fatal("domain not defined after the background create")
	}
	if o, err = h.dr.Ensure(ctx, spec); err != nil || o.ID != "abc" {
		t.Fatalf("after the create: %+v, %v", o, err)
	}
}

func TestBackgroundFailureIsReportedOnce(t *testing.T) {
	h := newHarness(t)
	h.image.Digest = "sha256:" + strings.Repeat("0", 64) // wrong
	ctx := context.Background()
	if _, err := h.dr.Ensure(ctx, h.spec("abc")); err != nil {
		t.Fatal(err)
	}
	<-h.dr.Events()
	_, err := h.dr.Ensure(ctx, h.spec("abc"))
	if err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("err = %v", err)
	}
	// The next pass retries rather than repeating the old error.
	o, err := h.dr.Ensure(ctx, h.spec("abc"))
	if err != nil || !strings.Contains(o.Reason, "fetching") {
		t.Fatalf("retry: %+v, %v", o, err)
	}
	_ = h.dr.Delete(ctx, "abc")
}

func TestCreateRollsBackOnFailure(t *testing.T) {
	h := newHarness(t)
	h.cached()
	h.api.startErr = errors.New("boom")
	_, err := h.dr.Ensure(context.Background(), h.spec("abc"))
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
	if _, ok := h.api.doms["kdk-abc"]; ok {
		t.Error("domain left defined")
	}
	if _, err := os.Stat(filepath.Join(h.dir, "instances", "abc")); !os.IsNotExist(err) {
		t.Errorf("instance dir left behind: %v", err)
	}
	h.api.startErr = nil
	if o, err := h.dr.Ensure(context.Background(), h.spec("abc")); err != nil || o.ID != "abc" {
		t.Fatalf("retry: %+v, %v", o, err)
	}
}

func TestDeleteIsIdempotentAndCleansEverything(t *testing.T) {
	h := newHarness(t)
	h.cached()
	ctx := context.Background()
	if _, err := h.dr.Ensure(ctx, h.spec("abc")); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		if err := h.dr.Delete(ctx, "abc"); err != nil {
			t.Fatalf("Delete #%d: %v", i+1, err)
		}
	}
	if len(h.api.doms) != 0 {
		t.Errorf("domains left: %v", h.api.doms)
	}
	if _, err := os.Stat(filepath.Join(h.dir, "instances", "abc")); !os.IsNotExist(err) {
		t.Errorf("instance dir left: %v", err)
	}
	if err := h.dr.Delete(ctx, "never-existed"); err != nil {
		t.Errorf("Delete of a missing instance: %v", err)
	}
	if err := h.dr.Delete(ctx, "../etc"); err == nil {
		t.Error("Delete accepted a path-like id")
	}
}

func TestOwnershipForeignDomainsAreNeverTouched(t *testing.T) {
	h := newHarness(t)
	h.cached()
	ctx := context.Background()
	h.api.doms["web01"] = &fakeDom{xml: "<domain><name>web01</name></domain>", running: true}
	h.api.doms["kdk-abc"] = &fakeDom{xml: "<domain><name>kdk-abc</name></domain>", running: true} // prefix, no marker
	h.api.doms["kdk-mine"] = &fakeDom{running: true}
	x, _ := RenderDomain(DomainParams{ID: "mine", VCPU: 1, RAMMiB: 1, Disk: "/d", Seed: "/s"})
	h.api.doms["kdk-mine"].xml = x

	l, err := h.dr.List(ctx)
	if err != nil || len(l) != 1 || l[0].ID != "mine" {
		t.Fatalf("List = %+v, %v", l, err)
	}
	for _, n := range h.api.xmlReads {
		if n == "web01" {
			t.Error("read the definition of a domain without the kdk- prefix")
		}
	}
	if _, err := h.dr.Ensure(ctx, h.spec("abc")); err == nil || !strings.Contains(err.Error(), "left alone") {
		t.Fatalf("Ensure over a foreign kdk- domain: %v", err)
	}
	if err := h.dr.Delete(ctx, "abc"); err == nil {
		t.Fatal("Delete removed a foreign kdk- domain")
	}
	for _, c := range h.api.callList() {
		if strings.Contains(c, "kdk-abc") || strings.Contains(c, "web01") {
			t.Fatalf("a foreign domain was changed: %v", h.api.callList())
		}
	}
	if len(h.api.doms) != 3 {
		t.Fatalf("domains changed: %v", len(h.api.doms))
	}
}

func TestGPUPassthrough(t *testing.T) {
	h := newHarness(t)
	h.cached()
	h.inv.Accelerators = []driver.Accelerator{
		{ID: "0000:26:00.0", PCIAddress: "0000:26:00.0", Companions: []string{"0000:26:00.1"}, Health: driver.HealthOK},
		{ID: "0000:27:00.0", PCIAddress: "0000:27:00.0", Health: driver.HealthUnavailable, HealthReason: "bound to nvidia, not vfio-pci"},
	}
	ctx := context.Background()
	spec := h.spec("gpu1")
	spec.AcceleratorIDs = []string{"0000:26:00.0"}
	if _, err := h.dr.Ensure(ctx, spec); err != nil {
		t.Fatal(err)
	}
	x := h.api.doms["kdk-gpu1"].xml
	if n := strings.Count(x, "<hostdev "); n != 2 {
		t.Fatalf("%d hostdevs, want the GPU and its audio function", n)
	}
	if !strings.Contains(x, "bus='0x26' slot='0x00' function='0x1'") {
		t.Error("the audio function is not passed through")
	}

	for name, ids := range map[string][]string{"unhealthy": {"0000:27:00.0"}, "unknown": {"0000:99:00.0"}} {
		s := h.spec("gpu2")
		s.AcceleratorIDs = ids
		if _, err := h.dr.Ensure(ctx, s); !errors.Is(err, driver.ErrUnsupported) {
			t.Errorf("%s: err = %v, want ErrUnsupported", name, err)
		}
	}
	if _, ok := h.api.doms["kdk-gpu2"]; ok {
		t.Error("a domain was defined for an unusable accelerator")
	}
}

func TestValidate(t *testing.T) {
	h := newHarness(t)
	good := h.spec("abc")
	if err := Validate(good, "amd64"); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(*driver.InstanceSpec){
		"bad id":        func(s *driver.InstanceSpec) { s.ID = "a/b" },
		"arm64 image":   func(s *driver.InstanceSpec) { s.Image.Arch = "arm64" },
		"no vcpu":       func(s *driver.InstanceSpec) { s.VCPU = 0 },
		"no disk":       func(s *driver.InstanceSpec) { s.DiskGiB = 0 },
		"no keys":       func(s *driver.InstanceSpec) { s.SSHKeys = nil },
		"bad digest":    func(s *driver.InstanceSpec) { s.Image.Digest = "md5:x" },
		"bad hostname":  func(s *driver.InstanceSpec) { s.Hostname = "Not_Valid" },
		"plain userdat": func(s *driver.InstanceSpec) { s.UserData = []byte("hello") },
	} {
		s := good
		mut(&s)
		if err := Validate(s, "amd64"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := Validate(good, "arm64"); !errors.Is(err, driver.ErrUnsupported) {
		t.Errorf("arm64 host: %v", err)
	}
}

func TestLifecycleEventsReachTheReconciler(t *testing.T) {
	h := newHarness(t)
	h.api.events <- LifecycleEvent{Name: "web01"}
	h.api.events <- LifecycleEvent{Name: "kdk-"}
	h.api.events <- LifecycleEvent{Name: "kdk-abc"}
	select {
	case ev := <-h.dr.Events():
		if ev.InstanceID != "abc" {
			t.Fatalf("event %+v: foreign or empty names must be dropped", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no event")
	}
}

func TestNetworkXML(t *testing.T) {
	x, err := NetworkXML(Network{Bridge: "kdk-br0", Subnet: netip.MustParsePrefix("10.77.0.0/24"), Gateway: netip.MustParseAddr("10.77.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<name>kdk-net</name>", "<forward mode='open'/>", "name='kdk-br0'",
		"address='10.77.0.1' netmask='255.255.255.0'", "start='10.77.0.100' end='10.77.0.200'"} {
		if !strings.Contains(x, want) {
			t.Errorf("network XML lacks %q:\n%s", want, x)
		}
	}
	if _, err := NetworkXML(Network{Subnet: netip.MustParsePrefix("10.77.0.0/28")}); err == nil {
		t.Error("a /28 has no room for the DHCP range")
	}
}

func TestNewRequiresTraversableDataDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := New(Options{API: newFake(), DataDir: dir, Arch: "amd64",
		Network: Network{Bridge: "b", Subnet: netip.MustParsePrefix("10.77.0.0/24"), Gateway: netip.MustParseAddr("10.77.0.1")}})
	if err == nil || !strings.Contains(err.Error(), "traversable") {
		t.Fatalf("err = %v", err)
	}
}

func TestInventoryListsCachedImages(t *testing.T) {
	h := newHarness(t)
	h.cached()
	inv, err := h.dr.Inventory(context.Background())
	if err != nil || len(inv.CachedImages) != 1 || inv.CachedImages[0].Name != "ubuntu" {
		t.Fatalf("%+v, %v", inv.CachedImages, err)
	}
	c, _ := h.dr.Capabilities(context.Background())
	if !c.GPUPassthrough || c.Arch != "amd64" {
		t.Fatalf("%+v", c)
	}
}
