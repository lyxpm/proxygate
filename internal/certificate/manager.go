// Package certificate owns ACME issuance, uploaded certificates, renewal and
// TLS material loading. Private keys are written to 0600 files and are never
// returned by the API.
package certificate

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/proxygate/proxygate/internal/apperr"
	"github.com/proxygate/proxygate/internal/secure"
	"github.com/proxygate/proxygate/internal/storage"
)

// ChallengeSink receives HTTP-01 tokens; the proxy engine implements it.
type ChallengeSink interface {
	Put(token, keyAuth string)
	Delete(token string)
	PortListening(port int) bool
	Serve(w http.ResponseWriter, r *http.Request) bool
}

// Manager implements certificate business logic.
type Manager struct {
	store      *storage.Store
	box        *secure.Box
	dataDir    string
	reload     func(context.Context) error
	challenges ChallengeSink
	log        *slog.Logger
	httpClient *http.Client

	jobMu sync.Mutex
	jobs  map[uint]bool

	warnMu sync.Mutex
	warned map[uint]time.Time

	renewMu   sync.RWMutex
	renewDays int

	adminReload func(context.Context, uint)
}

// NewManager creates a certificate manager.
func NewManager(store *storage.Store, box *secure.Box, dataDir string, challenges ChallengeSink, reload func(context.Context) error, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{store: store, box: box, dataDir: dataDir, challenges: challenges, reload: reload, log: log, jobs: make(map[uint]bool), warned: make(map[uint]time.Time), renewDays: 30}
}

// ReloadRenewalSettings reads the configurable renewal window (days).
func (m *Manager) ReloadRenewalSettings(ctx context.Context) {
	days := 30
	if raw, ok, err := m.store.GetSetting(ctx, "certificate_renew_before_days"); err == nil && ok {
		if n, err := strconv.Atoi(raw); err == nil && n >= 7 && n <= 60 {
			days = n
		}
	}
	m.renewMu.Lock()
	m.renewDays = days
	m.renewMu.Unlock()
}

func (m *Manager) renewalWindow() time.Duration {
	m.renewMu.RLock()
	defer m.renewMu.RUnlock()
	return time.Duration(m.renewDays) * 24 * time.Hour
}

// SetAdminHTTPSReloader registers a callback for the certificate used by the
// management HTTPS listener.
func (m *Manager) SetAdminHTTPSReloader(fn func(context.Context, uint)) {
	m.adminReload = fn
}

// SetHTTPClient overrides the ACME HTTP client (private CA trust, proxies).
func (m *Manager) SetHTTPClient(client *http.Client) {
	if client != nil {
		m.httpClient = client
	}
}

// IssueRequest describes an ACME order.
type IssueRequest struct {
	Name          string   `json:"name"`
	Domains       []string `json:"domains"`
	Email         string   `json:"email"`
	CA            string   `json:"ca"`
	Challenge     string   `json:"challenge"`
	DNSProviderID *uint    `json:"dns_provider_id"`
	KeyType       string   `json:"key_type"`
	AutoRenew     *bool    `json:"auto_renew"`
}

// UploadRequest is a user-provided certificate pair.
type UploadRequest struct {
	Name           string `json:"name"`
	CertificatePEM string `json:"certificate_pem"`
	PrivateKeyPEM  string `json:"private_key_pem"`
}

// List returns all certificates.
func (m *Manager) List(ctx context.Context) ([]storage.Certificate, error) {
	return m.store.ListCertificates(ctx)
}

// Get returns one certificate.
func (m *Manager) Get(ctx context.Context, id uint) (*storage.Certificate, error) {
	c, err := m.store.GetCertificate(ctx, id)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, apperr.NotFoundf("certificate %d not found", id)
	}
	return c, err
}

