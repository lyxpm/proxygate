package certificate

import (
	"context"
	"crypto"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"
	"github.com/proxygate/proxygate/internal/apperr"
	"github.com/proxygate/proxygate/internal/certificate/provider"
	"github.com/proxygate/proxygate/internal/secure"
	"github.com/proxygate/proxygate/internal/storage"
)

const (
	letsEncrypt        = "https://acme-v02.api.letsencrypt.org/directory"
	letsEncryptStaging = "https://acme-staging-v02.api.letsencrypt.org/directory"
)

// acmeUser implements registration.User for an account key stored on disk.
type acmeUser struct {
	email string
	key   crypto.PrivateKey
	reg   *registration.Resource
}

func (u *acmeUser) GetEmail() string                        { return u.email }
func (u *acmeUser) GetRegistration() *registration.Resource { return u.reg }
func (u *acmeUser) GetPrivateKey() crypto.PrivateKey        { return u.key }

// ensureAccount creates and persists an ACME account key when needed.
func (m *Manager) ensureAccount(ctx context.Context, email, caURL, _ string) (*storage.ACMEAccount, error) {
	acc, err := m.store.FindACMEAccount(ctx, email, caURL)
	if err != nil {
		return nil, err
	}
	if acc != nil {
		return acc, nil
	}
	key, err := generateAccountKey()
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	token, err := secure.RandomToken(16)
	if err != nil {
		return nil, err
	}
	rel := filepath.ToSlash(filepath.Join("acme", "accounts", token+".pem"))
	abs := m.dataPath(rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return nil, err
	}
	pemData := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := writeFileAtomic(abs, pemData, 0o600); err != nil {
		return nil, err
	}
	acc = &storage.ACMEAccount{Email: email, CAURL: caURL, AccountKeyPath: rel, Status: "pending"}
	if err := m.store.CreateACMEAccount(ctx, acc); err != nil {
		_ = os.Remove(abs)
		return nil, err
	}
	m.log.Info("created ACME account", "email", email, "ca", caURL)
	return acc, nil
}

// obtain runs one ACME order and returns PEM material.
func (m *Manager) obtain(ctx context.Context, cert *storage.Certificate) ([]byte, []byte, error) {
	if cert.ACMEAccountID == nil {
		return nil, nil, errors.New("certificate has no ACME account")
	}
	acc, err := m.store.GetACMEAccount(ctx, *cert.ACMEAccountID)
	if err != nil {
		return nil, nil, fmt.Errorf("load ACME account: %w", err)
	}
	keyPEM, err := os.ReadFile(m.dataPath(acc.AccountKeyPath))
	if err != nil {
		return nil, nil, fmt.Errorf("read account key: %w", err)
	}
	priv, err := certcrypto.ParsePEMPrivateKey(keyPEM)
	if err != nil {
		return nil, nil, fmt.Errorf("parse account key: %w", err)
	}
	user := &acmeUser{email: acc.Email, key: priv}
	if strings.TrimSpace(acc.Registration) != "" {
		var reg registration.Resource
		if json.Unmarshal([]byte(acc.Registration), &reg) == nil {
			user.reg = &reg
		}
	}
	keyType := cert.KeyType
	if keyType == "" {
		keyType = "P256"
	}
	lc := lego.NewConfig(user)
	lc.CADirURL = acc.CAURL
	lc.Certificate.KeyType = certcrypto.KeyType(keyType)
	lc.UserAgent = "ProxyManager/1.0"
	if m.httpClient != nil {
		lc.HTTPClient = m.httpClient
	}
	client, err := lego.NewClient(lc)
	if err != nil {
		return nil, nil, fmt.Errorf("create ACME client: %w", err)
	}
	if user.reg == nil {
		reg, err := client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
		if err != nil {
			return nil, nil, fmt.Errorf("register ACME account: %w", err)
		}
		user.reg = reg
		raw, _ := json.Marshal(reg)
		acc.Registration = string(raw)
		acc.Status = "valid"
		if err := m.store.UpdateACMEAccount(ctx, acc); err != nil {
			return nil, nil, err
		}
	}
	switch cert.ChallengeType {
	case "http01":
		if m.challenges == nil {
			return nil, nil, errors.New("HTTP-01 is unavailable: challenge store is not configured")
		}
		var temp *http.Server
		if !m.challenges.PortListening(80) {
			srv, err := startChallengeServer(m.challenges)
			if err != nil {
				return nil, nil, fmt.Errorf("HTTP-01 requires port 80: %w", err)
			}
			temp = srv
			defer temp.Close()
		}
		if err := client.Challenge.SetHTTP01Provider(&httpChallenge{store: m.challenges}); err != nil {
			return nil, nil, err
		}
	case "dns01":
		if cert.DNSProviderID == nil {
			return nil, nil, errors.New("DNS-01 certificate has no DNS provider")
		}
		creds, err := m.dnsCredentials(ctx, *cert.DNSProviderID)
		if err != nil {
			return nil, nil, err
		}
		p, err := provider.New(m.dnsProviderType(ctx, *cert.DNSProviderID), creds)
		if err != nil {
			return nil, nil, err
		}
		if err := client.Challenge.SetDNS01Provider(p); err != nil {
			return nil, nil, err
		}
	default:
		return nil, nil, fmt.Errorf("unsupported challenge type %q", cert.ChallengeType)
	}
	m.logf(ctx, cert.ID, "info", "向 %s 申请证书：%s", acc.CAURL, strings.Join(cert.Domains, ", "))
	res, err := client.Certificate.Obtain(certificate.ObtainRequest{
		Domains: cert.Domains,
		Bundle:  true,
	})
	if err != nil {
		return nil, nil, err
	}
	if len(res.Certificate) == 0 || len(res.PrivateKey) == 0 {
		return nil, nil, errors.New("ACME server returned an empty certificate")
	}
	return res.Certificate, res.PrivateKey, nil
}

