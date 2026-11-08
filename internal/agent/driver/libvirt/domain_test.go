package libvirt_test

import (
	"encoding/xml"
	"flag"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/St1viG/cj/internal/agent/driver/libvirt"
)

var update = flag.Bool("update", false, "rewrite golden files")

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("output differs from %s (rerun with -update):\n%s", path, got)
	}
}

func domainParams() libvirt.DomainParams {
	dir := "/var/lib/kondukter-agent/instances/" + webID
	return libvirt.DomainParams{
		Name: "kdk-" + webID, ID: webID, Generation: 3, VCPU: 4, RAMMiB: 8192,
		Disk: dir + "/disk.qcow2", Seed: dir + "/seed.iso", NVRAM: dir + "/nvram",
		MAC: libvirt.MAC(webID), Network: "kdk-net",
	}
}

// domainDoc is what the tests check in a domain definition.
type domainDoc struct {
	Type   string `xml:"type,attr"`
	Name   string `xml:"name"`
	Memory struct {
		Unit  string `xml:"unit,attr"`
		Value int64  `xml:",chardata"`
	} `xml:"memory"`
	VCPU int `xml:"vcpu"`
	OS   struct {
		Firmware string `xml:"firmware,attr"`
		Type     struct {
			Arch    string `xml:"arch,attr"`
			Machine string `xml:"machine,attr"`
		} `xml:"type"`
		NVRAM string `xml:"nvram"`
	} `xml:"os"`
	CPU struct {
		Mode string `xml:"mode,attr"`
	} `xml:"cpu"`
	Metadata struct {
		Inner string `xml:",innerxml"`
	} `xml:"metadata"`
	Disks []struct {
		Device string `xml:"device,attr"`
		Driver struct {
			Type    string `xml:"type,attr"`
			Discard string `xml:"discard,attr"`
			Cache   string `xml:"cache,attr"`
		} `xml:"driver"`
		Source struct {
			File string `xml:"file,attr"`
		} `xml:"source"`
		Target struct {
			Dev string `xml:"dev,attr"`
			Bus string `xml:"bus,attr"`
		} `xml:"target"`
		ReadOnly *struct{} `xml:"readonly"`
	} `xml:"devices>disk"`
	Interfaces []struct {
		Type string `xml:"type,attr"`
		MAC  struct {
			Address string `xml:"address,attr"`
		} `xml:"mac"`
		Source struct {
			Network string `xml:"network,attr"`
		} `xml:"source"`
		Model struct {
			Type string `xml:"type,attr"`
		} `xml:"model"`
	} `xml:"devices>interface"`
	Serials     []struct{} `xml:"devices>serial"`
	Consoles    []struct{} `xml:"devices>console"`
	Controllers []struct {
		Type  string `xml:"type,attr"`
		Model string `xml:"model,attr"`
	} `xml:"devices>controller"`
	Graphics []struct{} `xml:"devices>graphics"`
	Video    []struct{} `xml:"devices>video"`
	Sound    []struct{} `xml:"devices>sound"`
	Hostdevs []struct {
		Managed string `xml:"managed,attr"`
	} `xml:"devices>hostdev"`
}

