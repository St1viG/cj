package libvirt_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/St1viG/cj/internal/agent/driver"
	"github.com/St1viG/cj/internal/agent/driver/libvirt"
)

// cache is an image cache in a fresh directory with a server that runs
// handler, or serves imageBytes.
type cache struct {
	t    *testing.T
	im   *libvirt.Images
	ref  driver.ImageRef
	hits atomic.Int32
}

func newCache(t *testing.T, handler http.HandlerFunc) *cache {
	t.Helper()
	c := &cache{t: t}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.hits.Add(1)
		if handler != nil {
			handler(w, r)
			return
		}
		_, _ = w.Write(imageBytes)
	}))
	t.Cleanup(srv.Close)
	c.im = &libvirt.Images{Dir: filepath.Join(t.TempDir(), "images"), HTTP: srv.Client()}
	c.ref = driver.ImageRef{
		Name: "ubuntu-cpu", Version: "24.04-20261017", Arch: "amd64", Format: "qcow2",
		URL: srv.URL + "/image.qcow2", Digest: digest(imageBytes), SizeBytes: int64(len(imageBytes)),
	}
	return c
}

func digest(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(s[:])
}

// files lists the cache directory.
func (c *cache) files() []string {
	c.t.Helper()
	entries, err := os.ReadDir(c.im.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		c.t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestImageIsFetchedVerifiedAndCached(t *testing.T) {
	c := newCache(t, nil)
	path, err := c.im.Ensure(t.Context(), c.ref)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(c.im.Dir, "ubuntu-cpu-24.04-20261017.qcow2"); path != want || c.im.Path(c.ref) != want {
		t.Fatalf("path = %s, want %s", path, want)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(imageBytes) {
		t.Fatalf("cached %q, %v", got, err)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o644 {
		t.Fatalf("image mode %v, %v; QEMU's user must read it", st.Mode(), err)
	}
	if st, err := os.Stat(c.im.Dir); err != nil || st.Mode().Perm()&0o011 != 0o011 {
		t.Fatalf("cache directory mode %v, %v; QEMU's user must search it", st.Mode(), err)
	}
	var meta map[string]string
	raw, err := os.ReadFile(path + ".json")
	if err != nil || json.Unmarshal(raw, &meta) != nil || meta["digest"] != c.ref.Digest || meta["version"] != c.ref.Version {
		t.Fatalf("sidecar %s, %v", raw, err)
	}
	if want := []string{"ubuntu-cpu-24.04-20261017.qcow2", "ubuntu-cpu-24.04-20261017.qcow2.json"}; !slices.Equal(c.files(), want) {
		t.Fatalf("cache holds %v, want %v", c.files(), want)
	}
	if _, err := c.im.Ensure(t.Context(), c.ref); err != nil || c.hits.Load() != 1 {
		t.Fatalf("second Ensure: %v after %d downloads, want the cached file", err, c.hits.Load())
	}
	list, err := c.im.List()
	if err != nil || len(list) != 1 || list[0] != (driver.CachedImage{Name: "ubuntu-cpu", Version: "24.04-20261017", Digest: c.ref.Digest}) {
		t.Fatalf("List = %+v, %v", list, err)
	}
}

func TestImageDownloadFailuresLeaveNothing(t *testing.T) {
	tampered := slices.Clone(imageBytes)
	tampered[8] ^= 1
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		ref     func(*driver.ImageRef)
		want    string
	}{
		{"digest mismatch", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(tampered) }, nil, "digest sha256:"},
		{"not found", http.NotFound, nil, "404"},
		{"content length", nil, func(r *driver.ImageRef) { r.SizeBytes++ }, "the server sends"},
		{"too long", func(w http.ResponseWriter, _ *http.Request) {
			// Chunked, so no Content-Length tells beforehand.
			_, _ = w.Write(imageBytes[:10])
			w.(http.Flusher).Flush()
			_, _ = w.Write(imageBytes[10:])
			_, _ = w.Write([]byte("more"))
		}, nil, "or more"},
		{"too short", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(imageBytes[:10])
			w.(http.Flusher).Flush()
			_, _ = w.Write(imageBytes[10 : len(imageBytes)-1])
		}, nil, "got " + strconv.Itoa(len(imageBytes)-1) + " bytes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newCache(t, tc.handler)
			ref := c.ref
			if tc.ref != nil {
				tc.ref(&ref)
			}
			_, err := c.im.Ensure(t.Context(), ref)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Ensure: %v, want an error with %q", err, tc.want)
			}
			if f := c.files(); len(f) != 0 {
				t.Fatalf("cache holds %v after a failed download", f)
			}
		})
	}
}

func TestImageIsInvisibleUntilVerified(t *testing.T) {
	sent, release := make(chan struct{}), make(chan struct{})
	c := newCache(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(imageBytes)))
		_, _ = w.Write(imageBytes[:20])
		w.(http.Flusher).Flush()
		close(sent)
		<-release
		_, _ = w.Write(imageBytes[20:])
	})
	done := make(chan error, 1)
	go func() {
		_, err := c.im.Ensure(context.Background(), c.ref)
		done <- err
	}()
	<-sent
	eventually(t, "a partial file", func() bool {
		f := c.files()
		return len(f) == 1 && strings.HasPrefix(f[0], ".ubuntu-cpu-24.04-20261017.qcow2.part-")
	})
	if exists(t, c.im.Path(c.ref)) {
		t.Fatal("the image is visible before it is verified")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !exists(t, c.im.Path(c.ref)) || slices.ContainsFunc(c.files(), func(f string) bool { return strings.Contains(f, ".part-") }) {
		t.Fatalf("cache holds %v", c.files())
	}
}

