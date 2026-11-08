package store

import (
	"encoding/json"
	"fmt"
)

// InstanceSpec is the instances.spec jsonb document: everything the agent
// needs that is fixed at create time. Concrete accelerators are not here;
// they come from the instance's active allocations.
type InstanceSpec struct {
	VCPU          int        `json:"vcpu"`
	RAMMiB        int64      `json:"ram_mib"`
	DiskGiB       int64      `json:"disk_gib"`
	Image         ImageRef   `json:"image"`
	SSHPublicKeys []string   `json:"ssh_public_keys"`
	UserData      string     `json:"user_data,omitempty"`
	Ports         []PortSpec `json:"ports,omitempty"`
	Hostname      string     `json:"hostname,omitempty"`
}

// ImageRef is the image snapshot taken at create time.
type ImageRef struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Arch      string `json:"arch"`
	Digest    string `json:"digest"`
	URL       string `json:"url"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
	Format    string `json:"format"`
}

// PortSpec is a guest port the customer asked to expose.
type PortSpec struct {
	GuestPort int    `json:"guest_port"`
	Protocol  string `json:"protocol"` // "tcp" | "udp"
}

// Connection is the instances.connection jsonb document, filled in from the
// agent's port mappings once the instance runs (ADR-0016).
type Connection struct {
	Host         string        `json:"host,omitempty"`
	SSHPort      int           `json:"ssh_port,omitempty"`
	SSHUser      string        `json:"ssh_user,omitempty"`
	SSHCommand   string        `json:"ssh_command,omitempty"`
	PortMappings []PortMapping `json:"port_mappings,omitempty"`
	GuestIPs     []string      `json:"guest_ips,omitempty"`
}

// PortMapping is where a guest port is reachable.
type PortMapping struct {
	GuestPort   int    `json:"guest_port"`
	Protocol    string `json:"protocol"`
	HostAddress string `json:"host_address"`
	HostPort    int    `json:"host_port"`
}

// DefaultSSHUser is the login user of the curated images (ADR-0015).
const DefaultSSHUser = "ubuntu"

// ParseSpec decodes instances.spec.
func ParseSpec(raw []byte) (InstanceSpec, error) {
	var s InstanceSpec
	if err := json.Unmarshal(raw, &s); err != nil {
		return s, fmt.Errorf("store: instance spec: %w", err)
	}
	return s, nil
}

// MustJSON marshals v, which must be one of the store's document types.
func MustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("store: marshal %T: %v", v, err))
	}
	return b
}

// ParseConnection decodes instances.connection; an empty document is the
// zero Connection.
func ParseConnection(raw []byte) (Connection, error) {
	var c Connection
	if len(raw) == 0 {
		return c, nil
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("store: instance connection: %w", err)
	}
	return c, nil
}
