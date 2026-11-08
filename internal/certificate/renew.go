package certificate

import (
	"context"
	"time"

	"github.com/proxygate/proxygate/internal/storage"
)

// Start runs the renewal scheduler until ctx is cancelled.
func (m *Manager) Start(ctx context.Context) {
	m.resumePending(ctx)
	go func() {
		timer := time.NewTimer(0)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			m.checkRenewals(ctx)
			timer.Reset(30 * time.Minute)
		}
	}()
}

// resumePending replays ACME orders that were interrupted by a process
// restart. Pending certificates are idempotent because beginJob deduplicates.
func (m *Manager) resumePending(ctx context.Context) {
	certs, err := m.store.ListPendingCertificates(ctx)
	if err != nil {
		m.log.Warn("list pending certificate orders", "error", err)
		return
	}
	for _, cert := range certs {
		if cert.ACMEAccountID == nil {
			continue
		}
		m.logf(ctx, cert.ID, "info", "恢复未完成的证书申请")
		go func(id uint) {
			rctx, cancel := context.WithTimeout(ctx, 6*time.Minute)
			defer cancel()
			if err := m.RunIssue(rctx, id); err != nil {
				m.log.Warn("resume pending certificate failed", "certificate", id, "error", err)
			}
		}(cert.ID)
	}
}

// checkRenewals starts a renewal for every due certificate. RenewNow enforces
// one job per certificate, so overlapping ticks are harmless.
func (m *Manager) checkRenewals(ctx context.Context) {
	now := time.Now()
	m.warnExpiring(ctx, now)
	certs, err := m.store.ListCertificatesDue(ctx)
	if err != nil {
		m.log.Error("list certificates for renewal", "error", err)
		return
	}
	for _, c := range certs {
		if !renewalDue(c, now, m.renewalWindow()) {
			continue
		}
		go func(id uint) {
			rctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
			defer cancel()
			if err := m.RenewNow(rctx, id); err != nil {
				m.log.Warn("certificate renewal failed", "certificate", id, "error", err)
			}
		}(c.ID)
	}
}

// renewalDue reports whether an ACME certificate is inside its renewal window.
func renewalDue(c storage.Certificate, now time.Time, window time.Duration) bool {
	if !c.AutoRenew || c.ExpiresAt == nil {
		return false
	}
	if c.NextRenewAt != nil {
		return !now.Before(*c.NextRenewAt)
	}
	return c.ExpiresAt.Sub(now) <= window
}

// warnExpiring records one certificate log per 24h for certificates close to
// expiry, so operators can see renewal problems in the UI.
func (m *Manager) warnExpiring(ctx context.Context, now time.Time) {
	certs, err := m.store.ListCertificates(ctx)
	if err != nil {
		return
	}
	for _, c := range certs {
		if c.ExpiresAt == nil || c.CertificateType != "acme" || c.Status != "valid" {
			continue
		}
		left := c.ExpiresAt.Sub(now)
		if left <= 0 || left > 7*24*time.Hour {
			continue
		}
		m.warnMu.Lock()
		last := m.warned[c.ID]
		if !last.IsZero() && now.Sub(last) < 24*time.Hour {
			m.warnMu.Unlock()
			continue
		}
		m.warned[c.ID] = now
		m.warnMu.Unlock()
		days := int(left.Hours() / 24)
		m.logf(ctx, c.ID, "warn", "证书将在 %d 天后到期（%s），请检查自动续期状态", days, c.ExpiresAt.Format(time.RFC3339))
	}
}
