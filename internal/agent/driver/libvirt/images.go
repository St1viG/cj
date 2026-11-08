package libvirt

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/St1viG/cj/internal/agent/driver"
)

// Images is the node's image cache: <Dir>/<name>-<version>.<format>, each
// with a <file>.json sidecar recording the verified digest. Images are
// immutable per version (ADR-0015): a cached file is never replaced, as
// overlays reference it as their backing file.
type Images struct {
	Dir  string
	HTTP *http.Client
	Log  *slog.Logger

	mu    sync.Mutex
	locks map[string]chan struct{}
}

type imageMeta struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
	URL     string `json:"url"`
}

func imageFormat(ref driver.ImageRef) string {
	if ref.Format == "" {
		return "qcow2"
	}
	return ref.Format
}

// Path is where ref is cached.
func (im *Images) Path(ref driver.ImageRef) string {
	return filepath.Join(im.Dir, ref.Name+"-"+ref.Version+"."+imageFormat(ref))
}

// Ensure returns the path of ref in the cache, downloading it first if it
// is not there: streamed to a temporary file in the cache directory,
// checked against the size and sha256 digest, then renamed into place, so
// a cached file is always complete and verified. Concurrent calls for one
// image share a single download.
func (im *Images) Ensure(ctx context.Context, ref driver.ImageRef) (string, error) {
	path := im.Path(ref)
	unlock, err := im.lock(ctx, path)
	if err != nil {
		return "", err
	}
	defer unlock()
	if err := mkdirSearchable(im.Dir); err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err == nil {
		return path, im.verifyCached(ref, path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("libvirt: %w", err)
	}
	if err := im.fetch(ctx, ref, path); err != nil {
		return "", err
	}
	return path, im.writeMeta(ref, path)
}

// verifyCached checks a cached file against ref's digest: by its sidecar,
// or, for a file without one (placed by an operator, or cached by an agent
// that died before writing the sidecar), by hashing it once.
func (im *Images) verifyCached(ref driver.ImageRef, path string) error {
	m, err := readMeta(path + ".json")
	switch {
	case err == nil && m.Digest == ref.Digest:
		return nil
	case err == nil:
		return fmt.Errorf("libvirt: image %s-%s is cached with digest %s, but the spec says %s; image versions are immutable", ref.Name, ref.Version, m.Digest, ref.Digest)
	case !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("libvirt: image %s-%s: %w", ref.Name, ref.Version, err)
	}
	sum, err := hashFile(path)
	if err != nil {
		return fmt.Errorf("libvirt: %w", err)
	}
	if sum != ref.Digest {
		return fmt.Errorf("libvirt: %s has digest %s, not %s; remove it to download the image again", path, sum, ref.Digest)
	}
	return im.writeMeta(ref, path)
}

func (im *Images) fetch(ctx context.Context, ref driver.ImageRef, path string) error {
	name := ref.Name + "-" + ref.Version
	// Partial downloads of a previous agent process.
	if old, err := filepath.Glob(filepath.Join(im.Dir, "."+filepath.Base(path)+".part-*")); err == nil {
		for _, f := range old {
			_ = os.Remove(f)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ref.URL, nil)
	if err != nil {
		return fmt.Errorf("libvirt: image %s: %w", name, err)
	}
	began := time.Now()
	im.log().Info("libvirt: downloading image", "image", name, "url", ref.URL, "bytes", ref.SizeBytes)
	client := im.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("libvirt: download image %s: %w", name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("libvirt: download image %s: %s", name, resp.Status)
	}
	if ref.SizeBytes > 0 && resp.ContentLength >= 0 && resp.ContentLength != ref.SizeBytes {
		return fmt.Errorf("libvirt: download image %s: the server sends %d bytes, the spec says %d", name, resp.ContentLength, ref.SizeBytes)
	}
	tmp, err := os.CreateTemp(im.Dir, "."+filepath.Base(path)+".part-*")
	if err != nil {
		return fmt.Errorf("libvirt: %w", err)
	}
	done := false
	defer func() {
		if !done {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	body := io.Reader(resp.Body)
	if ref.SizeBytes > 0 {
		body = io.LimitReader(resp.Body, ref.SizeBytes+1)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), body)
	if err != nil {
		return fmt.Errorf("libvirt: download image %s: %w", name, err)
	}
	if ref.SizeBytes > 0 && n != ref.SizeBytes {
		return fmt.Errorf("libvirt: download image %s: got %d bytes or more, the spec says %d", name, n, ref.SizeBytes)
	}
	if sum := "sha256:" + hex.EncodeToString(h.Sum(nil)); sum != ref.Digest {
		return fmt.Errorf("libvirt: download image %s: digest %s, the spec says %s", name, sum, ref.Digest)
	}
	if err := errors.Join(tmp.Sync(), tmp.Close()); err != nil {
		return fmt.Errorf("libvirt: %w", err)
	}
	// QEMU, running as its own user, reads the backing file.
	if err := os.Chmod(tmp.Name(), 0o644); err != nil { //nolint:gosec // G302: images are public and read by QEMU's user
		return fmt.Errorf("libvirt: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("libvirt: %w", err)
	}
	done = true
	syncDir(im.Dir)
	im.log().Info("libvirt: image cached", "image", name, "bytes", n, "took", time.Since(began).Round(time.Second))
	return nil
}

func (im *Images) writeMeta(ref driver.ImageRef, path string) error {
	raw, err := json.Marshal(imageMeta{Name: ref.Name, Version: ref.Version, Digest: ref.Digest, URL: ref.URL})
	if err != nil {
		return err
	}
	return writeFileAtomic(path+".json", append(raw, '\n'), 0o644)
}

func readMeta(path string) (imageMeta, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // G304: a path in the agent's own cache
	if err != nil {
		return imageMeta{}, err
	}
	var m imageMeta
	if err := json.Unmarshal(raw, &m); err != nil {
		return imageMeta{}, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

// List reports the cached images.
func (im *Images) List() ([]driver.CachedImage, error) {
	entries, err := os.ReadDir(im.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("libvirt: %w", err)
	}
	var out []driver.CachedImage
	for _, e := range entries {
		file, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || strings.HasPrefix(file, ".") {
			continue
		}
		if _, err := os.Stat(filepath.Join(im.Dir, file)); err != nil {
			continue
		}
		m, err := readMeta(filepath.Join(im.Dir, e.Name()))
		if err != nil {
			im.log().Warn("libvirt: unreadable image sidecar", "err", err)
			continue
		}
		out = append(out, driver.CachedImage{Name: m.Name, Version: m.Version, Digest: m.Digest})
	}
	slices.SortFunc(out, func(a, b driver.CachedImage) int {
		return cmp.Or(strings.Compare(a.Name, b.Name), strings.Compare(a.Version, b.Version))
	})
	return out, nil
}

// lock takes the per-file lock, waiting no longer than ctx allows.
func (im *Images) lock(ctx context.Context, path string) (func(), error) {
	im.mu.Lock()
	if im.locks == nil {
		im.locks = map[string]chan struct{}{}
	}
	l := im.locks[path]
	if l == nil {
		l = make(chan struct{}, 1)
		im.locks[path] = l
	}
	im.mu.Unlock()
	select {
	case l <- struct{}{}:
		return func() { <-l }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (im *Images) log() *slog.Logger {
	if im.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return im.Log
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // G304: a path in the agent's own cache
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}
