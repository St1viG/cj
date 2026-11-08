//go:build libvirt

// Integration tests against the host's real libvirt (roadmap P2.5). They need
// a Fedora node as in spike S1: virtqemud and virtnetworkd running, KVM,
// qemu-img, and the firewalld arrangement that lets guests get DHCP.
//
//	KDK_IT_IMAGE=/var/lib/kondukter-s1/base.img \
//	KDK_IT_DATADIR=/var/lib/kondukter-s1/it \
//	go test -tags libvirt -run Integration -v ./internal/agent/driver/libvirt
//
// KDK_IT_DATADIR must be on a path QEMU can traverse (not /tmp or a home
// directory). The GPU test also needs KDK_IT_GPU (the GPU's PCI address,
// bound to vfio-pci) and KDK_IT_GPU_IMAGE (a standalone qcow2 with the NVIDIA
// driver installed); it passes the GPU through and runs nvidia-smi over SSH.
package libvirt

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/St1viG/cj/internal/agent/driver"
)

func itEnv(t *testing.T, name string) string {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		t.Skipf("%s is not set", name)
	}
	return v
}

// serveFile serves a local file and returns an ImageRef for it.
func serveFile(t *testing.T, path, name string) driver.ImageRef {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, path) }))
	t.Cleanup(srv.Close)
	return driver.ImageRef{Name: name, Version: "it", Format: "qcow2", Digest: "sha256:" + hex.EncodeToString(h.Sum(nil)), URL: srv.URL}
}

func itDriver(t *testing.T) (*Driver, API, string) {
	t.Helper()
	dir := itEnv(t, "KDK_IT_DATADIR")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	api, err := Dial("", "")
	if err != nil {
		t.Fatal(err)
	}
	dr, err := New(Options{
		API: api, DataDir: dir,
		Network: Network{Bridge: "kdk-br0", Subnet: netip.MustParsePrefix("10.77.0.0/24"), Gateway: netip.MustParseAddr("10.77.0.1")},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dr.Close() })
	return dr, api, dir
}

func newKey(t *testing.T) (ssh.Signer, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	sp, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return signer, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sp)))
}