// CreateIssue validates the order and creates a pending certificate record.
// The actual ACME exchange is performed by RunIssue.
func (m *Manager) CreateIssue(ctx context.Context, in IssueRequest) (*storage.Certificate, error) {
	domains, err := normalizeCertificateDomains(in.Domains)
	if err != nil {
		return nil, err
	}
	email := strings.TrimSpace(in.Email)
	if email == "" || !strings.Contains(email, "@") {
		return nil, apperr.InvalidField("email", "a valid email is required for ACME registration")
	}
	caURL, err := caDirectory(in.CA)
	if err != nil {
		return nil, err
	}
	challenge := strings.ToLower(strings.TrimSpace(in.Challenge))
	if challenge == "" {
		if in.DNSProviderID != nil && *in.DNSProviderID != 0 {
			challenge = "dns01"
		} else {
			challenge = "http01"
		}
	}
	if challenge != "http01" && challenge != "dns01" {
		return nil, apperr.InvalidField("challenge", "challenge must be http01 or dns01")
	}
	keyType := strings.TrimSpace(in.KeyType)
	if keyType == "" {
		keyType = "P256"
	}
	switch keyType {
	case "P256", "P384", "2048", "4096":
	default:
		return nil, apperr.InvalidField("key_type", "key type must be P256, P384, 2048 or 4096")
	}
	var providerID *uint
	if challenge == "dns01" {
		if in.DNSProviderID == nil || *in.DNSProviderID == 0 {
			return nil, apperr.InvalidField("dns_provider_id", "DNS-01 requires a DNS provider")
		}
		p, err := m.store.GetDNSProvider(ctx, *in.DNSProviderID)
		if errors.Is(err, storage.ErrNotFound) {
			return nil, apperr.InvalidField("dns_provider_id", "DNS provider not found")
		}
		if err != nil {
			return nil, err
		}
		if !p.Enabled {
			return nil, apperr.InvalidField("dns_provider_id", "DNS provider is disabled")
		}
		providerID = in.DNSProviderID
	}
	account, err := m.ensureAccount(ctx, email, caURL, keyType)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = domains[0]
	}
	cert := &storage.Certificate{
		Name: name, Domains: domains, CertificateType: "acme", ChallengeType: challenge,
		Status: "pending", AutoRenew: boolOr(in.AutoRenew, true),
		ACMEAccountID: &account.ID, DNSProviderID: providerID, KeyType: keyType,
	}
	if err := m.store.CreateCertificate(ctx, cert); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, apperr.Conflictf("a certificate named %q already exists", name)
		}
		return nil, err
	}
	dir := fmt.Sprintf("certificates/%d", cert.ID)
	cert.CertificatePath = filepath.ToSlash(filepath.Join(dir, "cert.pem"))
	cert.PrivateKeyPath = filepath.ToSlash(filepath.Join(dir, "private.key"))
	if err := m.store.UpdateCertificate(ctx, cert); err != nil {
		return nil, err
	}
	m.logf(ctx, cert.ID, "info", "已创建证书申请：%s（%s）", strings.Join(domains, ", "), challenge)
	return cert, nil
}

// RunIssue performs the ACME order for a pending certificate.
func (m *Manager) RunIssue(ctx context.Context, id uint) error {
	if !m.beginJob(id) {
		return apperr.Conflictf("this certificate is already being issued")
	}
	defer m.endJob(id)
	cert, err := m.Get(ctx, id)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	certPEM, keyPEM, err := m.obtain(ctx, cert)
	if err != nil {
		m.fail(ctx, cert, err)
		return err
	}
	if err := m.saveIssued(ctx, cert, certPEM, keyPEM); err != nil {
		m.fail(ctx, cert, err)
		return err
	}
	return nil
}

