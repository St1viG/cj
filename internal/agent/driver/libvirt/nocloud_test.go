package libvirt_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"maps"
	"mime"
	"mime/multipart"
	"net/mail"
	"slices"
	"strings"
	"testing"

	"github.com/kdomanski/iso9660"
	"go.yaml.in/yaml/v3"

	"github.com/St1viG/cj/internal/agent/driver"
	"github.com/St1viG/cj/internal/agent/driver/libvirt"
)

var keys = []string{
	"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIHk2i3sY5cL1JzM4QxQyH0Oo2b8m8m2Yp7r8hKj3T3dD alice@laptop",
	"ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQC7 bob@desk",
}

// validSpec is a spec Validate accepts.
func validSpec() driver.InstanceSpec {
	return driver.InstanceSpec{
		ID: webID, Generation: 1, VCPU: 2, RAMMiB: 2048, DiskGiB: 20,
		Image: driver.ImageRef{
			Name: "ubuntu-cpu", Version: "24.04-20261017", Arch: "amd64", Format: "qcow2",
			URL: "https://images.example/ubuntu.img", Digest: digest(imageBytes), SizeBytes: int64(len(imageBytes)),
		},
		SSHKeys:  keys,
		Hostname: "web-1",
	}
}

type userConfig struct {
	MergeHow []map[string]any `yaml:"merge_how"`
	Users    []struct {
		Name       string   `yaml:"name"`
		Sudo       string   `yaml:"sudo"`
		LockPasswd bool     `yaml:"lock_passwd"`
		Keys       []string `yaml:"ssh_authorized_keys"`
	} `yaml:"users"`
	SSHPwauth   *bool `yaml:"ssh_pwauth"`
	DisableRoot *bool `yaml:"disable_root"`
}

// checkOwnConfig checks Kondukter's cloud-config part.
func checkOwnConfig(t *testing.T, raw []byte, merged bool) {
	t.Helper()
	if !bytes.HasPrefix(raw, []byte("#cloud-config\n")) {
		t.Fatalf("Kondukter's part does not start with #cloud-config:\n%s", raw)
	}
	var cc userConfig
	if err := yaml.Unmarshal(raw, &cc); err != nil {
		t.Fatal(err)
	}
	if len(cc.Users) != 1 || cc.Users[0].Name != libvirt.SSHUser || !slices.Equal(cc.Users[0].Keys, keys) ||
		!cc.Users[0].LockPasswd || cc.Users[0].Sudo != "ALL=(ALL) NOPASSWD:ALL" {
		t.Fatalf("users = %+v", cc.Users)
	}
	if cc.SSHPwauth == nil || *cc.SSHPwauth || cc.DisableRoot == nil || !*cc.DisableRoot {
		t.Fatalf("want password and root logins off:\n%s", raw)
	}
	if merged != (len(cc.MergeHow) > 0) {
		t.Fatalf("merge_how = %v, want it only after customer cloud-config", cc.MergeHow)
	}
}

type part struct {
	typ  string
	body []byte
}

// parseMultipart reads user-data as cloud-init does: a MIME message whose
// parts are base64.
func parseMultipart(t *testing.T, raw []byte) []part {
	t.Helper()
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	typ, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || typ != "multipart/mixed" {
		t.Fatalf("Content-Type %q, %v", msg.Header.Get("Content-Type"), err)
	}
	r := multipart.NewReader(msg.Body, params["boundary"])
	var out []part
	for {
		p, err := r.NextPart()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		if p.Header.Get("Content-Transfer-Encoding") != "base64" {
			t.Fatalf("part encoding %q", p.Header.Get("Content-Transfer-Encoding"))
		}
		body, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, p))
		if err != nil {
			t.Fatal(err)
		}
		pt, _, err := mime.ParseMediaType(p.Header.Get("Content-Type"))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, part{pt, body})
	}
}

func TestMetaData(t *testing.T) {
	for hostname, want := range map[string]string{"web-1": "web-1", "": "kdk-0f8fad5b"} {
		spec := validSpec()
		spec.Hostname = hostname
		raw, err := libvirt.MetaData(spec)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]string
		if err := yaml.Unmarshal(raw, &m); err != nil || m["instance-id"] != webID || m["local-hostname"] != want || len(m) != 2 {
			t.Fatalf("meta-data %q, %v", raw, err)
		}
	}
}

func TestUserDataWithoutCustomerData(t *testing.T) {
	raw, err := libvirt.UserData(validSpec())
	if err != nil {
		t.Fatal(err)
	}
	checkOwnConfig(t, raw, false)
}