// waitRunning calls Ensure until the instance reports an address.
func waitRunning(t *testing.T, dr *Driver, spec driver.InstanceSpec, limit time.Duration) driver.Observed {
	t.Helper()
	deadline := time.Now().Add(limit)
	var last driver.Observed
	for time.Now().Before(deadline) {
		o, err := dr.Ensure(context.Background(), spec)
		if err != nil {
			t.Fatalf("Ensure: %v", err)
		}
		last = o
		if o.Phase == driver.PhaseRunning {
			return o
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("not running after %s; last: %+v", limit, last)
	return last
}

func sshRun(t *testing.T, signer ssh.Signer, ip, cmd string) string {
	t.Helper()
	cfg := &ssh.ClientConfig{User: GuestUser, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 5 * time.Second}
	var lastErr error
	for range 60 {
		c, err := ssh.Dial("tcp", net.JoinHostPort(ip, "22"), cfg)
		if err != nil {
			lastErr = err
			time.Sleep(2 * time.Second)
			continue
		}
		defer func() { _ = c.Close() }()
		s, err := c.NewSession()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = s.Close() }()
		var out bytes.Buffer
		s.Stdout, s.Stderr = &out, &out
		if err := s.Run(cmd); err != nil {
			t.Fatalf("ssh %q: %v\n%s", cmd, err, out.String())
		}
		return out.String()
	}
	t.Fatalf("ssh to %s: %v", ip, lastErr)
	return ""
}

func TestIntegrationLifecycle(t *testing.T) {
	image := itEnv(t, "KDK_IT_IMAGE")
	dr, api, dir := itDriver(t)
	ctx := context.Background()
	signer, pub := newKey(t)

	// A domain that is not Kondukter's must survive everything below.
	foreign := "kdk-it-foreign"
	_ = api.Destroy(ctx, foreign)
	_ = api.Undefine(ctx, foreign)
	fx := fmt.Sprintf("<domain type='kvm'><name>%s</name><memory unit='MiB'>64</memory><os><type arch='x86_64' machine='q35'>hvm</type></os></domain>", foreign)
	if err := api.Define(ctx, fx); err != nil {
		t.Fatalf("define the foreign domain: %v", err)
	}
	t.Cleanup(func() { _ = api.Undefine(ctx, foreign) })

	spec := driver.InstanceSpec{ID: "it-life", Generation: 1, VCPU: 2, RAMMiB: 2048, DiskGiB: 10,
		Image: serveFile(t, image, "ubuntu"), SSHKeys: []string{pub}}
	_ = dr.Delete(ctx, spec.ID)
	t.Cleanup(func() { _ = dr.Delete(ctx, spec.ID) })

	t0 := time.Now()
	o := waitRunning(t, dr, spec, 5*time.Minute)
	t.Logf("running with %v after %s", o.GuestIPs, time.Since(t0).Round(time.Second))

	// Ensure twice returns the same running instance.
	again, err := dr.Ensure(ctx, spec)
	if err != nil || again.Phase != driver.PhaseRunning || again.GuestIPs[0] != o.GuestIPs[0] {
		t.Fatalf("second Ensure = %+v, %v", again, err)
	}
	out := sshRun(t, signer, o.GuestIPs[0], "cloud-init status --wait >/dev/null 2>&1; echo host=$(hostname) user=$(id -un)")
	t.Logf("guest says: %q", out)
	if !strings.Contains(out, "host=it-life") {
		t.Errorf("hostname: %q", out)
	}

	l, err := dr.List(ctx)
	if err != nil || len(l) != 1 || l[0].ID != spec.ID {
		t.Fatalf("List = %+v, %v (the foreign domain must not show up)", l, err)
	}

	// Delete twice leaves nothing.
	for i := range 2 {
		if err := dr.Delete(ctx, spec.ID); err != nil {
			t.Fatalf("Delete #%d: %v", i+1, err)
		}
	}
	if _, ok, err := api.Domain(ctx, DomainName(spec.ID)); err != nil || ok {
		t.Errorf("domain still there: %v %v", ok, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "instances", spec.ID)); !os.IsNotExist(err) {
		t.Errorf("instance files left: %v", err)
	}
	if _, ok, _ := api.Domain(ctx, foreign); !ok {
		t.Error("the foreign domain is gone")
	}
	if err := dr.Delete(ctx, "kdk-it-foreign"[len(driver.NamePrefix):]); err == nil {
		t.Error("Delete accepted a domain without the Kondukter marker")
	}
	if _, ok, _ := api.Domain(ctx, foreign); !ok {
		t.Error("Delete removed the foreign domain")
	}
}

func TestIntegrationGPU(t *testing.T) {
	gpu := itEnv(t, "KDK_IT_GPU")
	image := itEnv(t, "KDK_IT_GPU_IMAGE")
	dr, _, _ := itDriver(t)
	ctx := context.Background()
	signer, pub := newKey(t)

	inv, err := dr.Inventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, a := range inv.Accelerators {
		if a.ID == gpu {
			found = true
			if a.Health != driver.HealthOK {
				t.Fatalf("%s is %s: %s", gpu, a.Health, a.HealthReason)
			}
		}
	}
	if !found {
		t.Fatalf("the inventory has no accelerator %s: %+v", gpu, inv.Accelerators)
	}

	spec := driver.InstanceSpec{ID: "it-gpu", Generation: 1, VCPU: 4, RAMMiB: 8192, DiskGiB: 40,
		Image: serveFile(t, image, "ubuntu-gpu"), SSHKeys: []string{pub}, AcceleratorIDs: []string{gpu}}
	_ = dr.Delete(ctx, spec.ID)
	t.Cleanup(func() { _ = dr.Delete(ctx, spec.ID) })

	t0 := time.Now()
	o := waitRunning(t, dr, spec, 8*time.Minute)
	out := sshRun(t, signer, o.GuestIPs[0], "nvidia-smi --query-gpu=name --format=csv,noheader")
	t.Logf("nvidia-smi: %s (running + nvidia-smi after %s)", strings.TrimSpace(out), time.Since(t0).Round(time.Second))
	if !strings.Contains(out, "GeForce") && !strings.Contains(out, "NVIDIA") {
		t.Errorf("nvidia-smi says %q", out)
	}
	if err := dr.Delete(ctx, spec.ID); err != nil {
		t.Fatal(err)
	}
}
