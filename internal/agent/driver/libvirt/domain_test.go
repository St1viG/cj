package libvirt

import (
	"strings"
	"testing"
)

func params() DomainParams {
	return DomainParams{ID: "abc123", Generation: 7, VCPU: 4, RAMMiB: 8192,
		Disk: "/data/instances/abc123/disk.qcow2", Seed: "/data/instances/abc123/seed.iso"}
}

func TestRenderDomain(t *testing.T) {
	x, err := RenderDomain(params())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"<name>kdk-abc123</name>",
		"<memory unit='MiB'>8192</memory>", "<vcpu>4</vcpu>",
		"machine='q35'", "firmware='efi'", "mode='host-passthrough'",
		"discard='unmap'", "bus='virtio'", "network='kdk-net'",
		"<source file='/data/instances/abc123/disk.qcow2'/>",
		"<source file='/data/instances/abc123/seed.iso'/>",
		"<serial type='pty'/>",
	} {
		if !strings.Contains(x, want) {
			t.Errorf("domain XML lacks %q", want)
		}
	}
	for _, banned := range []string{"<graphics", "<video", "<sound", "<usb", "<hostdev"} {
		if strings.Contains(x, banned) {
			t.Errorf("domain XML has %q, which a Kondukter guest must not", banned)
		}
	}
}

func TestRenderDomainHostdevs(t *testing.T) {
	p := params()
	for _, s := range []string{"0000:26:00.0", "0000:26:00.1"} {
		a, err := ParsePCI(s)
		if err != nil {
			t.Fatal(err)
		}
		p.Hostdevs = append(p.Hostdevs, a)
	}
	x, err := RenderDomain(p)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(x, "<hostdev mode='subsystem' type='pci' managed='yes'>"); n != 2 {
		t.Fatalf("%d hostdev elements, want 2", n)
	}
	for _, want := range []string{
		"domain='0x0000' bus='0x26' slot='0x00' function='0x0'",
		"domain='0x0000' bus='0x26' slot='0x00' function='0x1'",
	} {
		if !strings.Contains(x, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestRenderDomainEscapesPaths(t *testing.T) {
	p := params()
	p.Disk = "/data/it's/<x>&.qcow2"
	x, err := RenderDomain(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(x, "<x>") || strings.Contains(x, "it's") {
		t.Fatalf("path not escaped:\n%s", x)
	}
	if _, ok := ParseMarker(x); !ok {
		t.Fatal("escaped domain no longer parses")
	}
}

func TestRenderDomainRejects(t *testing.T) {
	for name, mut := range map[string]func(*DomainParams){
		"empty id":      func(p *DomainParams) { p.ID = "" },
		"id with quote": func(p *DomainParams) { p.ID = "a'b" },
		"id with space": func(p *DomainParams) { p.ID = "a b" },
		"long id":       func(p *DomainParams) { p.ID = strings.Repeat("a", 64) },
		"no vcpu":       func(p *DomainParams) { p.VCPU = 0 },
		"no ram":        func(p *DomainParams) { p.RAMMiB = 0 },
		"no disk":       func(p *DomainParams) { p.Disk = "" },
	} {
		p := params()
		mut(&p)
		if _, err := RenderDomain(p); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestParseMarker(t *testing.T) {
	x, _ := RenderDomain(params())
	m, ok := ParseMarker(x)
	if !ok || m.ID != "abc123" || m.Generation != 7 {
		t.Fatalf("ParseMarker = %+v, %v", m, ok)
	}
	for name, xml := range map[string]string{
		"no marker":     "<domain><name>kdk-abc123</name></domain>",
		"name mismatch": strings.Replace(x, "<name>kdk-abc123</name>", "<name>kdk-other</name>", 1),
		"no prefix":     strings.Replace(x, "<name>kdk-abc123</name>", "<name>abc123</name>", 1),
		"wrong ns":      strings.Replace(x, MetadataNS, "urn:someone:else", 1),
		"garbage":       "not xml",
	} {
		if m, ok := ParseMarker(xml); ok {
			t.Errorf("%s: owned as %+v", name, m)
		}
	}
}

func TestParsePCI(t *testing.T) {
	a, err := ParsePCI("0000:26:00.1")
	if err != nil || a != (PCIAddr{0, 0x26, 0, 1}) || a.String() != "0000:26:00.1" {
		t.Fatalf("ParsePCI = %+v, %v (%s)", a, err, a)
	}
	for _, bad := range []string{"", "26:00.0", "0000:26:00.8", "0000:xx:00.0", "0000:26:00.0 "} {
		if _, err := ParsePCI(bad); err == nil {
			t.Errorf("ParsePCI(%q) succeeded", bad)
		}
	}
}
