package libvirt

import (
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kdomanski/iso9660"
	"go.yaml.in/yaml/v3"
)

func TestBuildUserDataKeysOnly(t *testing.T) {
	keys := []string{"ssh-ed25519 AAAA one", `ssh-rsa BBBB "quoted": comment`}
	b, err := BuildUserData(keys, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), "#cloud-config\n") {
		t.Fatalf("no cloud-config header:\n%s", b)
	}
	var cc struct {
		Users []struct {
			Name string   `yaml:"name"`
			Keys []string `yaml:"ssh_authorized_keys"`
		} `yaml:"users"`
	}
	if err := yaml.Unmarshal(b, &cc); err != nil {
		t.Fatal(err)
	}
	if len(cc.Users) != 1 || cc.Users[0].Name != GuestUser || strings.Join(cc.Users[0].Keys, "|") != strings.Join(keys, "|") {
		t.Fatalf("users = %+v", cc.Users)
	}
}

func TestBuildUserDataCustom(t *testing.T) {
	for name, tc := range map[string]struct{ custom, ctype string }{
		"script": {"#!/bin/sh\necho hi\n", "text/x-shellscript"},
		"config": {"#cloud-config\npackages: [htop]\n", "text/cloud-config"},
	} {
		b, err := BuildUserData([]string{"ssh-ed25519 AAAA k"}, []byte(tc.custom))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		msg, err := mail.ReadMessage(strings.NewReader(string(b)))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		mt, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
		if err != nil || mt != "multipart/mixed" {
			t.Fatalf("%s: content type %q, %v", name, mt, err)
		}
		mr := multipart.NewReader(msg.Body, params["boundary"])
		var types, bodies []string
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(p)
			ct, _, _ := mime.ParseMediaType(p.Header.Get("Content-Type"))
			types, bodies = append(types, ct), append(bodies, string(body))
		}
		if len(types) != 2 || types[0] != "text/cloud-config" || types[1] != tc.ctype {
			t.Fatalf("%s: parts %v", name, types)
		}
		if !strings.Contains(bodies[0], "ssh-ed25519 AAAA k") || bodies[1] != tc.custom {
			t.Fatalf("%s: bodies %q", name, bodies)
		}
	}
}

func TestBuildUserDataRejects(t *testing.T) {
	k := []string{"ssh-ed25519 AAAA k"}
	big := append([]byte("#!/bin/sh\n"), make([]byte, MaxUserData)...)
	for name, tc := range map[string]struct {
		keys   []string
		custom []byte
	}{
		"no keys":        {nil, nil},
		"multiline key":  {[]string{"ssh-ed25519 A\nB"}, nil},
		"blank key":      {[]string{" "}, nil},
		"plain text":     {k, []byte("hello")},
		"over the cap":   {k, big},
		"just under cap": {k, big[:MaxUserData]},
	} {
		_, err := BuildUserData(tc.keys, tc.custom)
		if (err != nil) != (name != "just under cap") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestWriteSeed(t *testing.T) {
	p := filepath.Join(t.TempDir(), "seed.iso")
	if err := WriteSeed(p, "Abc-1", "", []string{"ssh-ed25519 AAAA k"}, nil); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != 0o644 {
		t.Fatalf("seed file: %v %v", fi, err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := iso9660.OpenImage(f)
	if err != nil {
		t.Fatal(err)
	}
	if l, _ := img.Label(); l != "CIDATA" && l != "cidata" {
		t.Errorf("volume label %q", l)
	}
	root, err := img.RootDir()
	if err != nil {
		t.Fatal(err)
	}
	kids, err := root.GetChildren()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, k := range kids {
		b, _ := io.ReadAll(k.Reader())
		got[strings.ToLower(strings.TrimSuffix(k.Name(), ";1"))] = string(b)
	}
	if !strings.Contains(got["user-data"], "ssh-ed25519 AAAA k") {
		t.Errorf("user-data = %q", got["user-data"])
	}
	if got["meta-data"] != "instance-id: kdk-Abc-1\nlocal-hostname: abc-1\n" {
		t.Errorf("meta-data = %q", got["meta-data"])
	}
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Errorf("leftover files: %v", entries)
	}
}

func TestMetaDataHostname(t *testing.T) {
	if m, err := MetaData("x1", "gpu-box"); err != nil || !strings.Contains(m, "local-hostname: gpu-box\n") {
		t.Fatalf("%q, %v", m, err)
	}
	for _, h := range []string{"Bad_Name", "-lead", "a b", strings.Repeat("a", 64)} {
		if _, err := MetaData("x1", h); err == nil {
			t.Errorf("hostname %q accepted", h)
		}
	}
}
