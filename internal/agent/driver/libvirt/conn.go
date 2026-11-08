package libvirt

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	golibvirt "github.com/digitalocean/go-libvirt"

	"github.com/St1viG/cj/internal/agent/driver"
)

// DefaultSocket is libvirt's read-write system socket: virtproxyd's on
// Fedora's modular daemons, libvirtd's elsewhere. Either serves
// qemu:///system and forwards network calls to the network driver.
const DefaultSocket = "/run/libvirt/libvirt-sock"

const (
	dialTimeout = 5 * time.Second
	// disconnectGrace is how long a failed call waits for the client to
	// notice that libvirtd went away before it gives up on a retry.
	disconnectGrace = time.Second
	// Backoff between attempts to re-subscribe to lifecycle events.
	minBackoff = time.Second
	maxBackoff = 30 * time.Second
)

// Conn is the part of the libvirt RPC client the driver uses.
// *golibvirt.Libvirt implements it; tests use a fake.
type Conn interface {
	ConnectListAllDomains(needResults int32, flags golibvirt.ConnectListAllDomainsFlags) ([]golibvirt.Domain, uint32, error)
	DomainLookupByName(name string) (golibvirt.Domain, error)
	DomainGetState(dom golibvirt.Domain, flags uint32) (int32, int32, error)
	DomainGetMetadata(dom golibvirt.Domain, typ int32, uri golibvirt.OptString, flags golibvirt.DomainModificationImpact) (string, error)
	DomainDefineXMLFlags(xml string, flags golibvirt.DomainDefineFlags) (golibvirt.Domain, error)
	DomainCreateWithFlags(dom golibvirt.Domain, flags uint32) (golibvirt.Domain, error)
	DomainDestroyFlags(dom golibvirt.Domain, flags golibvirt.DomainDestroyFlagsValues) error
	DomainUndefineFlags(dom golibvirt.Domain, flags golibvirt.DomainUndefineFlagsValues) error
	DomainInterfaceAddresses(dom golibvirt.Domain, source uint32, flags uint32) ([]golibvirt.DomainInterface, error)
	NetworkLookupByName(name string) (golibvirt.Network, error)
	NetworkGetXMLDesc(net golibvirt.Network, flags uint32) (string, error)
	NetworkDefineXML(xml string) (golibvirt.Network, error)
	NetworkCreate(net golibvirt.Network) error
	NetworkIsActive(net golibvirt.Network) (int32, error)
	NetworkSetAutostart(net golibvirt.Network, autostart int32) error
	NetworkGetDhcpLeases(net golibvirt.Network, mac golibvirt.OptString, needResults int32, flags uint32) ([]golibvirt.NetworkDhcpLease, uint32, error)
	LifecycleEvents(ctx context.Context) (<-chan golibvirt.DomainEventLifecycleMsg, error)
	// Disconnected is closed once the connection is gone.
	Disconnected() <-chan struct{}
	Disconnect() error
}

var _ Conn = (*golibvirt.Libvirt)(nil)

// DialFunc opens a connection to qemu:///system.
type DialFunc func(ctx context.Context) (Conn, error)

// DialSocket speaks libvirt's RPC protocol over the unix socket at path.
func DialSocket(path string) DialFunc {
	return DialWith(func(ctx context.Context) (net.Conn, error) {
		d := net.Dialer{Timeout: dialTimeout}
		return d.DialContext(ctx, "unix", path)
	})
}

// DialWith speaks libvirt's RPC protocol over connections from dial; tests
// pass go-libvirt's libvirttest mock server.
func DialWith(dial func(ctx context.Context) (net.Conn, error)) DialFunc {
	return func(ctx context.Context) (Conn, error) {
		l := golibvirt.NewWithDialer(dialerFunc(func() (net.Conn, error) { return dial(ctx) }))
		if err := l.ConnectToURI(golibvirt.QEMUSystem); err != nil {
			return nil, err
		}
		return l, nil
	}
}

type dialerFunc func() (net.Conn, error)

func (f dialerFunc) Dial() (net.Conn, error) { return f() }

