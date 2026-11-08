package libvirt

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	golibvirt "github.com/digitalocean/go-libvirt"
	"github.com/digitalocean/go-libvirt/socket/dialers"
)

// API is the part of libvirt the driver uses. The real implementation talks
// to the daemons through go-libvirt; the tests use a fake.
type API interface {
	// Domains returns the defined domains whose name starts with prefix,
	// with their definitions. Other domains are never read.
	Domains(ctx context.Context, prefix string) ([]DomainInfo, error)
	// Domain returns one domain by name; ok is false if it is not defined.
	Domain(ctx context.Context, name string) (info DomainInfo, ok bool, err error)
	// Define defines (or redefines) a persistent domain from XML.
	Define(ctx context.Context, xml string) error
	// Start boots a defined domain.
	Start(ctx context.Context, name string) error
	// Destroy powers a domain off; a domain that is not running is fine.
	Destroy(ctx context.Context, name string) error
	// Undefine removes the definition together with the domain's NVRAM; an
	// undefined domain is fine.
	Undefine(ctx context.Context, name string) error
	// GuestIPs returns the IPv4 addresses the DHCP server leased to a
	// running domain.
	GuestIPs(ctx context.Context, name string) ([]string, error)
	// EnsureNetwork defines the network from XML if it is missing and
	// starts it if it is inactive.
	EnsureNetwork(ctx context.Context, name, xml string) error
	// Events reports domain lifecycle changes until ctx ends or the
	// connection drops, when the channel closes.
	Events(ctx context.Context) (<-chan LifecycleEvent, error)
	Close() error
}

// DomainInfo is a defined domain.
type DomainInfo struct {
	Name string
	// Running means the domain is active: started and not shut off.
	Running bool
	XML     string
}

// LifecycleEvent says something happened to a domain: started, stopped,
// crashed, defined or undefined.
type LifecycleEvent struct {
	Name string
}

// Socket paths of the modular daemons (Fedora) and of the monolithic one.
const (
	QEMUSocket    = "/run/libvirt/virtqemud-sock"
	NetworkSocket = "/run/libvirt/virtnetworkd-sock"
	LegacySocket  = "/run/libvirt/libvirt-sock"
)

// Dial connects to libvirt. Empty paths pick the modular daemons' sockets,
// or the monolithic libvirtd's when those do not exist. The qemu driver and
// the network driver are separate daemons on modular hosts (spike S1), so
// there are two connections.
func Dial(qemuSock, networkSock string) (API, error) {
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	if qemuSock == "" {
		qemuSock = QEMUSocket
		if !exists(qemuSock) && exists(LegacySocket) {
			qemuSock = LegacySocket
		}
	}
	if networkSock == "" {
		networkSock = NetworkSocket
		if !exists(networkSock) && exists(LegacySocket) {
			networkSock = LegacySocket
		}
	}
	a := &goAPI{
		qemu: &conn{sock: qemuSock, uri: golibvirt.QEMUSystem},
		net:  &conn{sock: networkSock, uri: golibvirt.ConnectURI("network:///system")},
	}
	if networkSock == LegacySocket {
		a.net.uri = golibvirt.QEMUSystem
	}
	if err := a.qemu.ensure(); err != nil {
		return nil, err
	}
	return a, nil
}

// conn is one go-libvirt connection that reconnects after it drops.
type conn struct {
	sock string
	uri  golibvirt.ConnectURI

	mu sync.Mutex
	l  *golibvirt.Libvirt
}

func (c *conn) ensure() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.l != nil && c.l.IsConnected() {
		return nil
	}
	l := golibvirt.NewWithDialer(dialers.NewLocal(dialers.WithSocket(c.sock)))
	if err := l.ConnectToURI(c.uri); err != nil {
		return fmt.Errorf("libvirt: connect %s: %w", c.sock, err)
	}
	c.l = l
	return nil
}

func (c *conn) get() *golibvirt.Libvirt {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.l
}

// do runs fn on a live connection, reconnecting once if the connection
// dropped under it. Errors from libvirt itself are returned as they are.
func (c *conn) do(ctx context.Context, fn func(*golibvirt.Libvirt) error) error {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := c.ensure(); err != nil {
			return err
		}
		l := c.get()
		err := fn(l)
		if err == nil || attempt > 0 || l.IsConnected() {
			return err
		}
	}
}

func (c *conn) close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.l == nil {
		return nil
	}
	err := c.l.Disconnect()
	c.l = nil
	return err
}

type goAPI struct {
	qemu, net *conn
}

func (a *goAPI) Close() error { return errors.Join(a.qemu.close(), a.net.close()) }

func isMissing(err error) bool {
	var e golibvirt.Error
	return errors.As(err, &e) && (e.Code == uint32(golibvirt.ErrNoDomain) || e.Code == uint32(golibvirt.ErrNoNetwork))
}

