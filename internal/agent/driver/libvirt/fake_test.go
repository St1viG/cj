package libvirt_test

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sync"

	golibvirt "github.com/digitalocean/go-libvirt"

	"github.com/St1viG/cj/internal/agent/driver/libvirt"
)

// errGone is how a call fails once the connection dropped: not a
// golibvirt.Error, as libvirtd never answered.
var errGone = errors.New("fake libvirt: connection closed")

func lverr(code golibvirt.ErrorNumber, msg string) error {
	return golibvirt.Error{Code: uint32(code), Message: msg}
}

// fakeLibvirt is libvirtd with its QEMU and network drivers, in memory. It
// writes what libvirt writes on a host: a running domain's pid file and,
// from its first start, its NVRAM file. Domains survive kill, as VMs
// survive a libvirtd restart.
type fakeLibvirt struct {
	pidDir string

	mu      sync.Mutex
	doms    map[string]*fakeDom
	nets    map[string]*fakeNet
	calls   map[string]int
	dials   int
	dialErr error
	conns   []*fakeConn
	subs    map[*fakeSub]bool
	nextIP  int
	// dropOn drops the connection when that call arrives, once.
	dropOn string
	// noDomainLeases fails DomainInterfaceAddresses, as for a domain
	// whose interface libvirt cannot map, so the driver asks the network.
	noDomainLeases bool
	// noPid makes libvirt write no pid file.
	noPid bool
}

type fakeDom struct {
	name, xml, meta, mac, nvram string
	state                       golibvirt.DomainState
	reason                      int32
	// ip is the guest's DHCP lease; empty means none yet.
	ip string
	// lease false holds back the lease the next start would give.
	noLease bool
}

type fakeNet struct {
	xml       string
	active    bool
	autostart bool
}

type fakeSub struct {
	ch chan golibvirt.DomainEventLifecycleMsg
}

func newFakeLibvirt(pidDir string) *fakeLibvirt {
	return &fakeLibvirt{
		pidDir: pidDir,
		doms:   map[string]*fakeDom{},
		nets:   map[string]*fakeNet{},
		calls:  map[string]int{},
		subs:   map[*fakeSub]bool{},
	}
}

// dial is the driver's DialFunc.
func (f *fakeLibvirt) dial(context.Context) (libvirt.Conn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dials++
	if f.dialErr != nil {
		return nil, f.dialErr
	}
	c := &fakeConn{f: f, gone: make(chan struct{})}
	f.conns = append(f.conns, c)
	return c, nil
}

// kill drops every connection, as a libvirtd restart does.
func (f *fakeLibvirt) kill() {
	f.mu.Lock()
	conns := slices.Clone(f.conns)
	f.mu.Unlock()
	for _, c := range conns {
		c.close()
	}
}

func (f *fakeLibvirt) set(fn func(f *fakeLibvirt)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *fakeLibvirt) count(call string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[call]
}

// mutations counts the calls that change a domain.
func (f *fakeLibvirt) mutations() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls["DomainDefineXMLFlags"] + f.calls["DomainCreateWithFlags"] + f.calls["DomainDestroyFlags"] + f.calls["DomainUndefineFlags"]
}

func (f *fakeLibvirt) dialCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.dials
}

func (f *fakeLibvirt) subscribers() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.subs)
}

// add defines a domain directly, as an operator's virsh define would.
func (f *fakeLibvirt) add(name, meta string, state golibvirt.DomainState, reason int32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.doms[name] = &fakeDom{name: name, xml: "<domain><name>" + name + "</name></domain>", meta: meta, state: state, reason: reason}
}

// update changes a domain; the guest powering off, say.
func (f *fakeLibvirt) update(name string, fn func(d *fakeDom)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d := f.doms[name]
	if d == nil {
		panic("fake libvirt: no domain " + name)
	}
	fn(d)
}

// stop turns a running domain off with reason, as QEMU exiting does.
func (f *fakeLibvirt) stop(name string, state golibvirt.DomainState, reason int32) {
	f.update(name, func(d *fakeDom) {
		d.state, d.reason = state, reason
		_ = os.Remove(filepath.Join(f.pidDir, name+".pid"))
	})
}