// conn returns the live connection, dialing a new one when there is none
// yet or the last one dropped (libvirtd restarted). New does not dial, so
// the agent starts, and reports its inventory, while libvirtd is down.
func (d *Driver) conn(ctx context.Context) (Conn, error) {
	d.cmu.Lock()
	defer d.cmu.Unlock()
	if d.c != nil {
		select {
		case <-d.c.Disconnected():
			d.opts.Log.Warn("libvirt: connection lost; reconnecting")
			d.c = nil
		default:
			return d.c, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c, err := d.opts.Dial(ctx)
	if err != nil {
		return nil, errors.Join(errors.New("libvirt: connect to qemu:///system"), err)
	}
	d.c = c
	return c, nil
}

// call runs fn, a sequence of libvirt calls, on the connection. When fn
// fails because the connection dropped under it, call reconnects and runs
// fn once more; every fn is safe to repeat. An error libvirt itself
// returned, or one fn marked final, is returned at once.
func (d *Driver) call(ctx context.Context, fn func(Conn) error) error {
	c, err := d.conn(ctx)
	if err != nil {
		return err
	}
	err = fn(c)
	if err == nil || isLibvirtError(err) || isFinal(err) || ctx.Err() != nil {
		return err
	}
	select {
	case <-c.Disconnected():
	case <-time.After(disconnectGrace):
		return err
	case <-ctx.Done():
		return err
	}
	if c, err = d.conn(ctx); err != nil {
		return err
	}
	return fn(c)
}

// Close stops the event watcher and closes the connection.
func (d *Driver) Close() error {
	d.stopWatch()
	d.cmu.Lock()
	defer d.cmu.Unlock()
	if d.c == nil {
		return nil
	}
	c := d.c
	d.c = nil
	select {
	case <-c.Disconnected():
		return nil
	default:
		return c.Disconnect()
	}
}

// watch turns libvirt lifecycle events of Kondukter domains into driver
// events until ctx ends, re-subscribing with backoff whenever the
// connection drops.
func (d *Driver) watch(ctx context.Context) {
	backoff := minBackoff
	for ctx.Err() == nil {
		err := d.subscribe(ctx, func() { backoff = minBackoff })
		if ctx.Err() != nil {
			return
		}
		d.opts.Log.Warn("libvirt: lifecycle events stopped; re-subscribing", "err", err, "in", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(2*backoff, maxBackoff)
	}
}

// subscribe forwards events until the subscription ends.
func (d *Driver) subscribe(ctx context.Context, subscribed func()) error {
	c, err := d.conn(ctx)
	if err != nil {
		return err
	}
	ch, err := c.LifecycleEvents(ctx)
	if err != nil {
		return err
	}
	subscribed()
	// Read until the channel closes, even after ctx ends: go-libvirt's
	// sender blocks on an unread event.
	for ev := range ch {
		name := ev.Dom.Name
		if !strings.HasPrefix(name, driver.NamePrefix) || !idRe.MatchString(name[len(driver.NamePrefix):]) {
			continue
		}
		// The event only triggers a pass; ownership is checked there.
		d.notify(name[len(driver.NamePrefix):])
	}
	return errors.New("libvirt: connection closed")
}

// notify fires a driver event without blocking: a full channel already
// guarantees a reconcile pass.
func (d *Driver) notify(id string) {
	select {
	case d.events <- driver.Event{InstanceID: id, At: d.opts.Now()}:
	default:
	}
}

// finalError marks an error of the driver's own making inside a call, such
// as its refusal to touch a foreign domain. Like an error libvirtd
// returned, it says nothing about the connection, so call does not wait
// for a disconnect to retry.
type finalError struct{ error }

func (e finalError) Unwrap() error { return e.error }

func final(err error) error {
	if err == nil {
		return nil
	}
	return finalError{err}
}

func isFinal(err error) bool {
	var f finalError
	return errors.As(err, &f)
}

// isLibvirtError reports whether libvirtd answered with an error, as
// opposed to the call failing on the way.
func isLibvirtError(err error) bool {
	var le golibvirt.Error
	return errors.As(err, &le)
}

// isCode reports whether libvirtd answered with error number code.
func isCode(err error, code golibvirt.ErrorNumber) bool {
	var le golibvirt.Error
	return errors.As(err, &le) && int64(le.Code) == int64(code)
}
