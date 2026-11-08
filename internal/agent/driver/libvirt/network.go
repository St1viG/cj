package libvirt

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"net/netip"

	golibvirt "github.com/digitalocean/go-libvirt"
)

// normalize fills defaults and checks the network.
func (n *Network) normalize() error {
	if n.Name == "" {
		n.Name = DefaultNetwork
	}
	if n.Bridge == "" {
		return errors.New("a bridge is required")
	}
	if !n.Subnet.IsValid() || !n.Subnet.Addr().Is4() || n.Subnet.Masked() != n.Subnet || n.Subnet.Bits() > 29 {
		return fmt.Errorf("subnet %s: want an IPv4 network of at least /29", n.Subnet)
	}
	if !n.Gateway.IsValid() {
		n.Gateway = n.Subnet.Addr().Next()
	}
	first, last := hosts(n.Subnet)
	if n.Gateway.Less(first) || last.Less(n.Gateway) {
		return fmt.Errorf("gateway %s is not a host address of %s", n.Gateway, n.Subnet)
	}
	if !n.DNS.IsValid() {
		n.DNS = n.Gateway
	}
	return nil
}

// hosts returns the first and last host address of an IPv4 subnet.
func hosts(p netip.Prefix) (first, last netip.Addr) {
	a := p.Addr().As4()
	n := binary.BigEndian.Uint32(a[:])
	n |= 1<<(32-p.Bits()) - 1 // broadcast
	binary.BigEndian.PutUint32(a[:], n-1)
	return p.Addr().Next(), netip.AddrFrom4(a)
}

// dhcpRange is the larger side of the subnet's hosts around the gateway.
func (n Network) dhcpRange() (start, end netip.Addr) {
	first, last := hosts(n.Subnet)
	switch n.Gateway {
	case first:
		return first.Next(), last
	case last:
		return first, last.Prev()
	}
	// A gateway mid-subnet: hand out the addresses above it.
	return n.Gateway.Next(), last
}

// RenderNetwork returns the network XML (ADR-0016): open forward mode, so
// libvirt adds no firewall rules of its own and the agent's nftables table
// does all filtering and NAT; libvirt's dnsmasq serves DHCP and DNS on the
// gateway. A DNS address other than the gateway is handed out as the DHCP
// DNS option.
func RenderNetwork(n Network) (string, error) {
	if err := n.normalize(); err != nil {
		return "", fmt.Errorf("libvirt: network: %w", err)
	}
	start, end := n.dhcpRange()
	var opt string
	if n.DNS != n.Gateway {
		opt = "dhcp-option=option:dns-server," + n.DNS.String()
	}
	var b bytes.Buffer
	err := templates.ExecuteTemplate(&b, "network.xml.tmpl", map[string]any{
		"Name": n.Name, "Bridge": n.Bridge, "Gateway": n.Gateway, "Prefix": n.Subnet.Bits(),
		"First": start, "Last": end, "DNSOption": opt,
	})
	if err != nil {
		return "", fmt.Errorf("libvirt: render network: %w", err)
	}
	return b.String(), nil
}

// networkXML is the part of a network definition the driver checks.
type networkXML struct {
	Forward struct {
		Mode string `xml:"mode,attr"`
	} `xml:"forward"`
	Bridge struct {
		Name string `xml:"name,attr"`
	} `xml:"bridge"`
	IPs []struct {
		Address string `xml:"address,attr"`
		Prefix  string `xml:"prefix,attr"`
		Netmask string `xml:"netmask,attr"`
	} `xml:"ip"`
}

// ensureNetwork defines, autostarts and starts the guest network if it is
// missing or inactive. An existing network must use the configured bridge
// and gateway, which the agent's firewall rules name; it is never
// redefined.
func (d *Driver) ensureNetwork(ctx context.Context) error {
	d.netmu.Lock()
	defer d.netmu.Unlock()
	n := d.opts.Network
	return d.call(ctx, func(c Conn) error {
		net, err := c.NetworkLookupByName(n.Name)
		switch {
		case isCode(err, golibvirt.ErrNoNetwork):
			def, err := RenderNetwork(n)
			if err != nil {
				return final(err)
			}
			if net, err = c.NetworkDefineXML(def); err != nil {
				return fmt.Errorf("libvirt: define network %s: %w", n.Name, err)
			}
			if err := c.NetworkSetAutostart(net, 1); err != nil {
				return fmt.Errorf("libvirt: autostart network %s: %w", n.Name, err)
			}
			d.opts.Log.Info("libvirt: defined the guest network", "network", n.Name, "bridge", n.Bridge, "subnet", n.Subnet)
		case err != nil:
			return fmt.Errorf("libvirt: look up network %s: %w", n.Name, err)
		default:
			raw, err := c.NetworkGetXMLDesc(net, 0)
			if err != nil {
				return fmt.Errorf("libvirt: network %s: %w", n.Name, err)
			}
			if err := d.checkNetwork(raw); err != nil {
				return final(err)
			}
		}
		active, err := c.NetworkIsActive(net)
		if err != nil {
			return fmt.Errorf("libvirt: network %s: %w", n.Name, err)
		}
		if active == 0 {
			if err := c.NetworkCreate(net); err != nil && !isCode(err, golibvirt.ErrOperationInvalid) {
				return fmt.Errorf("libvirt: start network %s: %w", n.Name, err)
			}
		}
		return nil
	})
}

// checkNetwork compares an existing network with the configuration.
func (d *Driver) checkNetwork(raw string) error {
	n := d.opts.Network
	var got networkXML
	if err := xml.Unmarshal([]byte(raw), &got); err != nil {
		return fmt.Errorf("libvirt: network %s: %w", n.Name, err)
	}
	if got.Bridge.Name != n.Bridge {
		return fmt.Errorf("libvirt: network %s uses bridge %q, the agent config says %q; fix one of them", n.Name, got.Bridge.Name, n.Bridge)
	}
	want := netip.PrefixFrom(n.Gateway, n.Subnet.Bits())
	for _, ip := range got.IPs {
		if p, err := ipPrefix(ip.Address, ip.Prefix, ip.Netmask); err == nil && p == want {
			if got.Forward.Mode != "open" {
				d.opts.Log.Warn("libvirt: the guest network is not in open forward mode; libvirt's own firewall rules may interfere with the port map",
					"network", n.Name, "mode", got.Forward.Mode)
			}
			return nil
		}
	}
	return fmt.Errorf("libvirt: network %s has no address %s, which the agent config says; fix one of them", n.Name, want)
}

func ipPrefix(addr, prefix, netmask string) (netip.Prefix, error) {
	a, err := netip.ParseAddr(addr)
	if err != nil {
		return netip.Prefix{}, err
	}
	if prefix != "" {
		return netip.ParsePrefix(addr + "/" + prefix)
	}
	if netmask == "" {
		return netip.Prefix{}, errors.New("no prefix")
	}
	m, err := netip.ParseAddr(netmask)
	if err != nil || !m.Is4() {
		return netip.Prefix{}, fmt.Errorf("netmask %q", netmask)
	}
	b := m.As4()
	bits := 0
	for _, x := range b {
		for x&0x80 != 0 {
			bits++
			x <<= 1
		}
	}
	return netip.PrefixFrom(a, bits), nil
}
