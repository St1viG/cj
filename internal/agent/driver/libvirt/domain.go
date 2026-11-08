package libvirt

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
	"text/template"
)

//go:embed templates/*.xml.tmpl
var templateFS embed.FS

var templates = template.Must(template.New("").
	Funcs(template.FuncMap{"xml": xmlEscape}).
	ParseFS(templateFS, "templates/*.xml.tmpl"))

// DomainParams are the inputs of templates/domain.xml.tmpl.
type DomainParams struct {
	Name       string
	ID         string
	Generation uint64
	VCPU       int
	RAMMiB     int64
	// Disk, Seed and NVRAM are absolute paths of the overlay, the NoCloud
	// ISO and the UEFI variables; libvirt copies OVMF's variable template
	// to NVRAM on the first start.
	Disk, Seed, NVRAM string
	MAC               string
	Network           string
	// Hostdevs are the PCI functions to pass through.
	Hostdevs []PCIAddress
}

// RenderDomain returns the domain XML (SPEC §9.3): q35 with UEFI (OVMF
// picked by libvirt's firmware autoselection, Secure Boot off, NVRAM per
// VM), host-passthrough CPU, the overlay on virtio-blk with discard, the
// seed as a read-only SATA CD-ROM, virtio-net on the guest network, a
// serial console and nothing else: no graphics, video, sound, USB or
// balloon (VFIO pins guest memory anyway). Each hostdev is a managed VFIO
// passthrough: libvirt binds it to vfio-pci, resets it and returns it.
func RenderDomain(p DomainParams) (string, error) {
	switch {
	case !strings.HasPrefix(p.Disk, "/") || !strings.HasPrefix(p.Seed, "/") || !strings.HasPrefix(p.NVRAM, "/"):
		return "", fmt.Errorf("libvirt: domain %s: file paths must be absolute", p.Name)
	case p.VCPU < 1 || p.RAMMiB < 1:
		return "", fmt.Errorf("libvirt: domain %s: %d vCPU, %d MiB", p.Name, p.VCPU, p.RAMMiB)
	}
	var b bytes.Buffer
	err := templates.ExecuteTemplate(&b, "domain.xml.tmpl", struct {
		DomainParams
		NS string
	}{p, MetadataNS})
	if err != nil {
		return "", fmt.Errorf("libvirt: render domain: %w", err)
	}
	return b.String(), nil
}

// Metadata is the ownership element in a domain's <metadata>.
type Metadata struct {
	XMLName    xml.Name `xml:"kondukter://libvirt/instance instance"`
	ID         string   `xml:"kondukter://libvirt/instance id"`
	Generation uint64   `xml:"kondukter://libvirt/instance generation"`
}

// ParseMetadata parses the element as virDomainGetMetadata returns it.
func ParseMetadata(raw string) (Metadata, error) {
	var m Metadata
	if err := xml.Unmarshal([]byte(raw), &m); err != nil {
		return Metadata{}, err
	}
	if m.ID == "" {
		return Metadata{}, fmt.Errorf("no instance id in %q", raw)
	}
	return m, nil
}

// PCIAddress is a PCI function: domain:bus:slot.function.
type PCIAddress struct {
	Domain   uint16
	Bus      uint8
	Slot     uint8
	Function uint8
}

// ParsePCIAddress parses sysfs's "0000:01:00.0".
func ParsePCIAddress(s string) (PCIAddress, error) {
	var p PCIAddress
	dom, rest, ok1 := strings.Cut(s, ":")
	bus, rest, ok2 := strings.Cut(rest, ":")
	slot, fn, ok3 := strings.Cut(rest, ".")
	if !ok1 || !ok2 || !ok3 || len(dom) != 4 || len(bus) != 2 || len(slot) != 2 || len(fn) != 1 {
		return p, fmt.Errorf("PCI address %q: want dddd:bb:ss.f", s)
	}
	d, err1 := strconv.ParseUint(dom, 16, 16)
	b, err2 := strconv.ParseUint(bus, 16, 8)
	sl, err3 := strconv.ParseUint(slot, 16, 8)
	f, err4 := strconv.ParseUint(fn, 16, 8)
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || sl > 0x1f || f > 7 {
		return p, fmt.Errorf("PCI address %q: want dddd:bb:ss.f", s)
	}
	return PCIAddress{Domain: uint16(d), Bus: uint8(b), Slot: uint8(sl), Function: uint8(f)}, nil
}

func (p PCIAddress) String() string {
	return fmt.Sprintf("%04x:%02x:%02x.%x", p.Domain, p.Bus, p.Slot, p.Function)
}

// MAC is the guest NIC's address: QEMU's 52:54:00 prefix and three bytes of
// the id's hash, stable across re-creates, so the network's lease for it
// is found without reading the domain XML.
func MAC(id string) string {
	h := sha256.Sum256([]byte(id))
	return fmt.Sprintf("52:54:00:%02x:%02x:%02x", h[0], h[1], h[2])
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s)) // strings.Builder never fails
	return b.String()
}
