package proxy

import (
	"net"
	"testing"
)

func TestBlockedDialIP(t *testing.T) {
	cases := []struct {
		ip      string
		blocked bool
	}{
		{"169.254.169.254", true},
		{"fe80::1", true},
		{"ff02::1", true},
		{"0.0.0.0", true},
		{"10.0.0.5", false},
		{"192.168.1.5", false},
		{"127.0.0.1", false},
		{"8.8.8.8", false},
	}
	for _, tc := range cases {
		if got := blockedDialIP(net.ParseIP(tc.ip)); got != tc.blocked {
			t.Fatalf("blockedDialIP(%s)=%v want %v", tc.ip, got, tc.blocked)
		}
	}
}