// RenewNow forces a renewal, used by the API and the scheduler.
func (m *Manager) RenewNow(ctx context.Context, id uint) error {
	if !m.beginJob(id) {
		return apperr.Conflictf("this certificate is already being issued")
	}
	defer m.endJob(id)
	cert, err := m.Get(ctx, id)
	if err != nil {
		return err
	}
	if cert.CertificateType != "acme" {
		return apperr.InvalidField("certificate", "only ACME certificates can be renewed automatically")
	}
	if cert.ACMEAccountID == nil {
		return apperr.InvalidField("certificate", "certificate has no ACME account")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	m.logf(ctx, cert.ID, "info", "开始续期：%s", strings.Join(cert.Domains, ", "))
	certPEM, keyPEM, err := m.obtain(ctx, cert)
	if err != nil {
		m.fail(ctx, cert, err)
		return err
	}
	return m.saveIssued(ctx, cert, certPEM, keyPEM)
}

// Upload validates and stores a PEM pair.
func (m *Manager) Upload(ctx context.Context, in UploadRequest) (*storage.Certificate, error) {
	certPEM, keyPEM := []byte(in.CertificatePEM), []byte(in.PrivateKeyPEM)
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, apperr.InvalidField("certificate_pem", "certificate and private key do not match: "+err.Error())
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, apperr.InvalidField("certificate_pem", "cannot parse certificate")
	}
	if time.Now().After(leaf.NotAfter) {
		return nil, apperr.InvalidField("certificate_pem", "certificate has already expired")
	}
	domains := leafDomains(leaf)
	if len(domains) == 0 {
		return nil, apperr.InvalidField("certificate_pem", "certificate contains no DNS names")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = domains[0]
	}
	cert := &storage.Certificate{
		Name: name, Domains: domains, CertificateType: "upload",
		Status: "valid", Issuer: leaf.Issuer.String(),
		ExpiresAt: &leaf.NotAfter, LastRenewAt: ptrTime(time.Now()),
	}
	if err := m.store.CreateCertificate(ctx, cert); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, apperr.Conflictf("a certificate named %q already exists", name)
		}
		return nil, err
	}
	dir := fmt.Sprintf("certificates/%d", cert.ID)
	cert.CertificatePath = filepath.ToSlash(filepath.Join(dir, "cert.pem"))
	cert.PrivateKeyPath = filepath.ToSlash(filepath.Join(dir, "private.key"))
	if err := m.writePair(cert.ID, certPEM, keyPEM); err != nil {
		_ = m.store.DeleteCertificate(ctx, cert.ID)
		return nil, apperr.Internalf(err, "cannot store certificate files")
	}
	if err := m.store.UpdateCertificate(ctx, cert); err != nil {
		return nil, err
	}
	m.logf(ctx, cert.ID, "info", "已上传证书：%s", strings.Join(domains, ", "))
	if m.adminReload != nil {
		m.adminReload(ctx, cert.ID)
	}
	return cert, nil
}

// Delete removes an unused certificate and its key material.
func (m *Manager) Delete(ctx context.Context, id uint) error {
	cert, err := m.Get(ctx, id)
	if err != nil {
		return err
	}
	used, err := m.store.CountSitesByCertificate(ctx, id)
	if err != nil {
		return err
	}
	if used > 0 {
		return apperr.Conflictf("certificate is associated with %d site(s); unlink it first", used)
	}
	if err := m.store.DeleteCertificate(ctx, id); err != nil {
		return err
	}
	dir := m.dataPath(fmt.Sprintf("certificates/%d", cert.ID))
	if err := os.RemoveAll(dir); err != nil {
		m.log.Warn("remove certificate directory", "dir", dir, "error", err)
	}
	if err := m.reload(ctx); err != nil {
		return apperr.Internalf(err, "certificate deleted but runtime update failed")
	}
	if m.adminReload != nil {
		m.adminReload(ctx, id)
	}
	return nil
}

