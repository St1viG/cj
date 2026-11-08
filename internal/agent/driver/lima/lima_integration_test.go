//go:build lima

// The Lima driver against real VMs on this Mac (roadmap P2.6):
//
//	go test -tags lima -run TestLima -v -timeout 20m ./internal/agent/driver/lima/
//
// It uses a fresh LIMA_HOME, so instances of the operator's own are not in
// view, plus one non-Kondukter instance it creates itself to prove the
// driver leaves foreign VMs alone. The image is the spike S3 Ubuntu 24.04
// arm64 cloud image (Lima's download cache is shared, so it is fetched once).
package lima_test

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/St1viG/cj/internal/agent/driver"
	"github.com/St1viG/cj/internal/agent/driver/lima"
)

const (
	itID      = "it-1"
	itName    = "kdk-it-1"
	foreignVM = "lab-foreign"
)

// countingRunner counts limactl mutations.
type countingRunner struct {
	run       lima.Runner
	mutations []string
}

func (c *countingRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	if args[0] != "list" {
		c.mutations = append(c.mutations, strings.Join(args, " "))
	}
	return c.run(ctx, args...)
}

func limaHome(t *testing.T) string {
	if h := os.Getenv("KDK_LIMA_HOME"); h != "" {
		return h
	}
	// Short, so not $TMPDIR: Lima's sockets live under the instance
	// directory, and a UNIX socket path is under 104 bytes on macOS.
	h, err := os.MkdirTemp("/tmp", "kl")
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func itSpec(sshPort int, pubKey string) driver.InstanceSpec {
	return driver.InstanceSpec{
		ID: itID, VCPU: 1, RAMMiB: 1024, DiskGiB: 10,
		Image:    noble,
		SSHKeys:  []string{pubKey},
		UserData: []byte("#!/bin/sh\necho ran >> /var/lib/kondukter/ud-count\n"),
		Hostname: "it-web",
		PortMappings: []driver.PortMapping{
			{GuestPort: 22, Protocol: driver.TCP, HostAddress: "127.0.0.1", HostPort: sshPort},
		},
	}
}

// TestLimaHelperEnsure is the "agent" that TestLima kills mid-start: it is
// skipped unless re-executed by TestLima.
func TestLimaHelperEnsure(t *testing.T) {
	if os.Getenv("KDK_LIMA_HELPER") == "" {
		t.Skip("helper process for TestLima")
	}
	limactl, err := lima.FindLimactl("")
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(os.Getenv("KDK_LIMA_PORT"))
	// Not t.TempDir: this process is killed, so no cleanup runs; Lima
	// ignores directories of LIMA_HOME whose names start with "_".
	d, err := lima.New(lima.Options{Run: lima.Exec(limactl, os.Getenv("LIMA_HOME")), StateDir: filepath.Join(os.Getenv("LIMA_HOME"), "_kdk-helper")})
	if err != nil {
		t.Fatal(err)
	}
	o, err := d.Ensure(context.Background(), itSpec(port, os.Getenv("KDK_LIMA_KEY")))
	fmt.Printf("helper: %+v %v\n", o, err)
	<-d.Events() // killed before the start finishes
}

func TestLima(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("needs an Apple silicon Mac")
	}
	limactl, err := lima.FindLimactl("")
	if err != nil {
		t.Skip(err)
	}
	home, err := lima.ResolveHome(limaHome(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIMA_HOME", home)
	t.Logf("LIMA_HOME=%s", home)
	raw := lima.Exec(limactl, home)
	t.Cleanup(func() {
		ctx := context.Background()
		for _, n := range []string{itName, foreignVM} {
			if _, err := raw(ctx, "delete", "--force", n); err != nil {
				t.Logf("cleanup %s: %v", n, err)
			}
		}
		if os.Getenv("KDK_LIMA_HOME") == "" {
			_ = os.RemoveAll(home)
		}
	})
	// Registered after the deletion, so it runs first.
	t.Cleanup(func() {
		if t.Failed() {
			diagnose(t, raw, home)
		}
	})

	// A VM that is not Kondukter's: created, never started.
	tmpl := filepath.Join(t.TempDir(), "foreign.yaml")
	writeFile(t, tmpl, fmt.Sprintf("vmType: vz\nimages:\n- location: %s\n  arch: aarch64\n  digest: %s\ncpus: 1\nmemory: 1GiB\ndisk: 10GiB\nmounts: []\n", noble.URL, noble.Digest))
	if _, err := raw(context.Background(), "create", "--name", foreignVM, tmpl); err != nil {
		t.Fatal(err)
	}

	keyDir := t.TempDir()
	key := filepath.Join(keyDir, "id")
	run(t, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "kdk-it", "-f", key)
	pub := strings.TrimSpace(string(readFile(t, key+".pub")))
	port := freePort(t)

	runner := &countingRunner{run: raw}
	d, err := lima.New(lima.Options{Run: runner.Run, StateDir: t.TempDir(), Home: home})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	spec := itSpec(port, pub)

	// Create, then wait for the background start's event.
	began := time.Now()
	o, err := d.Ensure(ctx, spec)
	if err != nil || o.Phase != driver.PhaseProvisioning {
		t.Fatalf("Ensure = %+v, %v", o, err)
	}
	waitRunning(t, d, spec, 10*time.Minute)
	t.Logf("create -> running: %s", time.Since(began).Round(time.Second))
	waitSSH(t, key, port, time.Minute)
	t.Logf("create -> ssh: %s", time.Since(began).Round(time.Second))

	// Idempotent: a second Ensure changes nothing.
	n := len(runner.mutations)
	first, err := d.Ensure(ctx, spec)
	if err != nil || first.Phase != driver.PhaseRunning || first.StartedAt.IsZero() {
		t.Fatalf("Ensure = %+v, %v", first, err)
	}
	if again, _ := d.Ensure(ctx, spec); !again.StartedAt.Equal(first.StartedAt) || len(runner.mutations) != n {
		t.Fatalf("second Ensure = %+v; mutations %v", again, runner.mutations[n:])
	}
	if st, err := os.Stat(filepath.Join(home, itName, "ha.pid")); err != nil || !st.ModTime().Equal(first.StartedAt) {
		t.Fatalf("StartedAt %v does not come from ha.pid (%v)", first.StartedAt, err)
	}

	// The customer's view.
	// user-data runs in the background, so it may not have run yet.
	out := ssh(t, key, port, "for i in $(seq 60); do [ -e /var/lib/kondukter/ud-count ] && break; sleep 1; done; "+
		"whoami; hostname; cat /etc/kondukter/instance-id; sudo -n true && echo sudo-ok; cat /var/lib/kondukter/ud-count")
	if want := "ubuntu\nit-web\nit-1\nsudo-ok\nran\n"; out != want {
		t.Fatalf("guest says %q, want %q", out, want)
	}
	if lsof := run(t, "lsof", "-nP", fmt.Sprintf("-iTCP:%d", port), "-sTCP:LISTEN"); !strings.Contains(lsof, fmt.Sprintf("*:%d", port)) {
		t.Fatalf("the SSH forward does not listen on every address:\n%s", lsof)
	}

	// Guest ports outside the allocation stay unreachable from the host,
	// wildcard and loopback binds alike (the two trailing ignore rules).
	g1, g2 := freePort(t), freePort(t)
	serve := "(nohup python3 -m http.server %d --bind %s </dev/null >/dev/null 2>&1 &); "
	ssh(t, key, port, fmt.Sprintf(serve, g1, "0.0.0.0")+fmt.Sprintf(serve, g2, "127.0.0.1")+
		fmt.Sprintf("sleep 3; ss -Hltn | grep -e ':%d ' -e ':%d '", g1, g2))
	time.Sleep(10 * time.Second)
	for _, p := range []int{g1, g2} {
		if c, err := (&net.Dialer{Timeout: time.Second}).DialContext(t.Context(), "tcp", fmt.Sprintf("127.0.0.1:%d", p)); err == nil {
			_ = c.Close()
			t.Fatalf("guest port %d reached the host", p)
		}
	}

	// A hard power-off (the host agent killed, S3 finding 5), then an agent
	// killed while it starts the VM again: the VM comes back by itself and
	// the provision script's second run changes nothing. The power-off comes
	// soon after the first boot on purpose: without the script's sync, it
	// left the SSH host keys empty and the VM locked (runs 6 and 7).
	pid := strings.TrimSpace(string(readFile(t, filepath.Join(home, itName, "ha.pid"))))
	run(t, "kill", "-9", pid)
	waitStatus(t, raw, itName, "Stopped", time.Minute)
	helper := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestLimaHelperEnsure$", "-test.v")
	helper.Env = append(os.Environ(), "KDK_LIMA_HELPER=1", "KDK_LIMA_PORT="+strconv.Itoa(port), "KDK_LIMA_KEY="+pub)
	var helperOut bytes.Buffer
	helper.Stdout, helper.Stderr = &helperOut, &helperOut
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	waitFile(t, filepath.Join(home, itName, "ha.pid"), pid, time.Minute)
	if err := helper.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	_ = helper.Wait()
	t.Logf("killed the agent mid-start; it had said: %s", strings.TrimSpace(helperOut.String()))

	d2, err := lima.New(lima.Options{Run: raw, StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	waitRunning(t, d2, spec, 5*time.Minute)
	waitSSH(t, key, port, 2*time.Minute)
	if out := ssh(t, key, port, "grep -c kdk-it .ssh/authorized_keys; cat /var/lib/kondukter/ud-count"); out != "1\nran\n" {
		t.Fatalf("after the restart: %q, want one key and one user-data run", out)
	}

	// The foreign VM was never touched, and is not in the driver's view.
	if list, err := d2.List(ctx); err != nil || len(list) != 1 || list[0].ID != itID {
		t.Fatalf("List = %+v, %v", list, err)
	}
	for _, m := range runner.mutations {
		if strings.Contains(m, foreignVM) {
			t.Fatalf("mutation on the foreign VM: %q", m)
		}
	}
	waitStatus(t, raw, foreignVM, "Stopped", time.Second)

	for range 2 {
		if err := d2.Delete(ctx, itID); err != nil {
			t.Fatal(err)
		}
	}
	if list, err := d2.List(ctx); err != nil || len(list) != 0 {
		t.Fatalf("List after delete = %+v, %v", list, err)
	}
	waitStatus(t, raw, foreignVM, "Stopped", time.Second)
}

// diagnose logs what Lima and the guest say about a failed run.
func diagnose(t *testing.T, run lima.Runner, home string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := run(ctx, "list", "--json", itName)
	t.Logf("limactl list: %s %v", out, err)
	for _, cmd := range []string{"ss -Hltnp", "systemctl status --no-pager ssh.socket ssh.service", "ps -eo pid,lstart,cmd | grep -e sshd -e lima-guestagent | grep -v grep"} {
		out, err := run(ctx, "shell", "--workdir", "/", itName, "sudo", "sh", "-c", cmd)
		t.Logf("guest %q: %v\n%s", cmd, err, out)
	}
	for _, f := range []string{"ha.stderr.log", "serialv.log"} {
		b, err := os.ReadFile(filepath.Join(home, itName, f))
		if err != nil {
			t.Logf("%s: %v", f, err)
			continue
		}
		lines := strings.Split(strings.TrimSpace(string(b)), "\n")
		t.Logf("%s (last 60 of %d lines):\n%s", f, len(lines), strings.Join(lines[max(0, len(lines)-60):], "\n"))
	}
}

func waitRunning(t *testing.T, d *lima.Driver, spec driver.InstanceSpec, limit time.Duration) {
	t.Helper()
	deadline := time.After(limit)
	began, last := time.Now(), ""
	for {
		o, err := d.Ensure(context.Background(), spec)
		if err != nil {
			t.Fatalf("Ensure: %v", err)
		}
		if now := string(o.Phase) + ": " + o.Reason; now != last {
			t.Logf("  +%s %s", time.Since(began).Round(time.Second), now)
			last = now
		}
		if o.Phase == driver.PhaseRunning {
			return
		}
		select {
		case <-d.Events():
		case <-time.After(time.Second):
		case <-deadline:
			t.Fatalf("not running after %s: %+v", limit, o)
		}
	}
}

func waitStatus(t *testing.T, run lima.Runner, name, want string, limit time.Duration) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for {
		out, err := run(context.Background(), "list", "--format", "{{.Status}}", name)
		got := strings.TrimSpace(string(out))
		if err == nil && got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s is %q (%v), want %s", name, got, err, want)
		}
		time.Sleep(time.Second)
	}
}

// waitFile waits until path exists with content other than old.
func waitFile(t *testing.T, path, old string, limit time.Duration) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for {
		if b, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(b)) != old && len(b) > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not change in %s", path, limit)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func sshArgs(key string, port int, cmd string) []string {
	return []string{
		"-i", key, "-p", strconv.Itoa(port), "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes",
		"-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "LogLevel=ERROR",
		"-o", "ConnectTimeout=5", "ubuntu@127.0.0.1", cmd,
	}
}

func waitSSH(t *testing.T, key string, port int, limit time.Duration) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for {
		out, err := exec.CommandContext(t.Context(), "ssh", sshArgs(key, port, "true")...).CombinedOutput()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no SSH on port %d after %s: %v %s", port, limit, err, out)
		}
		time.Sleep(2 * time.Second)
	}
}

func ssh(t *testing.T, key string, port int, cmd string) string {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "ssh", sshArgs(key, port, cmd)...).Output()
	if err != nil {
		t.Fatalf("ssh %q: %v", cmd, err)
	}
	return string(out)
}

func run(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return string(out)
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

func writeFile(t *testing.T, path, s string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
