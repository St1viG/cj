// Package config loads the kondukter-agent configuration file.
//
// Default locations: /etc/kondukter-agent/config.yaml on Linux and
// /Library/Application Support/kondukter-agent/config.yaml on macOS. Unknown
// keys are rejected so a typo never silently falls back to a default.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"runtime"
	"slices"
	"time"

	"go.yaml.in/yaml/v3"
)

// Config is the agent's configuration.
type Config struct {
	// Server is the Kondukter API URL used by `enroll`,
	// e.g. "http://kondukter.tailnet.ts.net:8080".
	Server string `yaml:"server"`
	// Gateway is host:port of the mTLS agent gateway; `enroll` writes it.
	Gateway string `yaml:"gateway"`
	// Driver is "mock", "libvirt" or "lima".
	Driver string `yaml:"driver"`
	// DataDir holds the local store, image cache and instance disks.
	DataDir string `yaml:"data_dir"`
	// Reserved is held back for the host and never offered to guests.
	Reserved Reserved `yaml:"reserved"`
	// PortRange is the host port range for port-map access (ADR-0016).
	PortRange PortRange `yaml:"port_range"`
	// AdvertiseAddress is where customers reach mapped ports: the node's
	// tailnet IP in the demo.
	AdvertiseAddress string `yaml:"advertise_address"`
	// Labels are free-form scheduling hints reported with the inventory.
	Labels map[string]string `yaml:"labels"`
	// MaxInstances caps concurrent instances; 0 means unlimited.
	MaxInstances int `yaml:"max_instances"`
	// LogLevel is "debug", "info", "warn" or "error".
	LogLevel string `yaml:"log_level"`
	// Network is the guest network on Linux nodes: libvirt's kdk-net, whose
	// DNAT and isolation rules the agent owns (ADR-0016).
	Network Network `yaml:"network"`
	// Mock configures the mock driver.
	Mock Mock `yaml:"mock"`
}

// Network is the libvirt guest network.
type Network struct {
	// Bridge is the host bridge of kdk-net.
	Bridge string `yaml:"bridge"`
	// Subnet is kdk-net's IPv4 subnet.
	Subnet string `yaml:"subnet"`
	// Gateway is the bridge's address; it defaults to the subnet's first
	// address.
	Gateway string `yaml:"gateway"`
	// DNS is the resolver guests use; it defaults to the gateway.
	DNS string `yaml:"dns"`
}

// Parse returns the network's subnet, gateway and DNS address.
func (n Network) Parse() (subnet netip.Prefix, gateway, dns netip.Addr, err error) {
	if n.Bridge == "" {
		return subnet, gateway, dns, errors.New("bridge is required")
	}
	if subnet, err = netip.ParsePrefix(n.Subnet); err != nil || !subnet.Addr().Is4() || subnet.Masked() != subnet {
		return subnet, gateway, dns, fmt.Errorf("subnet %q: want an IPv4 network such as 10.77.0.0/24", n.Subnet)
	}
	gateway = subnet.Addr().Next()
	if n.Gateway != "" {
		if gateway, err = netip.ParseAddr(n.Gateway); err != nil {
			return subnet, gateway, dns, fmt.Errorf("gateway: %w", err)
		}
	}
	if !subnet.Contains(gateway) {
		return subnet, gateway, dns, fmt.Errorf("gateway %s is outside %s", gateway, subnet)
	}
	dns = gateway
	if n.DNS != "" {
		if dns, err = netip.ParseAddr(n.DNS); err != nil {
			return subnet, gateway, dns, fmt.Errorf("dns: %w", err)
		}
	}
	return subnet, gateway, dns, nil
}

// Reserved resources stay with the host.
type Reserved struct {
	VCPU    int   `yaml:"vcpu"`
	RAMMiB  int64 `yaml:"ram_mib"`
	DiskGiB int64 `yaml:"disk_gib"`
}

// PortRange is an inclusive host port range.
type PortRange struct {
	First int `yaml:"first"`
	Last  int `yaml:"last"`
}

// Size is the number of ports in the range.
func (r PortRange) Size() int { return r.Last - r.First + 1 }

// Mock configures the simulated host of the mock driver.
type Mock struct {
	Arch         string            `yaml:"arch"`
	Hostname     string            `yaml:"hostname"`
	VCPU         int               `yaml:"vcpu"`
	RAMMiB       int64             `yaml:"ram_mib"`
	DiskGiB      int64             `yaml:"disk_gib"`
	Accelerators []MockAccelerator `yaml:"accelerators"`
	// ProvisionDelay is how long a new instance stays provisioning.
	ProvisionDelay time.Duration `yaml:"provision_delay"`
	// FailureRate in [0, 1] is the chance that provisioning fails.
	FailureRate float64 `yaml:"failure_rate"`
}

