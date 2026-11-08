package libvirt

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"strings"

	"github.com/kdomanski/iso9660"
	"go.yaml.in/yaml/v3"

	"github.com/St1viG/cj/internal/agent/driver"
)

// SSHUser is the guest user that gets the customer's keys.
const SSHUser = "ubuntu"

// SeedLabel is the volume label cloud-init's NoCloud datasource looks for.
const SeedLabel = "cidata"

// hostname is the guest's hostname: the spec's, else "kdk-" and the start
// of the id.
func hostname(spec driver.InstanceSpec) string {
	if spec.Hostname != "" {
		return spec.Hostname
	}
	return "kdk-" + strings.TrimRight(strings.ToLower(spec.ID[:min(8, len(spec.ID))]), "-")
}

// MetaData is the NoCloud meta-data: the instance id, which cloud-init
// keys its once-per-instance work on, and the hostname.
func MetaData(spec driver.InstanceSpec) ([]byte, error) {
	return yaml.Marshal(struct {
		InstanceID    string `yaml:"instance-id"`
		LocalHostname string `yaml:"local-hostname"`
	}{spec.ID, hostname(spec)})
}

type cloudConfig struct {
	MergeHow    []merger    `yaml:"merge_how,omitempty"`
	Users       []cloudUser `yaml:"users"`
	SSHPwauth   bool        `yaml:"ssh_pwauth"`
	DisableRoot bool        `yaml:"disable_root"`
}

type merger struct {
	Name     string   `yaml:"name"`
	Settings []string `yaml:"settings"`
}

type cloudUser struct {
	Name              string   `yaml:"name"`
	Shell             string   `yaml:"shell"`
	Groups            []string `yaml:"groups"`
	Sudo              string   `yaml:"sudo"`
	LockPasswd        bool     `yaml:"lock_passwd"`
	SSHAuthorizedKeys []string `yaml:"ssh_authorized_keys"`
}

// kondukterConfig is Kondukter's own cloud-config: the ubuntu user with
// the customer's keys and passwordless sudo, no password logins, no root
// logins. After customer cloud-config it is merged last, appending to the
// customer's lists (users, among them) and replacing the customer's
// settings, so its own take precedence (ADR-0015).
func kondukterConfig(spec driver.InstanceSpec, merge bool) ([]byte, error) {
	cc := cloudConfig{
		Users: []cloudUser{{
			Name: SSHUser, Shell: "/bin/bash", Groups: []string{"adm", "sudo"},
			Sudo: "ALL=(ALL) NOPASSWD:ALL", LockPasswd: true,
			SSHAuthorizedKeys: append([]string{}, spec.SSHKeys...),
		}},
		SSHPwauth: false, DisableRoot: true,
	}
	if merge {
		cc.MergeHow = []merger{
			{Name: "list", Settings: []string{"append"}},
			{Name: "dict", Settings: []string{"recurse_list", "replace"}},
			{Name: "str", Settings: []string{}},
		}
	}
	raw, err := yaml.Marshal(cc)
	if err != nil {
		return nil, fmt.Errorf("libvirt: user-data: %w", err)
	}
	return append([]byte("#cloud-config\n"), raw...), nil
}

// UserData is the NoCloud user-data: Kondukter's cloud-config alone, or,
// with customer user-data (at most MaxUserData), a MIME multipart of the
// customer's part and then Kondukter's.
func UserData(spec driver.InstanceSpec) ([]byte, error) {
	own, err := kondukterConfig(spec, len(spec.UserData) > 0)
	if err != nil || len(spec.UserData) == 0 {
		return own, err
	}
	if len(spec.UserData) > MaxUserData {
		return nil, fmt.Errorf("libvirt: user-data is %d bytes, at most %d", len(spec.UserData), MaxUserData)
	}
	typ, err := userDataType(spec.UserData)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	h.Write(spec.UserData)
	h.Write(own)
	boundary := "kdk-" + hex.EncodeToString(h.Sum(nil))[:32]

	var b bytes.Buffer
	fmt.Fprintf(&b, "Content-Type: multipart/mixed; boundary=%q\r\nMIME-Version: 1.0\r\n\r\n", boundary)
	w := multipart.NewWriter(&b)
	if err := w.SetBoundary(boundary); err != nil {
		return nil, err
	}
	for _, p := range []struct {
		typ, file string
		body      []byte
	}{{typ, "customer-user-data", spec.UserData}, {"text/cloud-config", "kondukter.yaml", own}} {
		pw, err := w.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {p.typ + `; charset="utf-8"`},
			"Mime-Version":              {"1.0"},
			"Content-Transfer-Encoding": {"base64"},
			"Content-Disposition":       {fmt.Sprintf("attachment; filename=%q", p.file)},
		})
		if err != nil {
			return nil, err
		}
		enc := base64.StdEncoding.EncodeToString(p.body)
		for len(enc) > 76 {
			fmt.Fprintf(pw, "%s\r\n", enc[:76])
			enc = enc[76:]
		}
		fmt.Fprintf(pw, "%s\r\n", enc)
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// SeedISO is the NoCloud seed: an ISO 9660 image labeled cidata with
// meta-data and user-data in its root, built in Go so a node needs no
// genisoimage or xorriso.
func SeedISO(spec driver.InstanceSpec) ([]byte, error) {
	meta, err := MetaData(spec)
	if err != nil {
		return nil, fmt.Errorf("libvirt: meta-data: %w", err)
	}
	user, err := UserData(spec)
	if err != nil {
		return nil, err
	}
	w, err := iso9660.NewWriter()
	if err != nil {
		return nil, fmt.Errorf("libvirt: seed: %w", err)
	}
	defer func() { _ = w.Cleanup() }()
	if err := w.AddFile(bytes.NewReader(meta), "meta-data"); err != nil {
		return nil, fmt.Errorf("libvirt: seed: %w", err)
	}
	if err := w.AddFile(bytes.NewReader(user), "user-data"); err != nil {
		return nil, fmt.Errorf("libvirt: seed: %w", err)
	}
	var b bytes.Buffer
	if err := w.WriteTo(&b, SeedLabel); err != nil {
		return nil, fmt.Errorf("libvirt: seed: %w", err)
	}
	return b.Bytes(), nil
}