func TestUserDataMergesCustomerDataFirst(t *testing.T) {
	for custom, typ := range map[string]string{
		"#cloud-config\npackages: [htop]\nusers: [default]\n": "text/cloud-config",
		"#!/bin/sh\necho hello > /etc/motd\n":                 "text/x-shellscript",
		"#cloud-boothook\n#!/bin/sh\necho early\n":            "text/cloud-boothook",
	} {
		spec := validSpec()
		spec.UserData = []byte(custom)
		raw, err := libvirt.UserData(spec)
		if err != nil {
			t.Fatal(err)
		}
		parts := parseMultipart(t, raw)
		if len(parts) != 2 || string(parts[0].body) != custom || parts[0].typ != typ || parts[1].typ != "text/cloud-config" {
			t.Fatalf("parts = %+v, want the customer's %s, then Kondukter's", parts, typ)
		}
		checkOwnConfig(t, parts[1].body, true)
		again, err := libvirt.UserData(spec)
		if err != nil || !bytes.Equal(raw, again) {
			t.Fatal("user-data is not deterministic")
		}
	}
}

func TestUserDataCap(t *testing.T) {
	spec := validSpec()
	head := "#!/bin/sh\n"
	spec.UserData = []byte(head + strings.Repeat("#", libvirt.MaxUserData-len(head)))
	if len(spec.UserData) != 16384 {
		t.Fatalf("fixture is %d bytes", len(spec.UserData))
	}
	if _, err := libvirt.UserData(spec); err != nil {
		t.Fatalf("16 KiB of user-data: %v", err)
	}
	if err := libvirt.Validate(spec, "amd64"); err != nil {
		t.Fatalf("16 KiB of user-data: %v", err)
	}
	spec.UserData = append(spec.UserData, '#')
	if _, err := libvirt.UserData(spec); err == nil || !strings.Contains(err.Error(), "at most 16384") {
		t.Fatalf("16 KiB + 1 of user-data: %v", err)
	}
	if err := libvirt.Validate(spec, "amd64"); err == nil {
		t.Fatal("Validate accepted 16 KiB + 1 of user-data")
	}
}

func TestUserDataTypes(t *testing.T) {
	for custom, unsupported := range map[string]bool{
		"hello":                     true,
		"Content-Type: multipart/x": true,
		"#cloud-config\n- a list\n": false,
		"#cloud-config\nfoo: [\n":   false,
	} {
		spec := validSpec()
		spec.UserData = []byte(custom)
		_, err := libvirt.UserData(spec)
		if err == nil || errors.Is(err, driver.ErrUnsupported) != unsupported {
			t.Errorf("UserData(%q): %v", custom, err)
		}
		if err := libvirt.Validate(spec, "amd64"); err == nil {
			t.Errorf("Validate accepted user-data %q", custom)
		}
	}
}

func TestSeedISO(t *testing.T) {
	spec := validSpec()
	spec.UserData = []byte("#cloud-config\npackages: [htop]\n")
	raw, err := libvirt.SeedISO(spec)
	if err != nil {
		t.Fatal(err)
	}
	// The primary volume descriptor, at sector 16, carries the label
	// cloud-init's NoCloud datasource finds the seed by.
	pvd := raw[16*2048:]
	if pvd[0] != 1 || string(pvd[1:6]) != "CD001" || strings.TrimRight(string(pvd[40:72]), " ") != libvirt.SeedLabel {
		t.Fatalf("primary volume descriptor %q", pvd[:72])
	}
	// Plain ISO 9660 names, which Linux's isofs shows as meta-data and
	// user-data (lower case, version dropped).
	for _, name := range []string{"meta-data;1", "user-data;1"} {
		if !bytes.Contains(raw, []byte(name)) {
			t.Errorf("no directory record %s", name)
		}
	}

	img, err := iso9660.OpenImage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if label, err := img.Label(); err != nil || label != "cidata" {
		t.Fatalf("label %q, %v", label, err)
	}
	root, err := img.RootDir()
	if err != nil {
		t.Fatal(err)
	}
	children, err := root.GetChildren()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]byte{}
	for _, f := range children {
		b, err := io.ReadAll(f.Reader())
		if err != nil {
			t.Fatal(err)
		}
		got[f.Name()] = b
	}
	meta, err := libvirt.MetaData(spec)
	if err != nil {
		t.Fatal(err)
	}
	user, err := libvirt.UserData(spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !bytes.Equal(got["meta-data"], meta) || !bytes.Equal(got["user-data"], user) {
		t.Fatalf("seed holds %v", slices.Sorted(maps.Keys(got)))
	}

	spec.UserData = []byte("not a known format")
	if _, err := libvirt.SeedISO(spec); err == nil {
		t.Fatal("SeedISO accepted bad user-data")
	}
}