// MockAccelerator is a simulated device.
type MockAccelerator struct {
	ID     string `yaml:"id"`
	Kind   string `yaml:"kind"`
	Model  string `yaml:"model"`
	MemMiB int64  `yaml:"mem_mib"`
}

// DefaultPath is the platform's config file location.
func DefaultPath() string {
	if runtime.GOOS == "darwin" {
		return "/Library/Application Support/kondukter-agent/config.yaml"
	}
	return "/etc/kondukter-agent/config.yaml"
}

// DefaultDataDir is the platform's data directory (ADR-0018).
func DefaultDataDir() string {
	if runtime.GOOS == "darwin" {
		return "/usr/local/var/lib/kondukter-agent"
	}
	return "/var/lib/kondukter-agent"
}

// Default returns a configuration with every default applied.
func Default() Config {
	return Config{
		DataDir:   DefaultDataDir(),
		PortRange: PortRange{First: 40000, Last: 49999},
		Network:   Network{Bridge: "kdk-br0", Subnet: "10.77.0.0/24"},
		LogLevel:  "info",
	}
}

// Load reads, defaults and validates the file at path.
func Load(path string) (Config, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the operator chooses the config path
	if err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	cfg, err := Parse(b)
	if err != nil {
		return Config{}, fmt.Errorf("config: %s: %w", path, err)
	}
	return cfg, nil
}

// Parse decodes YAML over Default() and validates the result.
func Parse(b []byte) (Config, error) {
	cfg := Default()
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	// An empty file decodes to io.EOF and leaves the defaults.
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate reports the first invalid setting.
func (c Config) Validate() error {
	switch c.Driver {
	case "mock", "libvirt", "lima":
	case "":
		return errors.New("driver is required (mock, libvirt or lima)")
	default:
		return fmt.Errorf("driver %q: want mock, libvirt or lima", c.Driver)
	}
	if c.DataDir == "" {
		return errors.New("data_dir is required")
	}
	if c.Reserved.VCPU < 0 || c.Reserved.RAMMiB < 0 || c.Reserved.DiskGiB < 0 {
		return errors.New("reserved resources must not be negative")
	}
	if c.PortRange.First < 1024 || c.PortRange.Last > 65535 || c.PortRange.First > c.PortRange.Last {
		return fmt.Errorf("port_range %d-%d: want 1024 <= first <= last <= 65535", c.PortRange.First, c.PortRange.Last)
	}
	if c.AdvertiseAddress != "" {
		if _, err := netip.ParseAddr(c.AdvertiseAddress); err != nil {
			return fmt.Errorf("advertise_address: %w", err)
		}
	}
	if c.MaxInstances < 0 {
		return errors.New("max_instances must not be negative")
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("log_level %q: want debug, info, warn or error", c.LogLevel)
	}
	if c.Driver == "libvirt" {
		if _, _, _, err := c.Network.Parse(); err != nil {
			return fmt.Errorf("network: %w", err)
		}
	}
	if c.Driver == "mock" {
		if err := c.Mock.validate(); err != nil {
			return fmt.Errorf("mock: %w", err)
		}
	}
	return nil
}

func (m Mock) validate() error {
	switch m.Arch {
	case "amd64", "arm64":
	default:
		return fmt.Errorf("arch %q: want amd64 or arm64", m.Arch)
	}
	if m.VCPU <= 0 || m.RAMMiB <= 0 || m.DiskGiB <= 0 {
		return errors.New("vcpu, ram_mib and disk_gib must be positive")
	}
	if m.FailureRate < 0 || m.FailureRate > 1 {
		return fmt.Errorf("failure_rate %v: want 0..1", m.FailureRate)
	}
	seen := map[string]bool{}
	for i, a := range m.Accelerators {
		if a.ID == "" || a.Model == "" {
			return fmt.Errorf("accelerators[%d]: id and model are required", i)
		}
		if seen[a.ID] {
			return fmt.Errorf("accelerators[%d]: duplicate id %q", i, a.ID)
		}
		seen[a.ID] = true
	}
	return nil
}

// SetKeys sets top-level string keys in the YAML file at path, keeping
// comments and the order of everything else, and writes it atomically.
func SetKeys(path string, kv map[string]string) error {
	raw, err := os.ReadFile(path) //nolint:gosec // the operator chooses the config path
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("config: %s: %w", path, err)
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("config: %s: top level is not a mapping", path)
	}
	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		set := false
		for i := 0; i+1 < len(root.Content); i += 2 {
			if root.Content[i].Value == k {
				root.Content[i+1] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: kv[k]}
				set = true
				break
			}
		}
		if !set {
			root.Content = append(root.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k},
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: kv[k]})
		}
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	if _, err := Parse(buf.Bytes()); err != nil {
		return fmt.Errorf("config: refusing to write an invalid config: %w", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), fi.Mode().Perm()); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