func (f *fakeLibvirt) domain(name string) (fakeDom, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d := f.doms[name]
	if d == nil {
		return fakeDom{}, false
	}
	return *d, true
}

// snapshot is every domain's definition and state.
func (f *fakeLibvirt) snapshot() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]string{}
	for n, d := range f.doms {
		out[n] = fmt.Sprintf("%d/%d %s %s", d.state, d.reason, d.meta, d.xml)
	}
	return out
}

func (f *fakeLibvirt) network(name string) (fakeNet, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := f.nets[name]
	if n == nil {
		return fakeNet{}, false
	}
	return *n, true
}

func (f *fakeLibvirt) addNetwork(name, xml string, active bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nets[name] = &fakeNet{xml: xml, active: active, autostart: true}
}

// emit sends a lifecycle event to every subscriber.
func (f *fakeLibvirt) emit(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.emitLocked(name, golibvirt.DomainEventStarted)
}

func (f *fakeLibvirt) emitLocked(name string, ev golibvirt.DomainEventType) {
	for s := range f.subs {
		select {
		case s.ch <- golibvirt.DomainEventLifecycleMsg{Dom: golibvirt.Domain{Name: name}, Event: int32(ev)}:
		default:
		}
	}
}

// ownMeta is the ownership element as libvirt returns it for id.
func ownMeta(id string) string {
	return fmt.Sprintf("<kdk:instance xmlns:kdk=%q><kdk:id>%s</kdk:id><kdk:generation>1</kdk:generation></kdk:instance>", libvirt.MetadataNS, id)
}

var (
	nameRe  = regexp.MustCompile(`<name>([^<]+)</name>`)
	metaRe  = regexp.MustCompile(`(?s)<kdk:instance\b.*</kdk:instance>`)
	macRe   = regexp.MustCompile(`<mac address='([^']+)'/>`)
	nvramRe = regexp.MustCompile(`<nvram>([^<]+)</nvram>`)
)

