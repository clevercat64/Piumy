package netinfo

import (
	"net"
	"testing"
)

// Documentation-range addresses (RFC 5737 / RFC 3849): never a real machine.
var (
	ownIP   = net.ParseIP("192.0.2.10")
	otherIP = net.ParseIP("198.51.100.7")
)

func isOwnFake(ip net.IP) bool { return ip.Equal(ownIP) || ip.Equal(net.ParseIP("2001:db8::1")) }

func isOwnAll(net.IP) bool { return true }

func TestLocalizeEndpoint(t *testing.T) {
	cases := []struct {
		name, in, want string
		isOwn          func(net.IP) bool
	}{
		{"own LAN IP becomes loopback, same port", "http://192.0.2.10:8787", "http://127.0.0.1:8787", isOwnFake},
		{"scheme and path survive", "https://192.0.2.10:8789/api", "https://127.0.0.1:8789/api", isOwnFake},
		{"own IP without a port", "http://192.0.2.10", "http://127.0.0.1", isOwnFake},
		{"own IPv6 also goes to IPv4 loopback", "http://[2001:db8::1]:8787", "http://127.0.0.1:8787", isOwnFake},
		{"another machine's IP stays (S6, gateway on a Pi)", "http://198.51.100.7:8787", "http://198.51.100.7:8787", isOwnFake},
		{"loopback stays", "http://127.0.0.1:8787", "http://127.0.0.1:8787", isOwnFake},
		{"another loopback address stays even if the lookup claims it", "http://127.0.0.2:8787", "http://127.0.0.2:8787", isOwnAll},
		{"a name stays", "http://localhost:8787", "http://localhost:8787", isOwnFake},
		{"an mDNS name stays", "http://mypi.local:8787", "http://mypi.local:8787", isOwnFake},
		{"no scheme doesn't parse: untouched", "192.0.2.10:8787", "192.0.2.10:8787", isOwnFake},
		{"empty stays empty", "", "", isOwnFake},
		{"nil isOwn never rewrites", "http://192.0.2.10:8787", "http://192.0.2.10:8787", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := LocalizeEndpoint(c.in, c.isOwn); got != c.want {
				t.Errorf("LocalizeEndpoint(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

type otherAddr struct{}

func (otherAddr) Network() string { return "x" }
func (otherAddr) String() string  { return "x" }

func TestHasIP(t *testing.T) {
	addrs := []net.Addr{
		otherAddr{},
		&net.IPNet{IP: net.ParseIP("203.0.113.1"), Mask: net.CIDRMask(24, 32)},
		&net.IPAddr{IP: ownIP},
	}
	if !hasIP(addrs, ownIP) {
		t.Error("hasIP = false for an IP present as *net.IPAddr, want true")
	}
	if !hasIP(addrs, net.ParseIP("203.0.113.1")) {
		t.Error("hasIP = false for an IP present as *net.IPNet, want true")
	}
	if hasIP(addrs, otherIP) {
		t.Error("hasIP = true for an IP no interface has, want false")
	}
	if hasIP(nil, ownIP) {
		t.Error("hasIP = true on an empty interface list, want false")
	}
}

// TestIsOwnIPKnownCase is the instrument check for the real interface lookup:
// every machine has 127.0.0.1 on its loopback interface.
func TestIsOwnIPKnownCase(t *testing.T) {
	if !IsOwnIP(net.ParseIP("127.0.0.1")) {
		t.Error("IsOwnIP(127.0.0.1) = false, want true — the loopback interface is always there")
	}
}
