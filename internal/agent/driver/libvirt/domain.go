// Package libvirt is the Linux driver (ADR-0003, ADR-0019): one libvirt/QEMU
// domain per instance, driven through the pure-Go go-libvirt binding, with
// optional VFIO passthrough of NVIDIA GPUs. Spikes S1 and S2a ran this
// arrangement on Fedora.
//
// Ownership (SPEC §7.8): a domain is Kondukter's when its name is
// "kdk-<id>" and the <metadata> of its definition carries a kondukter
// instance element whose id is <id>. Every other domain on the host,
// including a "kdk-" one without the marker, is never listed, changed or
// deleted.
package libvirt

import (
	"bytes"
	_ "embed"
	"encoding/xml"
	"fmt"
	"regexp"
	"strconv"
	"text/template"

	"github.com/St1viG/cj/internal/agent/driver"
)

// MetadataNS is the XML namespace of the marker element in a domain's
// <metadata>.
const MetadataNS = "urn:kondukter:instance:1"

// NetworkName is the libvirt network the guests attach to.
const NetworkName = "kdk-net"

//go:embed templates/domain.xml.tmpl
var domainTmpl string

var (
	domainT = template.Must(template.New("domain").Funcs(template.FuncMap{"esc": xmlEscape}).Parse(domainTmpl))
	idRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,62}$`)
	pciRe   = regexp.MustCompile(`^([0-9a-f]{4}):([0-9a-f]{2}):([0-9a-f]{2})\.([0-7])$`)
)

func xmlEscape(s string) (string, error) {
	var b bytes.Buffer
	if err := xml.EscapeText(&b, []byte(s)); err != nil {
		return "", err
	}
	return b.String(), nil
}

// DomainName is the libvirt domain name of a Kondukter instance.
func DomainName(id string) string { return driver.NamePrefix + id }

// PCIAddr is a PCI function address.
type PCIAddr struct {
	Domain, Bus, Slot, Function int
}

// ParsePCI parses "0000:26:00.0".
func ParsePCI(s string) (PCIAddr, error) {
	m := pciRe.FindStringSubmatch(s)
	if m == nil {
		return PCIAddr{}, fmt.Errorf("libvirt: PCI address %q: want 0000:26:00.0", s)
	}
	var a PCIAddr
	for i, dst := range []*int{&a.Domain, &a.Bus, &a.Slot, &a.Function} {
		v, err := strconv.ParseInt(m[i+1], 16, 32)
		if err != nil {
			return PCIAddr{}, err
		}
		*dst = int(v)
	}
	return a, nil
}

// String is the canonical "0000:26:00.0" form.
func (a PCIAddr) String() string {
	return fmt.Sprintf("%04x:%02x:%02x.%x", a.Domain, a.Bus, a.Slot, a.Function)
}

// DomainParams are the values of the domain template.
type DomainParams struct {
	ID         string
	Generation uint64
	VCPU       int
	RAMMiB     int64
	// Disk is the instance's qcow2 overlay, Seed its NoCloud ISO.
	Disk, Seed string
	// Network is the libvirt network to attach to; empty means NetworkName.
	Network  string
	Hostdevs []PCIAddr
}

// RenderDomain returns the domain XML for p: q35, OVMF with libvirt's
// per-VM NVRAM, host-passthrough CPU, a virtio-blk overlay with discard,
// virtio-net, the seed ISO as CD-ROM, a serial console and nothing else: no
// graphics, USB or sound.
func RenderDomain(p DomainParams) (string, error) {
	if !idRe.MatchString(p.ID) {
		return "", fmt.Errorf("libvirt: instance id %q: want letters, digits and dashes, at most 63", p.ID)
	}
	if p.VCPU < 1 || p.RAMMiB < 1 {
		return "", fmt.Errorf("libvirt: %d vCPU, %d MiB: every size must be positive", p.VCPU, p.RAMMiB)
	}
	if p.Disk == "" || p.Seed == "" {
		return "", fmt.Errorf("libvirt: disk and seed paths are required")
	}
	if p.Network == "" {
		p.Network = NetworkName
	}
	data := struct {
		DomainParams
		Name, MetadataNS string
	}{p, DomainName(p.ID), MetadataNS}
	var b bytes.Buffer
	if err := domainT.Execute(&b, data); err != nil {
		return "", err
	}
	return b.String(), nil
}

// Marker is what the metadata of a Kondukter domain says.
type Marker struct {
	ID         string
	Generation uint64
}

// ParseMarker reads the Kondukter marker from a domain definition. ok is
// false for a domain that is not Kondukter's: no marker, or a marker whose
// id does not match the domain's name.
func ParseMarker(domainXML string) (m Marker, ok bool) {
	var d struct {
		Name     string `xml:"name"`
		Metadata struct {
			Instance struct {
				XMLName    xml.Name
				ID         string `xml:"id,attr"`
				Generation string `xml:"generation,attr"`
			} `xml:"urn:kondukter:instance:1 instance"`
		} `xml:"metadata"`
	}
	if err := xml.Unmarshal([]byte(domainXML), &d); err != nil {
		return Marker{}, false
	}
	in := d.Metadata.Instance
	if in.ID == "" || d.Name != DomainName(in.ID) {
		return Marker{}, false
	}
	g, _ := strconv.ParseUint(in.Generation, 10, 64)
	return Marker{ID: in.ID, Generation: g}, true
}
