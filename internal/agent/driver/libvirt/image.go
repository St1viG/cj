package libvirt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/sync/singleflight"

	"github.com/St1viG/cj/internal/agent/driver"
)

var (
	nameRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	digestRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// Runner runs an external command and returns its combined output.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// ExecRunner runs commands with os/exec.
func ExecRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// ImageCache holds guest images at <Dir>/<name>-<version>.<format>, each next
// to a ".meta" file (JSON: name, version, digest) that says which digest the
// image was verified against. A cached image is trusted without being hashed
// again.
type ImageCache struct {
	Dir string
	// Client fetches images; nil means http.DefaultClient.
	Client *http.Client

	flight singleflight.Group
}

// check validates the parts of ref that end up in a file name or a command.
func check(ref driver.ImageRef) (format string, err error) {
	format = ref.Format
	if format == "" {
		format = "qcow2"
	}
	switch {
	case !nameRe.MatchString(ref.Name) || !nameRe.MatchString(ref.Version):
		return "", fmt.Errorf("libvirt: image %q version %q: want letters, digits, dot, dash", ref.Name, ref.Version)
	case format != "qcow2" && format != "raw":
		return "", fmt.Errorf("%w: image format %q", driver.ErrUnsupported, format)
	case !digestRe.MatchString(ref.Digest):
		return "", fmt.Errorf("libvirt: image %s: digest %q is not sha256:<hex>", ref.Name, ref.Digest)
	case ref.URL == "":
		return "", fmt.Errorf("libvirt: image %s has no URL", ref.Name)
	}
	return format, nil
}

// Path is where ref lives in the cache.
func (c *ImageCache) Path(ref driver.ImageRef) string {
	f := ref.Format
	if f == "" {
		f = "qcow2"
	}
	return filepath.Join(c.Dir, ref.Name+"-"+ref.Version+"."+f)
}

// Has reports whether ref is cached and was verified against its digest.
func (c *ImageCache) Has(ref driver.ImageRef) bool {
	if _, err := check(ref); err != nil {
		return false
	}
	p := c.Path(ref)
	if _, err := os.Stat(p); err != nil {
		return false
	}
	m, ok := readMeta(p + metaSuffix)
	return ok && m.Digest == ref.Digest
}

const metaSuffix = ".meta"

func readMeta(path string) (driver.CachedImage, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return driver.CachedImage{}, false
	}
	var m driver.CachedImage
	return m, json.Unmarshal(b, &m) == nil
}

// Fetch returns the path of ref, downloading it first if it is not cached:
// the body goes to a temporary file in the cache directory, is hashed on the
// way, and replaces the final name only if the digest matches. Concurrent
// fetches of one image share a single download.
func (c *ImageCache) Fetch(ctx context.Context, ref driver.ImageRef) (string, error) {
	if _, err := check(ref); err != nil {
		return "", err
	}
	if c.Has(ref) {
		return c.Path(ref), nil
	}
	p := c.Path(ref)
	_, err, _ := c.flight.Do(p, func() (any, error) { return nil, c.download(ctx, ref, p) })
	if err != nil {
		return "", err
	}
	return p, nil
}

func (c *ImageCache) download(ctx context.Context, ref driver.ImageRef, dst string) error {
	if err := os.MkdirAll(c.Dir, 0o755); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ref.URL, nil)
	if err != nil {
		return err
	}
	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("libvirt: fetch image %s: %w", ref.Name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("libvirt: fetch image %s: %s", ref.Name, resp.Status)
	}
	tmp, err := os.CreateTemp(c.Dir, ".part-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), resp.Body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("libvirt: fetch image %s: %w", ref.Name, err)
	}
	if got := "sha256:" + hex.EncodeToString(h.Sum(nil)); got != ref.Digest {
		_ = tmp.Close()
		return fmt.Errorf("libvirt: image %s: digest is %s, want %s", ref.Name, got, ref.Digest)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// QEMU runs as another user and reads the image as a backing file.
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return err
	}
	meta, err := json.Marshal(driver.CachedImage{Name: ref.Name, Version: ref.Version, Digest: ref.Digest})
	if err != nil {
		return err
	}
	return os.WriteFile(dst+metaSuffix, meta, 0o644)
}

// List returns the cached images for the inventory.
func (c *ImageCache) List() []driver.CachedImage {
	metas, _ := filepath.Glob(filepath.Join(c.Dir, "*"+metaSuffix))
	var out []driver.CachedImage
	for _, p := range metas {
		if _, err := os.Stat(strings.TrimSuffix(p, metaSuffix)); err != nil {
			continue
		}
		if m, ok := readMeta(p); ok {
			out = append(out, m)
		}
	}
	slices.SortFunc(out, func(a, b driver.CachedImage) int {
		return strings.Compare(a.Name+"\x00"+a.Version, b.Name+"\x00"+b.Version)
	})
	return out
}

// CreateOverlay creates a qcow2 overlay of sizeGiB on top of a backing image.
func CreateOverlay(ctx context.Context, run Runner, backing, backingFormat, overlay string, sizeGiB int64) error {
	abs, err := filepath.Abs(backing)
	if err != nil {
		return err
	}
	_, err = run(ctx, "qemu-img", "create", "-f", "qcow2", "-b", abs, "-F", backingFormat, overlay, strconv.FormatInt(sizeGiB, 10)+"G")
	if err != nil {
		return err
	}
	// QEMU runs as another user.
	return os.Chmod(overlay, 0o644)
}