func TestImageCanceledDownloadIsRemoved(t *testing.T) {
	sent := make(chan struct{})
	c := newCache(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(imageBytes)))
		_, _ = w.Write(imageBytes[:20])
		w.(http.Flusher).Flush()
		close(sent)
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := c.im.Ensure(ctx, c.ref)
		done <- err
	}()
	<-sent
	cancel()
	if err := <-done; err == nil {
		t.Fatal("a canceled download succeeded")
	}
	if f := c.files(); len(f) != 0 {
		t.Fatalf("cache holds %v after a canceled download", f)
	}
}

func TestConcurrentEnsureDownloadsOnce(t *testing.T) {
	gate := make(chan struct{})
	c := newCache(t, func(w http.ResponseWriter, _ *http.Request) {
		<-gate
		_, _ = w.Write(imageBytes)
	})
	const n = 8
	var wg sync.WaitGroup
	paths, errs := make([]string, n), make([]error, n)
	for i := range n {
		wg.Go(func() { paths[i], errs[i] = c.im.Ensure(t.Context(), c.ref) })
	}
	eventually(t, "the download", func() bool { return c.hits.Load() == 1 })
	close(gate)
	wg.Wait()
	for i := range n {
		if errs[i] != nil || paths[i] != c.im.Path(c.ref) {
			t.Fatalf("Ensure %d = %s, %v", i, paths[i], errs[i])
		}
	}
	if c.hits.Load() != 1 {
		t.Fatalf("%d downloads, want 1", c.hits.Load())
	}
}

func TestImageEnsureGivesUpWaitingWithItsContext(t *testing.T) {
	gate := make(chan struct{})
	c := newCache(t, func(w http.ResponseWriter, _ *http.Request) {
		<-gate
		_, _ = w.Write(imageBytes)
	})
	first := make(chan error, 1)
	go func() {
		_, err := c.im.Ensure(context.Background(), c.ref)
		first <- err
	}()
	eventually(t, "the download", func() bool { return c.hits.Load() == 1 })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := c.im.Ensure(ctx, c.ref)
	close(gate)
	if err == nil {
		t.Fatal("Ensure waited out a canceled context")
	}
	if err := <-first; err != nil {
		t.Fatal(err)
	}
}

func TestCachedImageWithoutSidecarIsHashed(t *testing.T) {
	c := newCache(t, nil)
	if err := os.MkdirAll(c.im.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := c.im.Path(c.ref)
	if err := os.WriteFile(path, imageBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := c.im.Ensure(t.Context(), c.ref); err != nil || c.hits.Load() != 0 || !exists(t, path+".json") {
		t.Fatalf("Ensure: %v, %d downloads; want the placed file verified and kept", err, c.hits.Load())
	}

	c = newCache(t, nil)
	if err := os.MkdirAll(c.im.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path = c.im.Path(c.ref)
	if err := os.WriteFile(path, []byte("something else"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := c.im.Ensure(t.Context(), c.ref)
	if err == nil || !strings.Contains(err.Error(), "remove it") || c.hits.Load() != 0 {
		t.Fatalf("Ensure: %v, %d downloads; want the file left for the operator", err, c.hits.Load())
	}
	if got, _ := os.ReadFile(path); string(got) != "something else" {
		t.Fatal("a mismatching cached file was replaced")
	}
}

func TestImageVersionsAreImmutable(t *testing.T) {
	c := newCache(t, nil)
	if _, err := c.im.Ensure(t.Context(), c.ref); err != nil {
		t.Fatal(err)
	}
	ref := c.ref
	ref.Digest = digest([]byte("a rebuilt image"))
	_, err := c.im.Ensure(t.Context(), ref)
	if err == nil || !strings.Contains(err.Error(), "immutable") || c.hits.Load() != 1 {
		t.Fatalf("Ensure with another digest: %v after %d downloads", err, c.hits.Load())
	}
}

func TestStalePartialDownloadIsRemoved(t *testing.T) {
	c := newCache(t, nil)
	if err := os.MkdirAll(c.im.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(c.im.Dir, ".ubuntu-cpu-24.04-20261017.qcow2.part-123")
	if err := os.WriteFile(stale, []byte("half"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.im.Ensure(t.Context(), c.ref); err != nil {
		t.Fatal(err)
	}
	if exists(t, stale) {
		t.Fatal("the partial download of an earlier run is still there")
	}
}

func TestImageList(t *testing.T) {
	c := newCache(t, nil)
	if list, err := c.im.List(); err != nil || len(list) != 0 {
		t.Fatalf("List of a missing cache = %+v, %v", list, err)
	}
	b := c.ref
	b.Version = "24.04-20261101"
	a := c.ref
	a.Name = "debian-cpu"
	for _, ref := range []driver.ImageRef{b, c.ref, a} {
		if _, err := c.im.Ensure(t.Context(), ref); err != nil {
			t.Fatal(err)
		}
	}
	// A sidecar without its image, and a stray dotfile, are not images.
	if err := os.Remove(c.im.Path(b)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.im.Dir, ".x.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	list, err := c.im.List()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, im := range list {
		got = append(got, im.Name+"-"+im.Version)
	}
	if want := []string{"debian-cpu-24.04-20261017", "ubuntu-cpu-24.04-20261017"}; !slices.Equal(got, want) {
		t.Fatalf("List = %v, want %v", got, want)
	}
}
