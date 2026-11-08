package libvirt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/St1viG/cj/internal/agent/driver"
)

func serve(t *testing.T, body string) (driver.ImageRef, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	sum := sha256.Sum256([]byte(body))
	return driver.ImageRef{Name: "ubuntu-2404", Version: "20261001", Format: "qcow2",
		Digest: "sha256:" + hex.EncodeToString(sum[:]), URL: srv.URL + "/img"}, &hits
}

func TestFetchVerifiesAndCaches(t *testing.T) {
	ref, hits := serve(t, "image bytes")
	c := &ImageCache{Dir: filepath.Join(t.TempDir(), "images")}
	p, err := c.Fetch(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "image bytes" {
		t.Fatalf("content %q", b)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o644 {
		t.Errorf("mode %v", fi.Mode())
	}
	if _, err := c.Fetch(context.Background(), ref); err != nil || hits.Load() != 1 {
		t.Fatalf("second fetch: err %v, %d downloads", err, hits.Load())
	}
	if !c.Has(ref) {
		t.Fatal("Has = false")
	}
	want := driver.CachedImage{Name: "ubuntu-2404", Version: "20261001", Digest: ref.Digest}
	if l := c.List(); len(l) != 1 || l[0] != want {
		t.Errorf("List = %+v, want [%+v]", l, want)
	}
	if e, _ := os.ReadDir(c.Dir); len(e) != 2 {
		t.Errorf("cache dir has %d entries, want the image and its .meta", len(e))
	}
}

func TestFetchRejectsWrongDigest(t *testing.T) {
	ref, _ := serve(t, "image bytes")
	ref.Digest = "sha256:" + strings.Repeat("0", 64)
	c := &ImageCache{Dir: t.TempDir()}
	if _, err := c.Fetch(context.Background(), ref); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("err = %v", err)
	}
	if e, _ := os.ReadDir(c.Dir); len(e) != 0 {
		t.Errorf("a failed download left %v", e)
	}
	if c.Has(ref) {
		t.Error("Has = true after a failed download")
	}
}

func TestFetchHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	ref := driver.ImageRef{Name: "x", Version: "1", Digest: "sha256:" + strings.Repeat("a", 64), URL: srv.URL}
	if _, err := (&ImageCache{Dir: t.TempDir()}).Fetch(context.Background(), ref); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchSharesOneDownload(t *testing.T) {
	ref, hits := serve(t, strings.Repeat("x", 1<<16))
	c := &ImageCache{Dir: t.TempDir()}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Fetch(context.Background(), ref); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := hits.Load(); n > 2 {
		t.Errorf("%d downloads for 8 concurrent fetches", n)
	}
}

func TestFetchRejectsBadRefs(t *testing.T) {
	good := driver.ImageRef{Name: "x", Version: "1", Digest: "sha256:" + strings.Repeat("a", 64), URL: "http://h/i"}
	for name, mut := range map[string]func(*driver.ImageRef){
		"path in name":    func(r *driver.ImageRef) { r.Name = "../etc/passwd" },
		"path in version": func(r *driver.ImageRef) { r.Version = "1/2" },
		"empty name":      func(r *driver.ImageRef) { r.Name = "" },
		"vmdk":            func(r *driver.ImageRef) { r.Format = "vmdk" },
		"short digest":    func(r *driver.ImageRef) { r.Digest = "sha256:abc" },
		"no url":          func(r *driver.ImageRef) { r.URL = "" },
	} {
		r := good
		mut(&r)
		if _, err := (&ImageCache{Dir: t.TempDir()}).Fetch(context.Background(), r); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestCreateOverlay(t *testing.T) {
	dir := t.TempDir()
	var got []string
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		got = append([]string{name}, args...)
		return nil, os.WriteFile(args[len(args)-2], nil, 0o600)
	}
	ov := filepath.Join(dir, "disk.qcow2")
	if err := CreateOverlay(context.Background(), run, "rel/base.qcow2", "qcow2", ov, 40); err != nil {
		t.Fatal(err)
	}
	abs, _ := filepath.Abs("rel/base.qcow2")
	want := []string{"qemu-img", "create", "-f", "qcow2", "-b", abs, "-F", "qcow2", ov, "40G"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("command %v\nwant    %v", got, want)
	}
	if fi, _ := os.Stat(ov); fi.Mode().Perm() != 0o644 {
		t.Errorf("overlay mode %v", fi.Mode())
	}
}