func TestRenderDomainGolden(t *testing.T) {
	for _, tc := range []struct {
		file     string
		hostdevs []libvirt.PCIAddress
	}{
		{"domain.xml", nil},
		{"domain-gpu.xml", []libvirt.PCIAddress{{Bus: 1}, {Bus: 1, Function: 1}}},
	} {
		t.Run(tc.file, func(t *testing.T) {
			p := domainParams()
			p.Hostdevs = tc.hostdevs
			got, err := libvirt.RenderDomain(p)
			if err != nil {
				t.Fatal(err)
			}
			golden(t, tc.file, got)

			var doc domainDoc
			if err := xml.Unmarshal([]byte(got), &doc); err != nil {
				t.Fatalf("not well-formed: %v", err)
			}
			switch {
			case doc.Type != "kvm" || doc.Name != p.Name:
				t.Errorf("domain type %q name %q", doc.Type, doc.Name)
			case doc.OS.Type.Machine != "q35" || doc.OS.Type.Arch != "x86_64" || doc.OS.Firmware != "efi" || doc.OS.NVRAM != p.NVRAM:
				t.Errorf("os = %+v, want q35 with OVMF and per-VM NVRAM", doc.OS)
			case doc.CPU.Mode != "host-passthrough":
				t.Errorf("cpu mode %q", doc.CPU.Mode)
			case doc.Memory.Unit != "MiB" || doc.Memory.Value != 8192 || doc.VCPU != 4:
				t.Errorf("memory %+v, vcpu %d", doc.Memory, doc.VCPU)
			}
			if len(doc.Disks) != 2 {
				t.Fatalf("%d disks", len(doc.Disks))
			}
			if d := doc.Disks[0]; d.Device != "disk" || d.Driver.Type != "qcow2" || d.Driver.Discard != "unmap" || d.Target.Bus != "virtio" || d.Source.File != p.Disk || d.Driver.Cache != "" {
				t.Errorf("root disk = %+v, want the qcow2 overlay on virtio-blk with discard", d)
			}
			if d := doc.Disks[1]; d.Device != "cdrom" || d.ReadOnly == nil || d.Source.File != p.Seed || d.Driver.Type != "raw" {
				t.Errorf("seed = %+v, want a read-only CD-ROM", d)
			}
			if len(doc.Interfaces) != 1 || doc.Interfaces[0].Type != "network" || doc.Interfaces[0].Source.Network != "kdk-net" ||
				doc.Interfaces[0].Model.Type != "virtio" || doc.Interfaces[0].MAC.Address != p.MAC {
				t.Errorf("interfaces = %+v", doc.Interfaces)
			}
			if len(doc.Serials) != 1 || len(doc.Consoles) != 1 || len(doc.Graphics)+len(doc.Video)+len(doc.Sound) != 0 {
				t.Errorf("want a serial console and no graphics, video or sound")
			}
			if len(doc.Controllers) != 1 || doc.Controllers[0].Type != "usb" || doc.Controllers[0].Model != "none" {
				t.Errorf("controllers = %+v, want USB off", doc.Controllers)
			}
			if len(doc.Hostdevs) != len(tc.hostdevs) {
				t.Errorf("%d hostdevs, want %d", len(doc.Hostdevs), len(tc.hostdevs))
			}
			for _, h := range doc.Hostdevs {
				if h.Managed != "yes" {
					t.Errorf("hostdev not managed")
				}
			}
			m, err := libvirt.ParseMetadata(strings.TrimSpace(doc.Metadata.Inner))
			if err != nil || m.ID != webID || m.Generation != 3 {
				t.Fatalf("metadata = %+v, %v", m, err)
			}
		})
	}
}

func TestRenderDomainEscapesAndRefuses(t *testing.T) {
	p := domainParams()
	p.Disk = "/data/a&b/disk'.qcow2"
	got, err := libvirt.RenderDomain(p)
	if err != nil {
		t.Fatal(err)
	}
	var doc domainDoc
	if err := xml.Unmarshal([]byte(got), &doc); err != nil || doc.Disks[0].Source.File != p.Disk {
		t.Fatalf("disk path %q, %v", doc.Disks[0].Source.File, err)
	}
	p = domainParams()
	p.NVRAM = "nvram"
	if _, err := libvirt.RenderDomain(p); err == nil {
		t.Fatal("a relative path was accepted")
	}
	p = domainParams()
	p.VCPU = 0
	if _, err := libvirt.RenderDomain(p); err == nil {
		t.Fatal("0 vCPU was accepted")
	}
}

func TestParseMetadata(t *testing.T) {
	for _, raw := range []string{
		// As defined, and as libvirt may return it, with a default namespace.
		"<kdk:instance xmlns:kdk='kondukter://libvirt/instance'><kdk:id>abc</kdk:id><kdk:generation>7</kdk:generation></kdk:instance>",
		"<instance xmlns=\"kondukter://libvirt/instance\">\n  <id>abc</id>\n  <generation>7</generation>\n</instance>",
	} {
		m, err := libvirt.ParseMetadata(raw)
		if err != nil || m.ID != "abc" || m.Generation != 7 {
			t.Errorf("ParseMetadata(%q) = %+v, %v", raw, m, err)
		}
	}
	for _, raw := range []string{
		"<instance><id>abc</id></instance>", // no namespace
		"<kdk:instance xmlns:kdk='kondukter://other'><kdk:id>abc</kdk:id></kdk:instance>",
		"<kdk:instance xmlns:kdk='kondukter://libvirt/instance'><kdk:generation>1</kdk:generation></kdk:instance>",
		"<kdk:instance xmlns:kdk='kondukter://libvirt/instance'><kdk:id>",
	} {
		if m, err := libvirt.ParseMetadata(raw); err == nil {
			t.Errorf("ParseMetadata(%q) = %+v, want an error", raw, m)
		}
	}
}

