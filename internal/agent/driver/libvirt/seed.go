package libvirt

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kdomanski/iso9660"
	"go.yaml.in/yaml/v3"

	"github.com/St1viG/cj/internal/agent/driver"
)

// MaxUserData is the cap on a customer's user-data.
const MaxUserData = 16 << 10

// GuestUser is the account the customer's SSH keys are installed for.
const GuestUser = "ubuntu"

var hostnameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// BuildUserData returns the NoCloud user-data: a cloud-config that creates
// GuestUser with the customer's keys. A customer's user-data (a script
// starting with #! or a #cloud-config) is added as a second part of a MIME
// multipart message; cloud-init merges cloud-config parts, with the keys of
// the first part winning, so a customer's cloud-config cannot replace the
// SSH keys we install.
func BuildUserData(keys []string, custom []byte) ([]byte, error) {
	if len(keys) == 0 {
		return nil, fmt.Errorf("%w: at least one SSH key is required", driver.ErrUnsupported)
	}
	for _, k := range keys {
		if strings.ContainsAny(k, "\r\n") || strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("libvirt: SSH key %q: want one line", k)
		}
	}
	type user struct {
		Name       string   `yaml:"name"`
		Sudo       string   `yaml:"sudo"`
		Shell      string   `yaml:"shell"`
		LockPasswd bool     `yaml:"lock_passwd"`
		Keys       []string `yaml:"ssh_authorized_keys"`
	}
	cc, err := yaml.Marshal(struct {
		Users []user `yaml:"users"`
	}{[]user{{GuestUser, "ALL=(ALL) NOPASSWD:ALL", "/bin/bash", true, keys}}})
	if err != nil {
		return nil, err
	}
	base := append([]byte("#cloud-config\n"), cc...)
	if len(custom) == 0 {
		return base, nil
	}
	if len(custom) > MaxUserData {
		return nil, fmt.Errorf("%w: user-data is %d bytes, the cap is %d", driver.ErrUnsupported, len(custom), MaxUserData)
	}
	var ctype string
	switch {
	case bytes.HasPrefix(custom, []byte("#!")):
		ctype = "text/x-shellscript"
	case bytes.HasPrefix(custom, []byte("#cloud-config")):
		ctype = "text/cloud-config"
	default:
		return nil, fmt.Errorf("%w: user-data must start with #! or #cloud-config", driver.ErrUnsupported)
	}
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	b.WriteString("Content-Type: multipart/mixed; boundary=\"" + mw.Boundary() + "\"\nMIME-Version: 1.0\n\n")
	for _, p := range []struct {
		ctype string
		body  []byte
	}{{"text/cloud-config", base}, {ctype, custom}} {
		w, err := mw.CreatePart(textproto.MIMEHeader{"Content-Type": {p.ctype + `; charset="utf-8"`}})
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(p.body); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// MetaData is the NoCloud meta-data. The instance id is fixed per instance,
// so cloud-init runs its first-boot work once and not again after a restart.
func MetaData(id, hostname string) (string, error) {
	if hostname == "" {
		hostname = strings.ToLower(id)
	}
	if !hostnameRe.MatchString(hostname) {
		return "", fmt.Errorf("libvirt: hostname %q is not a valid host name", hostname)
	}
	return "instance-id: " + DomainName(id) + "\nlocal-hostname: " + hostname + "\n", nil
}

// WriteSeed writes the NoCloud seed ISO (volume label "cidata") to path,
// atomically.
func WriteSeed(path, id, hostname string, keys []string, custom []byte) error {
	userData, err := BuildUserData(keys, custom)
	if err != nil {
		return err
	}
	metaData, err := MetaData(id, hostname)
	if err != nil {
		return err
	}
	w, err := iso9660.NewWriter()
	if err != nil {
		return err
	}
	defer func() { _ = w.Cleanup() }()
	if err := w.AddFile(bytes.NewReader(userData), "user-data"); err != nil {
		return err
	}
	if err := w.AddFile(strings.NewReader(metaData), "meta-data"); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".seed-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := w.WriteTo(tmp, "cidata"); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// QEMU runs as another user and must be able to read it.
	if err := os.Chmod(tmp.Name(), 0o644); err != nil { //nolint:gosec // QEMU runs as another user and must read the seed ISO
		return err
	}
	return os.Rename(tmp.Name(), path)
}