func (a *goAPI) Domains(ctx context.Context, prefix string) ([]DomainInfo, error) {
	var out []DomainInfo
	err := a.qemu.do(ctx, func(l *golibvirt.Libvirt) error {
		out = out[:0]
		doms, _, err := l.ConnectListAllDomains(1, 0)
		if err != nil {
			return err
		}
		for _, d := range doms {
			if !strings.HasPrefix(d.Name, prefix) {
				continue
			}
			info, err := describe(l, d)
			if isMissing(err) {
				continue // undefined between the list and the read
			}
			if err != nil {
				return err
			}
			out = append(out, info)
		}
		return nil
	})
	return out, err
}

func describe(l *golibvirt.Libvirt, d golibvirt.Domain) (DomainInfo, error) {
	st, _, err := l.DomainGetState(d, 0)
	if err != nil {
		return DomainInfo{}, err
	}
	x, err := l.DomainGetXMLDesc(d, 0)
	if err != nil {
		return DomainInfo{}, err
	}
	return DomainInfo{Name: d.Name, Running: st != int32(golibvirt.DomainShutoff), XML: x}, nil
}

func (a *goAPI) Domain(ctx context.Context, name string) (info DomainInfo, ok bool, err error) {
	err = a.qemu.do(ctx, func(l *golibvirt.Libvirt) error {
		d, err := l.DomainLookupByName(name)
		if isMissing(err) {
			ok = false
			return nil
		}
		if err != nil {
			return err
		}
		if info, err = describe(l, d); err != nil {
			return err
		}
		ok = true
		return nil
	})
	return info, ok, err
}

func (a *goAPI) Define(ctx context.Context, xml string) error {
	return a.qemu.do(ctx, func(l *golibvirt.Libvirt) error {
		_, err := l.DomainDefineXMLFlags(xml, 0)
		return err
	})
}

func (a *goAPI) with(ctx context.Context, name string, fn func(*golibvirt.Libvirt, golibvirt.Domain) error) error {
	return a.qemu.do(ctx, func(l *golibvirt.Libvirt) error {
		d, err := l.DomainLookupByName(name)
		if err != nil {
			return err
		}
		return fn(l, d)
	})
}

func (a *goAPI) Start(ctx context.Context, name string) error {
	return a.with(ctx, name, func(l *golibvirt.Libvirt, d golibvirt.Domain) error { return l.DomainCreate(d) })
}

func (a *goAPI) Destroy(ctx context.Context, name string) error {
	err := a.with(ctx, name, func(l *golibvirt.Libvirt, d golibvirt.Domain) error {
		st, _, err := l.DomainGetState(d, 0)
		if err != nil || st == int32(golibvirt.DomainShutoff) {
			return err
		}
		return l.DomainDestroy(d)
	})
	if isMissing(err) {
		return nil
	}
	return err
}

func (a *goAPI) Undefine(ctx context.Context, name string) error {
	err := a.with(ctx, name, func(l *golibvirt.Libvirt, d golibvirt.Domain) error {
		return l.DomainUndefineFlags(d, golibvirt.DomainUndefineNvram|golibvirt.DomainUndefineManagedSave|golibvirt.DomainUndefineSnapshotsMetadata)
	})
	if isMissing(err) {
		return nil
	}
	return err
}

func (a *goAPI) GuestIPs(ctx context.Context, name string) ([]string, error) {
	var ips []string
	err := a.with(ctx, name, func(l *golibvirt.Libvirt, d golibvirt.Domain) error {
		ips = ips[:0]
		ifs, err := l.DomainInterfaceAddresses(d, uint32(golibvirt.DomainInterfaceAddressesSrcLease), 0)
		if err != nil {
			return err
		}
		for _, ifc := range ifs {
			for _, ad := range ifc.Addrs {
				if ad.Type == int32(golibvirt.IPAddrTypeIpv4) {
					ips = append(ips, ad.Addr)
				}
			}
		}
		return nil
	})
	return ips, err
}

func (a *goAPI) EnsureNetwork(ctx context.Context, name, xml string) error {
	return a.net.do(ctx, func(l *golibvirt.Libvirt) error {
		n, err := l.NetworkLookupByName(name)
		if isMissing(err) {
			n, err = l.NetworkDefineXML(xml)
		}
		if err != nil {
			return err
		}
		active, err := l.NetworkIsActive(n)
		if err != nil || active != 0 {
			return err
		}
		return l.NetworkCreate(n)
	})
}

func (a *goAPI) Events(ctx context.Context) (<-chan LifecycleEvent, error) {
	if err := a.qemu.ensure(); err != nil {
		return nil, err
	}
	raw, err := a.qemu.get().LifecycleEvents(ctx)
	if err != nil {
		return nil, err
	}
	out := make(chan LifecycleEvent, 16)
	go func() {
		defer close(out)
		for ev := range raw {
			select {
			case out <- LifecycleEvent{Name: ev.Dom.Name}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}