func submatch(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

func wellFormed(s string) error {
	dec := xml.NewDecoder(bytes.NewReader([]byte(s)))
	for {
		_, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// fakeConn is one client connection.
type fakeConn struct {
	f    *fakeLibvirt
	gone chan struct{}
	once sync.Once
}

var _ libvirt.Conn = (*fakeConn)(nil)

func (c *fakeConn) close() { c.once.Do(func() { close(c.gone) }) }

// enter starts a call: it fails once the connection is gone, and otherwise
// returns with the fake's lock held.
func (c *fakeConn) enter(call string) (func(), error) {
	f := c.f
	f.mu.Lock()
	if f.dropOn == call {
		f.dropOn = ""
		c.close()
	}
	select {
	case <-c.gone:
		f.mu.Unlock()
		return nil, errGone
	default:
	}
	f.calls[call]++
	return f.mu.Unlock, nil
}

func (c *fakeConn) dom(name string) (*fakeDom, error) {
	d := c.f.doms[name]
	if d == nil {
		return nil, lverr(golibvirt.ErrNoDomain, "Domain not found: no domain with matching name '"+name+"'")
	}
	return d, nil
}

func (c *fakeConn) ConnectListAllDomains(int32, golibvirt.ConnectListAllDomainsFlags) ([]golibvirt.Domain, uint32, error) {
	unlock, err := c.enter("ConnectListAllDomains")
	if err != nil {
		return nil, 0, err
	}
	defer unlock()
	var out []golibvirt.Domain
	for n := range c.f.doms {
		out = append(out, golibvirt.Domain{Name: n})
	}
	slices.SortFunc(out, func(a, b golibvirt.Domain) int {
		switch {
		case a.Name < b.Name:
			return -1
		case a.Name > b.Name:
			return 1
		}
		return 0
	})
	return out, uint32(len(out)), nil
}

func (c *fakeConn) DomainLookupByName(name string) (golibvirt.Domain, error) {
	unlock, err := c.enter("DomainLookupByName")
	if err != nil {
		return golibvirt.Domain{}, err
	}
	defer unlock()
	if _, err := c.dom(name); err != nil {
		return golibvirt.Domain{}, err
	}
	return golibvirt.Domain{Name: name}, nil
}

func (c *fakeConn) DomainGetState(dom golibvirt.Domain, _ uint32) (int32, int32, error) {
	unlock, err := c.enter("DomainGetState")
	if err != nil {
		return 0, 0, err
	}
	defer unlock()
	d, err := c.dom(dom.Name)
	if err != nil {
		return 0, 0, err
	}
	return int32(d.state), d.reason, nil
}

func (c *fakeConn) DomainGetMetadata(dom golibvirt.Domain, typ int32, uri golibvirt.OptString, _ golibvirt.DomainModificationImpact) (string, error) {
	unlock, err := c.enter("DomainGetMetadata")
	if err != nil {
		return "", err
	}
	defer unlock()
	d, err := c.dom(dom.Name)
	if err != nil {
		return "", err
	}
	if typ != int32(golibvirt.DomainMetadataElement) || len(uri) != 1 {
		return "", lverr(golibvirt.ErrInvalidArg, "invalid argument: want an element and a namespace")
	}
	if uri[0] != libvirt.MetadataNS || d.meta == "" {
		return "", lverr(golibvirt.ErrNoDomainMetadata, "metadata not found: Requested metadata element is not present")
	}
	return d.meta, nil
}

func (c *fakeConn) DomainDefineXMLFlags(x string, flags golibvirt.DomainDefineFlags) (golibvirt.Domain, error) {
	unlock, err := c.enter("DomainDefineXMLFlags")
	if err != nil {
		return golibvirt.Domain{}, err
	}
	defer unlock()
	if err := wellFormed(x); err != nil {
		return golibvirt.Domain{}, lverr(golibvirt.ErrXMLError, "XML error: "+err.Error())
	}
	if flags&golibvirt.DomainDefineValidate == 0 {
		return golibvirt.Domain{}, lverr(golibvirt.ErrInvalidArg, "fake: the driver always asks libvirt to validate")
	}
	name := submatch(nameRe, x)
	d := c.f.doms[name]
	if d == nil {
		d = &fakeDom{name: name, state: golibvirt.DomainShutoff}
		c.f.doms[name] = d
	}
	d.xml, d.meta, d.mac, d.nvram = x, metaRe.FindString(x), submatch(macRe, x), submatch(nvramRe, x)
	c.f.emitLocked(name, golibvirt.DomainEventDefined)
	return golibvirt.Domain{Name: name}, nil
}

func (c *fakeConn) DomainCreateWithFlags(dom golibvirt.Domain, _ uint32) (golibvirt.Domain, error) {
	unlock, err := c.enter("DomainCreateWithFlags")
	if err != nil {
		return golibvirt.Domain{}, err
	}
	defer unlock()
	d, err := c.dom(dom.Name)
	if err != nil {
		return golibvirt.Domain{}, err
	}
	if d.state == golibvirt.DomainRunning || d.state == golibvirt.DomainPaused {
		return golibvirt.Domain{}, lverr(golibvirt.ErrOperationInvalid, "Requested operation is not valid: domain is already running")
	}
	if d.nvram != "" {
		if _, err := os.Stat(d.nvram); errors.Is(err, os.ErrNotExist) {
			// libvirt copies OVMF's variable template on the first start.
			if err := os.WriteFile(d.nvram, []byte("OVMF_VARS"), 0o600); err != nil {
				return golibvirt.Domain{}, lverr(golibvirt.ErrOperationFailed, err.Error())
			}
		}
	}
	if !c.f.noPid {
		if err := os.WriteFile(filepath.Join(c.f.pidDir, d.name+".pid"), []byte("4242\n"), 0o600); err != nil {
			return golibvirt.Domain{}, lverr(golibvirt.ErrOperationFailed, err.Error())
		}
	}
	d.state, d.reason = golibvirt.DomainRunning, int32(golibvirt.DomainRunningBooted)
	if d.ip == "" && !d.noLease {
		d.ip = fmt.Sprintf("10.77.0.%d", 10+c.f.nextIP)
		c.f.nextIP++
	}
	c.f.emitLocked(d.name, golibvirt.DomainEventStarted)
	return dom, nil
}

func (c *fakeConn) DomainDestroyFlags(dom golibvirt.Domain, _ golibvirt.DomainDestroyFlagsValues) error {
	unlock, err := c.enter("DomainDestroyFlags")
	if err != nil {
		return err
	}
	defer unlock()
	d, err := c.dom(dom.Name)
	if err != nil {
		return err
	}
	if d.state == golibvirt.DomainShutoff || d.state == golibvirt.DomainCrashed {
		return lverr(golibvirt.ErrOperationInvalid, "Requested operation is not valid: domain is not running")
	}
	d.state, d.reason = golibvirt.DomainShutoff, int32(golibvirt.DomainShutoffDestroyed)
	_ = os.Remove(filepath.Join(c.f.pidDir, d.name+".pid"))
	c.f.emitLocked(d.name, golibvirt.DomainEventStopped)
	return nil
}

func (c *fakeConn) DomainUndefineFlags(dom golibvirt.Domain, flags golibvirt.DomainUndefineFlagsValues) error {
	unlock, err := c.enter("DomainUndefineFlags")
	if err != nil {
		return err
	}
	defer unlock()
	d, err := c.dom(dom.Name)
	if err != nil {
		return err
	}
	if d.state != golibvirt.DomainShutoff && d.state != golibvirt.DomainCrashed {
		return lverr(golibvirt.ErrOperationInvalid, "fake: the driver destroys a domain before it undefines it")
	}
	if d.nvram != "" {
		if _, err := os.Stat(d.nvram); err == nil {
			if flags&golibvirt.DomainUndefineNvram == 0 {
				return lverr(golibvirt.ErrOperationInvalid, "Requested operation is not valid: cannot undefine domain with nvram")
			}
			if err := os.Remove(d.nvram); err != nil {
				return lverr(golibvirt.ErrOperationFailed, err.Error())
			}
		}
	}
	delete(c.f.doms, d.name)
	c.f.emitLocked(d.name, golibvirt.DomainEventUndefined)
	return nil
}

func (c *fakeConn) DomainInterfaceAddresses(dom golibvirt.Domain, source, _ uint32) ([]golibvirt.DomainInterface, error) {
	unlock, err := c.enter("DomainInterfaceAddresses")
	if err != nil {
		return nil, err
	}
	defer unlock()
	d, err := c.dom(dom.Name)
	if err != nil {
		return nil, err
	}
	if source != uint32(golibvirt.DomainInterfaceAddressesSrcLease) {
		return nil, lverr(golibvirt.ErrInvalidArg, "fake: the driver reads the lease table")
	}
	if c.f.noDomainLeases {
		return nil, lverr(golibvirt.ErrNoSupport, "this function is not supported by the connection driver")
	}
	if d.state != golibvirt.DomainRunning || d.ip == "" {
		return nil, nil
	}
	return []golibvirt.DomainInterface{{
		Name: "vnet0", Hwaddr: golibvirt.OptString{d.mac},
		Addrs: []golibvirt.DomainIPAddr{
			{Type: int32(golibvirt.IPAddrTypeIpv6), Addr: "fd00::10", Prefix: 64},
			{Type: int32(golibvirt.IPAddrTypeIpv4), Addr: d.ip, Prefix: 24},
		},
	}}, nil
}

func (c *fakeConn) net(name string) (*fakeNet, error) {
	n := c.f.nets[name]
	if n == nil {
		return nil, lverr(golibvirt.ErrNoNetwork, "Network not found: no network with matching name '"+name+"'")
	}
	return n, nil
}

func (c *fakeConn) NetworkLookupByName(name string) (golibvirt.Network, error) {
	unlock, err := c.enter("NetworkLookupByName")
	if err != nil {
		return golibvirt.Network{}, err
	}
	defer unlock()
	if _, err := c.net(name); err != nil {
		return golibvirt.Network{}, err
	}
	return golibvirt.Network{Name: name}, nil
}

func (c *fakeConn) NetworkGetXMLDesc(net golibvirt.Network, _ uint32) (string, error) {
	unlock, err := c.enter("NetworkGetXMLDesc")
	if err != nil {
		return "", err
	}
	defer unlock()
	n, err := c.net(net.Name)
	if err != nil {
		return "", err
	}
	return n.xml, nil
}

func (c *fakeConn) NetworkDefineXML(x string) (golibvirt.Network, error) {
	unlock, err := c.enter("NetworkDefineXML")
	if err != nil {
		return golibvirt.Network{}, err
	}
	defer unlock()
	if err := wellFormed(x); err != nil {
		return golibvirt.Network{}, lverr(golibvirt.ErrXMLError, "XML error: "+err.Error())
	}
	name := submatch(nameRe, x)
	c.f.nets[name] = &fakeNet{xml: x}
	return golibvirt.Network{Name: name}, nil
}

func (c *fakeConn) NetworkCreate(net golibvirt.Network) error {
	unlock, err := c.enter("NetworkCreate")
	if err != nil {
		return err
	}
	defer unlock()
	n, err := c.net(net.Name)
	if err != nil {
		return err
	}
	if n.active {
		return lverr(golibvirt.ErrOperationInvalid, "Requested operation is not valid: network is already active")
	}
	n.active = true
	return nil
}

func (c *fakeConn) NetworkIsActive(net golibvirt.Network) (int32, error) {
	unlock, err := c.enter("NetworkIsActive")
	if err != nil {
		return 0, err
	}
	defer unlock()
	n, err := c.net(net.Name)
	if err != nil {
		return 0, err
	}
	if n.active {
		return 1, nil
	}
	return 0, nil
}

func (c *fakeConn) NetworkSetAutostart(net golibvirt.Network, autostart int32) error {
	unlock, err := c.enter("NetworkSetAutostart")
	if err != nil {
		return err
	}
	defer unlock()
	n, err := c.net(net.Name)
	if err != nil {
		return err
	}
	n.autostart = autostart != 0
	return nil
}

func (c *fakeConn) NetworkGetDhcpLeases(net golibvirt.Network, mac golibvirt.OptString, _ int32, _ uint32) ([]golibvirt.NetworkDhcpLease, uint32, error) {
	unlock, err := c.enter("NetworkGetDhcpLeases")
	if err != nil {
		return nil, 0, err
	}
	defer unlock()
	if _, err := c.net(net.Name); err != nil {
		return nil, 0, err
	}
	var out []golibvirt.NetworkDhcpLease
	for _, d := range c.f.doms {
		if d.state != golibvirt.DomainRunning || d.ip == "" || (len(mac) == 1 && mac[0] != d.mac) {
			continue
		}
		out = append(out, golibvirt.NetworkDhcpLease{Iface: "kdk-br0", Type: int32(golibvirt.IPAddrTypeIpv4), Mac: golibvirt.OptString{d.mac}, Ipaddr: d.ip, Prefix: 24})
	}
	return out, uint32(len(out)), nil
}

func (c *fakeConn) LifecycleEvents(ctx context.Context) (<-chan golibvirt.DomainEventLifecycleMsg, error) {
	unlock, err := c.enter("LifecycleEvents")
	if err != nil {
		return nil, err
	}
	s := &fakeSub{ch: make(chan golibvirt.DomainEventLifecycleMsg, 16)}
	c.f.subs[s] = true
	unlock()
	go func() {
		select {
		case <-ctx.Done():
		case <-c.gone:
		}
		c.f.mu.Lock()
		delete(c.f.subs, s)
		close(s.ch)
		c.f.mu.Unlock()
	}()
	return s.ch, nil
}

func (c *fakeConn) Disconnected() <-chan struct{} { return c.gone }

func (c *fakeConn) Disconnect() error {
	c.close()
	return nil
}
