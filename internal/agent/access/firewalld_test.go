package access

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// fakeFirewalld is a scripted firewall-cmd that keeps the state the real
// one would.
type fakeFirewalld struct {
	running  bool
	missing  bool
	ifaces   []string
	policies map[string]*fakePolicy
	calls    []string
}

type fakePolicy struct{ target, ingress, egress string }

func (f *fakeFirewalld) run(_ context.Context, name string, args ...string) ([]byte, error) {
	if f.missing {
		return nil, &exec.Error{Name: name, Err: exec.ErrNotFound}
	}
	if !f.running {
		return []byte("not running"), errors.New("exit status 252")
	}
	a := strings.Join(args, " ")
	f.calls = append(f.calls, a)
	switch {
	case a == "--state":
		return []byte("running"), nil
	case a == "--permanent --zone=trusted --list-interfaces":
		return []byte(strings.Join(f.ifaces, " ")), nil
	case strings.HasPrefix(a, "--permanent --zone=trusted --add-interface="):
		f.ifaces = append(f.ifaces, strings.TrimPrefix(a, "--permanent --zone=trusted --add-interface="))
	case a == "--permanent --get-policies":
		var n []string
		for p := range f.policies {
			n = append(n, p)
		}
		return []byte(strings.Join(n, " ")), nil
	case strings.HasPrefix(a, "--permanent --new-policy="):
		f.policies[strings.TrimPrefix(a, "--permanent --new-policy=")] = &fakePolicy{target: "CONTINUE"}
	case strings.HasPrefix(a, "--permanent --info-policy="):
		p := f.policies[strings.TrimPrefix(a, "--permanent --info-policy=")]
		return []byte("kdk-egress (active)\n  target: " + p.target + "\n  ingress-zones: " + p.ingress + "\n  egress-zones: " + p.egress), nil
	case strings.HasPrefix(a, "--permanent --policy="+EgressPolicy+" "):
		p := f.policies[EgressPolicy]
		switch strings.TrimPrefix(a, "--permanent --policy="+EgressPolicy+" ") {
		case "--set-target=ACCEPT":
			p.target = "ACCEPT"
		case "--add-ingress-zone=trusted":
			p.ingress = "trusted"
		case "--add-egress-zone=ANY":
			p.egress = "ANY"
		}
	case a == "--reload":
	default:
		return nil, errors.New("unexpected: " + a)
	}
	return nil, nil
}

func (f *fakeFirewalld) mutations() int {
	n := 0
	for _, c := range f.calls {
		if strings.Contains(c, "--add-") || strings.Contains(c, "--new-") || strings.Contains(c, "--set-") || c == "--reload" {
			n++
		}
	}
	return n
}

func TestEnsureFirewalldSetsUpOnce(t *testing.T) {
	f := &fakeFirewalld{running: true, ifaces: []string{"tailscale0"}, policies: map[string]*fakePolicy{"allow-host-ipv6": {}}}
	changed, err := EnsureFirewalld(context.Background(), f.run, "kdk-br0", nil)
	if err != nil || !changed {
		t.Fatalf("first run: %v, changed %v", err, changed)
	}
	if got := strings.Join(f.ifaces, " "); got != "tailscale0 kdk-br0" {
		t.Errorf("trusted zone interfaces = %q: the existing ones must stay", got)
	}
	if p := f.policies[EgressPolicy]; p == nil || p.target != "ACCEPT" || p.ingress != "trusted" || p.egress != "ANY" {
		t.Errorf("policy = %+v", p)
	}
	if f.calls[len(f.calls)-1] != "--reload" {
		t.Errorf("a change is activated by a reload: %v", f.calls)
	}

	before := f.mutations()
	changed, err = EnsureFirewalld(context.Background(), f.run, "kdk-br0", nil)
	if err != nil || changed || f.mutations() != before {
		t.Fatalf("second run: %v, changed %v, %d new mutations", err, changed, f.mutations()-before)
	}
}

func TestEnsureFirewalldRepairsAPartialPolicy(t *testing.T) {
	// A crash between creating the policy and filling it in.
	f := &fakeFirewalld{running: true, ifaces: []string{"kdk-br0"}, policies: map[string]*fakePolicy{EgressPolicy: {target: "CONTINUE"}}}
	changed, err := EnsureFirewalld(context.Background(), f.run, "kdk-br0", nil)
	if err != nil || !changed {
		t.Fatalf("%v, changed %v", err, changed)
	}
	if p := f.policies[EgressPolicy]; p.target != "ACCEPT" || p.ingress != "trusted" || p.egress != "ANY" {
		t.Errorf("policy = %+v", p)
	}
}

func TestEnsureFirewalldWithoutFirewalld(t *testing.T) {
	for name, f := range map[string]*fakeFirewalld{"stopped": {}, "not installed": {missing: true}} {
		changed, err := EnsureFirewalld(context.Background(), f.run, "kdk-br0", nil)
		if err != nil || changed {
			t.Errorf("%s: %v, changed %v", name, err, changed)
		}
		if len(f.calls) != 0 {
			t.Errorf("%s: touched firewalld: %v", name, f.calls)
		}
	}
}

func TestEnsureFirewalldReportsFailures(t *testing.T) {
	fail := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "--state" {
			return nil, nil
		}
		return []byte("boom"), errors.New("exit status 1")
	}
	if _, err := EnsureFirewalld(context.Background(), fail, "kdk-br0", nil); err == nil {
		t.Fatal("a failing firewall-cmd was ignored")
	}
}
