package netinfo

import (
	"net"
	"net/url"
)

// IsOwnIP reports whether ip is assigned to one of this machine's network
// interfaces. Best effort like the rest of the package: when the interfaces
// can't be listed it answers false, and the caller leaves the address alone.
func IsOwnIP(ip net.IP) bool {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	return hasIP(addrs, ip)
}

func hasIP(addrs []net.Addr, ip net.IP) bool {
	for _, addr := range addrs {
		if own := addrIP(addr); own != nil && own.Equal(ip) {
			return true
		}
	}
	return false
}

// addrIP extracts the IP from the two address types net.Interface.Addrs and
// net.InterfaceAddrs hand back; nil for anything else.
func addrIP(addr net.Addr) net.IP {
	switch v := addr.(type) {
	case *net.IPNet:
		return v.IP
	case *net.IPAddr:
		return v.IP
	}
	return nil
}

// LocalizeEndpoint rewrites an endpoint whose host is one of this machine's
// own non-loopback IPs to 127.0.0.1, same scheme, port and path (T177): an
// antenna that lives on this PC is reachable at loopback whatever network the
// PC is on, while its LAN address goes stale the day the router hands out a
// new one — and the handshake posts the pinpass in clear to whoever holds it.
//
// Everything else comes back untouched: another machine's IP (a gateway on a
// Raspberry Pi with the agent elsewhere on the LAN, S6), a name, loopback
// itself, a value that doesn't parse, and isOwn == nil. Whether the endpoint
// is ALLOWED stays store.IsAllowedPrincipalEndpoint's call, not this one's.
func LocalizeEndpoint(endpoint string, isOwn func(net.IP) bool) string {
	if isOwn == nil {
		return endpoint
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return endpoint
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || ip.IsLoopback() || !isOwn(ip) {
		return endpoint
	}
	port := u.Port() // read before u.Host is overwritten: Port() parses it from Host
	u.Host = "127.0.0.1"
	if port != "" {
		u.Host = net.JoinHostPort(u.Host, port)
	}
	return u.String()
}
