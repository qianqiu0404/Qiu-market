package netutil

import "net"

// IsIPLoopbackAddress deliberately rejects hostnames such as localhost. The
// trading process and local-auth mode must bind to an explicit loopback IP so
// DNS or hosts-file changes cannot widen the trust boundary.
func IsIPLoopbackAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
