package libvirt

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/St1viG/cj/internal/agent/driver"
)

// MaxUserData caps the customer's cloud-init user-data, as the API does.
const MaxUserData = 16 << 10

var (
	idRe       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,62}$`)
	hostnameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	digestRe   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	// imagePartRe keeps image names and versions safe as file names.
	imagePartRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

// Validate checks that the spec is something this driver can run.
func Validate(spec driver.InstanceSpec, hostArch string) error {
	img := spec.Image
	switch {
	case !idRe.MatchString(spec.ID):
		return fmt.Errorf("libvirt: instance id %q: want letters, digits and dashes, at most 63", spec.ID)
	case hostArch != "amd64":
		return fmt.Errorf("%w: libvirt nodes run amd64 (q35) guests only; this node is %s", driver.ErrUnsupported, hostArch)
	case img.Arch != "" && img.Arch != hostArch:
		return fmt.Errorf("%w: image %s is %s, this node is %s", driver.ErrUnsupported, img.Name, img.Arch, hostArch)
	case !imagePartRe.MatchString(img.Name) || !imagePartRe.MatchString(img.Version):
		return fmt.Errorf("libvirt: image %q version %q: want letters, digits, '.', '_' and '-'", img.Name, img.Version)
	case !strings.HasPrefix(img.URL, "https://") && !strings.HasPrefix(img.URL, "http://"):
		return fmt.Errorf("libvirt: image %s: URL %q is not http(s)", img.Name, img.URL)
	case !digestRe.MatchString(img.Digest):
		return fmt.Errorf("libvirt: image %s: digest %q is not sha256:<hex>", img.Name, img.Digest)
	case img.Format != "" && img.Format != "qcow2" && img.Format != "raw":
		return fmt.Errorf("%w: image %s is %s; want qcow2 or raw", driver.ErrUnsupported, img.Name, img.Format)
	case spec.VCPU < 1 || spec.RAMMiB < 1 || spec.DiskGiB < 1:
		return fmt.Errorf("libvirt: %d vCPU, %d MiB, %d GiB: every size must be positive", spec.VCPU, spec.RAMMiB, spec.DiskGiB)
	case spec.Hostname != "" && !hostnameRe.MatchString(spec.Hostname):
		return fmt.Errorf("libvirt: hostname %q is not a DNS label", spec.Hostname)
	case len(spec.UserData) > MaxUserData:
		return fmt.Errorf("libvirt: user-data is %d bytes, at most %d", len(spec.UserData), MaxUserData)
	}
	if _, err := userDataType(spec.UserData); err != nil {
		return err
	}
	for i, k := range spec.SSHKeys {
		if strings.TrimSpace(k) == "" || strings.ContainsAny(k, "\r\n") {
			return fmt.Errorf("libvirt: SSH key %d is not one non-empty line", i+1)
		}
	}
	seen := map[string]bool{}
	for _, id := range spec.AcceleratorIDs {
		if _, err := ParsePCIAddress(id); err != nil {
			return fmt.Errorf("%w: accelerator %q: %w", driver.ErrUnsupported, id, err)
		}
		if seen[id] {
			return fmt.Errorf("libvirt: accelerator %s is listed twice", id)
		}
		seen[id] = true
	}
	return nil
}

// userDataType returns the MIME type of customer user-data. Cloud-config
// must be a YAML mapping; scripts and boothooks pass through as they are.
func userDataType(b []byte) (string, error) {
	switch {
	case len(b) == 0:
		return "", nil
	case bytes.HasPrefix(b, []byte("#cloud-config")):
		var m map[string]any
		if err := yaml.Unmarshal(b, &m); err != nil {
			return "", fmt.Errorf("libvirt: user-data is not a cloud-config mapping: %w", err)
		}
		return "text/cloud-config", nil
	case bytes.HasPrefix(b, []byte("#cloud-boothook")):
		return "text/cloud-boothook", nil
	case bytes.HasPrefix(b, []byte("#!")):
		return "text/x-shellscript", nil
	}
	return "", fmt.Errorf("%w: user-data must start with #cloud-config, #! or #cloud-boothook", driver.ErrUnsupported)
}