func TestParsePCIAddress(t *testing.T) {
	for s, want := range map[string]libvirt.PCIAddress{
		"0000:01:00.0": {Bus: 1},
		"0000:01:00.1": {Bus: 1, Function: 1},
		"0001:c1:1f.7": {Domain: 1, Bus: 0xc1, Slot: 0x1f, Function: 7},
	} {
		got, err := libvirt.ParsePCIAddress(s)
		if err != nil || got != want || got.String() != s {
			t.Errorf("ParsePCIAddress(%q) = %+v (%s), %v", s, got, got, err)
		}
	}
	for _, s := range []string{"", "01:00.0", "0000:01:00", "0000:01:20.0", "0000:01:00.8", "0000:0g:00.0", "GPU-0"} {
		if _, err := libvirt.ParsePCIAddress(s); err == nil {
			t.Errorf("ParsePCIAddress(%q) succeeded", s)
		}
	}
}

func TestMAC(t *testing.T) {
	a, b := libvirt.MAC(webID), libvirt.MAC(otherID)
	if a != libvirt.MAC(webID) || a == b || !strings.HasPrefix(a, "52:54:00:") || len(a) != 17 {
		t.Fatalf("MAC = %s, %s", a, b)
	}
}

// networkDoc is what the tests check in a network definition.
type networkDoc struct {
	Name    string `xml:"name"`
	Forward struct {
		Mode string `xml:"mode,attr"`
	} `xml:"forward"`
	Bridge struct {
		Name string `xml:"name,attr"`
	} `xml:"bridge"`
	IP struct {
		Address string `xml:"address,attr"`
		Prefix  string `xml:"prefix,attr"`
		Range   struct {
			Start string `xml:"start,attr"`
			End   string `xml:"end,attr"`
		} `xml:"dhcp>range"`
	} `xml:"ip"`
	Options []struct {
		Value string `xml:"value,attr"`
	} `xml:"http://libvirt.org/schemas/network/dnsmasq/1.0 options>option"`
}

func TestRenderNetwork(t *testing.T) {
	subnet := netip.MustParsePrefix("10.77.0.0/24")
	for _, tc := range []struct {
		name                 string
		n                    libvirt.Network
		gateway, first, last string
		option               string
	}{
		{"default", libvirt.Network{Bridge: "kdk-br0", Subnet: subnet}, "10.77.0.1", "10.77.0.2", "10.77.0.254", ""},
		{"last gateway", libvirt.Network{Bridge: "kdk-br0", Subnet: subnet, Gateway: netip.MustParseAddr("10.77.0.254")}, "10.77.0.254", "10.77.0.1", "10.77.0.253", ""},
		{"public dns", libvirt.Network{Bridge: "kdk-br0", Subnet: subnet, DNS: netip.MustParseAddr("9.9.9.9")}, "10.77.0.1", "10.77.0.2", "10.77.0.254", "dhcp-option=option:dns-server,9.9.9.9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := libvirt.RenderNetwork(tc.n)
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "default" {
				golden(t, "network.xml", raw)
			}
			var doc networkDoc
			if err := xml.Unmarshal([]byte(raw), &doc); err != nil {
				t.Fatal(err)
			}
			if doc.Name != libvirt.DefaultNetwork || doc.Forward.Mode != "open" || doc.Bridge.Name != "kdk-br0" ||
				doc.IP.Address != tc.gateway || doc.IP.Prefix != "24" || doc.IP.Range.Start != tc.first || doc.IP.Range.End != tc.last {
				t.Fatalf("network = %+v", doc)
			}
			if (tc.option == "") != (len(doc.Options) == 0) || (tc.option != "" && doc.Options[0].Value != tc.option) {
				t.Fatalf("dnsmasq options = %+v, want %q", doc.Options, tc.option)
			}
		})
	}
	for _, n := range []libvirt.Network{
		{Subnet: subnet},
		{Bridge: "kdk-br0", Subnet: netip.MustParsePrefix("10.77.0.1/24")},
		{Bridge: "kdk-br0", Subnet: netip.MustParsePrefix("10.77.0.0/30")},
		{Bridge: "kdk-br0", Subnet: netip.MustParsePrefix("fd00::/64")},
		{Bridge: "kdk-br0", Subnet: subnet, Gateway: netip.MustParseAddr("10.77.0.255")},
		{Bridge: "kdk-br0", Subnet: subnet, Gateway: netip.MustParseAddr("10.78.0.1")},
	} {
		if _, err := libvirt.RenderNetwork(n); err == nil {
			t.Errorf("RenderNetwork(%+v) succeeded", n)
		}
	}
}
