package proxy

import (
	"context"
	"fmt"
	"net"
	"syscall"
	"time"
)

// safeDialContext blocks link-local/metadata targets after DNS resolution.
// Private RFC1918/ULA ranges remain allowed by design.
func safeDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return safeDialer(10*time.Second).DialContext(ctx, network, address)
}

func safeDialer(timeout time.Duration) *net.Dialer {
	return &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil || blockedDialIP(ip) {
				return fmt.Errorf("address %s is not allowed", address)
			}
			return nil
		},
	}
}

func blockedDialIP(ip net.IP) bool {
	return ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast()
}