func (m *Manager) dnsProviderType(ctx context.Context, id uint) string {
	p, err := m.store.GetDNSProvider(ctx, id)
	if err != nil {
		return ""
	}
	return p.ProviderType
}

func (m *Manager) dnsCredentials(ctx context.Context, id uint) (provider.Credentials, error) {
	p, err := m.store.GetDNSProvider(ctx, id)
	if err != nil {
		return provider.Credentials{}, err
	}
	raw, err := m.box.Open(p.CredentialsEncrypted, fmt.Sprintf("dns-provider:%d", p.ID))
	if err != nil {
		return provider.Credentials{}, errors.New("cannot decrypt DNS credentials")
	}
	var c provider.Credentials
	if err := json.Unmarshal(raw, &c); err != nil {
		return provider.Credentials{}, err
	}
	return c, nil
}

// httpChallenge serves HTTP-01 tokens through the proxy engine.
type httpChallenge struct {
	store ChallengeSink
}

func (h *httpChallenge) Present(_ string, token, keyAuth string) error {
	h.store.Put(token, keyAuth)
	return nil
}

func (h *httpChallenge) CleanUp(_ string, token, _ string) error {
	h.store.Delete(token)
	return nil
}

func (h *httpChallenge) Timeout() (time.Duration, time.Duration) {
	return time.Minute, 2 * time.Second
}

// startChallengeServer listens on :80 for HTTP-01 when the proxy itself does
// not own port 80 (e.g. a DNS-only deployment that still requests HTTP-01).
func startChallengeServer(store ChallengeSink) (*http.Server, error) {
	ln, err := net.Listen("tcp", ":80")
	if err != nil {
		return nil, err
	}
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !store.Serve(w, r) {
				http.NotFound(w, r)
			}
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() { _ = srv.Serve(ln) }()
	return srv, nil
}

// caDirectory maps user-facing CA names to directory URLs.
func caDirectory(ca string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(ca)) {
	case "", "letsencrypt":
		return letsEncrypt, nil
	case "letsencrypt-staging", "staging":
		return letsEncryptStaging, nil
	default:
		ca = strings.TrimSpace(ca)
		if strings.HasPrefix(ca, "https://") {
			return ca, nil
		}
		return "", apperr.InvalidField("ca", "CA must be letsencrypt, letsencrypt-staging or an https directory URL")
	}
}

func normalizeCertificateDomains(in []string) ([]string, error) {
	seen := make(map[string]bool)
	var out []string
	for _, raw := range in {
		d := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
		if d == "" {
			continue
		}
		if d == "*" || (net.ParseIP(d) == nil && !validCertDomain(d)) {
			return nil, apperr.InvalidField("domains", fmt.Sprintf("invalid domain %q", raw))
		}
		if seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	if len(out) == 0 {
		return nil, apperr.InvalidField("domains", "at least one domain is required")
	}
	if len(out) > 100 {
		return nil, apperr.InvalidField("domains", "too many domains")
	}
	return out, nil
}

func validCertDomain(d string) bool {
	if net.ParseIP(d) != nil {
		return true
	}
	if len(d) > 253 {
		return false
	}
	// Same shape as site validation: labels, optional single wildcard.
	if strings.HasPrefix(d, "*.") {
		d = d[2:]
	}
	if d == "" {
		return false
	}
	for _, label := range strings.Split(d, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

var _ challenge.Provider = (*httpChallenge)(nil)
