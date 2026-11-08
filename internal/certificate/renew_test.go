package certificate

import (
	"testing"
	"time"

	"github.com/proxygate/proxygate/internal/storage"
)

func TestNextRenewalWindow(t *testing.T) {
	now := time.Now()
	expires := now.Add(60 * 24 * time.Hour)
	at := nextRenewal(expires, now, 7*24*time.Hour)
	base := expires.Add(-7 * 24 * time.Hour)
	if at.Before(base) || at.After(base.Add(12*time.Hour)) {
		t.Fatalf("next renewal = %v, want within [%v, %v]", at, base, base.Add(12*time.Hour))
	}
}

func TestRenewalDue(t *testing.T) {
	now := time.Now()
	exp := now.Add(60 * 24 * time.Hour)
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)
	soon := now.Add(10 * 24 * time.Hour)
	cases := []struct {
		name string
		cert storage.Certificate
		want bool
	}{
		{"not-auto", storage.Certificate{ExpiresAt: &soon}, false},
		{"no-expiry", storage.Certificate{AutoRenew: true}, false},
		{"next-past", storage.Certificate{AutoRenew: true, ExpiresAt: &exp, NextRenewAt: &past}, true},
		{"next-future", storage.Certificate{AutoRenew: true, ExpiresAt: &exp, NextRenewAt: &future}, false},
		{"fallback-soon", storage.Certificate{AutoRenew: true, ExpiresAt: &soon}, true},
		{"fallback-later", storage.Certificate{AutoRenew: true, ExpiresAt: &exp}, false},
	}
	for _, tc := range cases {
		if got := renewalDue(tc.cert, now, 30*24*time.Hour); got != tc.want {
			t.Fatalf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}