// LoadPair reads the certificate and key for the proxy TLS index.
func (m *Manager) LoadPair(cert *storage.Certificate) (*tls.Certificate, error) {
	if cert == nil {
		return nil, errors.New("nil certificate")
	}
	certPEM, err := os.ReadFile(m.dataPath(cert.CertificatePath))
	if err != nil {
		return nil, fmt.Errorf("read certificate: %w", err)
	}
	keyPEM, err := os.ReadFile(m.dataPath(cert.PrivateKeyPath))
	if err != nil {
		return nil, fmt.Errorf("read private key: %w", err)
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	if len(pair.Certificate) > 0 {
		leaf, err := x509.ParseCertificate(pair.Certificate[0])
		if err == nil {
			pair.Leaf = leaf
		}
	}
	return &pair, nil
}

// Logs returns renewal logs for a certificate.
func (m *Manager) Logs(ctx context.Context, id uint, limit int) ([]storage.CertificateLog, error) {
	if _, err := m.Get(ctx, id); err != nil {
		return nil, err
	}
	return m.store.ListCertificateLogs(ctx, id, limit)
}

func (m *Manager) saveIssued(ctx context.Context, cert *storage.Certificate, certPEM, keyPEM []byte) error {
	if err := m.writePair(cert.ID, certPEM, keyPEM); err != nil {
		return err
	}
	leaf, err := parseCertificate(certPEM)
	if err != nil {
		return err
	}
	now := time.Now()
	cert.Status = "valid"
	cert.Issuer = leaf.Issuer.String()
	cert.ExpiresAt = &leaf.NotAfter
	cert.LastRenewAt = &now
	cert.RenewAttempts = 0
	cert.LastError = ""
	if domains := leafDomains(leaf); len(domains) > 0 {
		cert.Domains = domains
	}
	next := nextRenewal(leaf.NotAfter, now, m.renewalWindow())
	cert.NextRenewAt = &next
	if err := m.store.UpdateCertificate(ctx, cert); err != nil {
		return err
	}
	m.logf(ctx, cert.ID, "info", "证书已签发：%s，有效期至 %s", strings.Join(cert.Domains, ", "), leaf.NotAfter.Format(time.RFC3339))
	if err := m.reload(ctx); err != nil {
		m.log.Error("reload after certificate issuance", "certificate", cert.ID, "error", err)
		return err
	}
	if m.adminReload != nil {
		m.adminReload(ctx, cert.ID)
	}
	return nil
}

func (m *Manager) fail(ctx context.Context, cert *storage.Certificate, cause error) {
	cert.RenewAttempts++
	cert.LastError = cause.Error()
	// Keep serving the previous certificate when one exists.
	if _, err := os.Stat(m.dataPath(cert.CertificatePath)); err != nil {
		cert.Status = "error"
	}
	delay := time.Hour
	for i := 1; i < cert.RenewAttempts && i < 5; i++ {
		delay *= 2
	}
	if delay > 24*time.Hour {
		delay = 24 * time.Hour
	}
	next := time.Now().Add(delay)
	cert.NextRenewAt = &next
	if err := m.store.UpdateCertificate(ctx, cert); err != nil {
		m.log.Error("persist certificate failure", "certificate", cert.ID, "error", err)
	}
	m.logf(ctx, cert.ID, "error", "证书申请/续期失败：%s（%s 后重试）", cause.Error(), delay)
}

func (m *Manager) writePair(id uint, certPEM, keyPEM []byte) error {
	dir := m.dataPath(fmt.Sprintf("certificates/%d", id))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(dir, "cert.pem"), certPEM, 0o644); err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(dir, "private.key"), keyPEM, 0o600); err != nil {
		return err
	}
	return nil
}

func (m *Manager) dataPath(rel string) string {
	return filepath.Join(m.dataDir, filepath.FromSlash(rel))
}

func (m *Manager) logf(ctx context.Context, certID uint, level, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if err := m.store.InsertCertificateLog(ctx, &storage.CertificateLog{
		CertificateID: certID, Level: level, Message: msg,
	}); err != nil {
		m.log.Warn("write certificate log", "error", err)
	}
	if level == "error" {
		m.log.Error(msg, "certificate", certID)
	} else {
		m.log.Info(msg, "certificate", certID)
	}
}

func (m *Manager) beginJob(id uint) bool {
	m.jobMu.Lock()
	defer m.jobMu.Unlock()
	if m.jobs[id] {
		return false
	}
	m.jobs[id] = true
	return true
}

func (m *Manager) endJob(id uint) {
	m.jobMu.Lock()
	delete(m.jobs, id)
	m.jobMu.Unlock()
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func parseCertificate(certPEM []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, errors.New("certificate PEM is empty")
	}
	return x509.ParseCertificate(block.Bytes)
}

func leafDomains(leaf *x509.Certificate) []string {
	seen := make(map[string]bool)
	var out []string
	for _, d := range append(append([]string{}, leaf.DNSNames...), leaf.Subject.CommonName) {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	for _, ip := range leaf.IPAddresses {
		d := ip.String()
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
}

func nextRenewal(expires, now time.Time, window time.Duration) time.Time {
	remaining := expires.Sub(now)
	var at time.Time
	if remaining < 10*24*time.Hour {
		at = now.Add(remaining / 2)
	} else {
		at = expires.Add(-window)
		if at.Before(now) {
			at = now.Add(time.Hour)
		}
	}
	// Spread renewals to avoid a thundering herd at the CA.
	jitter := time.Duration(randomInt(12*3600)) * time.Second
	return at.Add(jitter)
}

func randomInt(max int) int {
	if max <= 0 {
		return 0
	}
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0
	}
	return int(uint32(b[0])<<24|uint32(b[1])<<16|uint32(b[2])<<8|uint32(b[3])) % max
}

func ptrTime(t time.Time) *time.Time { return &t }

func boolOr(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

// generateAccountKey creates an ECDSA P-256 account key.
func generateAccountKey() (*ecdsa.PrivateKey, error) {
	return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}
