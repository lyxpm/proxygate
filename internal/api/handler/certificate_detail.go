package handler

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/proxygate/proxygate/internal/api/response"
	"github.com/proxygate/proxygate/internal/apperr"
	"github.com/proxygate/proxygate/internal/storage"
)

type certDetail struct {
	certView
	CertificatePEM string   `json:"certificate_pem"`
	Subject        string   `json:"subject"`
	SerialNumber   string   `json:"serial_number"`
	DNSNames       []string `json:"dns_names"`
	NotBefore      string   `json:"not_before"`
	NotAfter       string   `json:"not_after"`
	Fingerprint    string   `json:"fingerprint_sha256"`
}

// GetCertificateDetail returns metadata plus the public certificate PEM. The
// private key is never returned by any API.
func (s *Server) GetCertificateDetail(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	cert, err := s.Certs.Get(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	raw, err := s.readCertificatePEM(cert)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	detail := certDetail{certView: toCertView(*cert), CertificatePEM: string(raw)}
	if block, _ := pem.Decode(raw); block != nil {
		if parsed, err := x509.ParseCertificate(block.Bytes); err == nil {
			detail.Subject = parsed.Subject.String()
			detail.SerialNumber = parsed.SerialNumber.Text(16)
			detail.DNSNames = parsed.DNSNames
			detail.NotBefore = parsed.NotBefore.UTC().Format(time.RFC3339)
			detail.NotAfter = parsed.NotAfter.UTC().Format(time.RFC3339)
			sum := sha256.Sum256(parsed.Raw)
			detail.Fingerprint = strings.ToUpper(hex.EncodeToString(sum[:]))
		}
	}
	response.OK(w, detail)
}

// DownloadCertificate returns the certificate chain as a PEM attachment.
func (s *Server) DownloadCertificate(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	cert, err := s.Certs.Get(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	raw, err := s.readCertificatePEM(cert)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	name := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, cert.Name)
	if name == "" {
		name = fmt.Sprintf("certificate-%d", cert.ID)
	}
	s.audit(r, "download_certificate", "certificate", cert.ID, "success", nil)
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.pem"`, name))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(raw)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func (s *Server) readCertificatePEM(cert *storage.Certificate) ([]byte, error) {
	base, err := filepath.EvalSymlinks(filepath.Clean(s.Config.DataDir))
	if err != nil {
		return nil, apperr.New(apperr.Internal, "data directory is unavailable")
	}
	path := filepath.Clean(cert.CertificatePath)
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return nil, apperr.NotFoundf("certificate file not found")
	}
	if base == "" || (path != base && !strings.HasPrefix(path, base+string(os.PathSeparator))) {
		return nil, apperr.New(apperr.Internal, "certificate path is outside the data directory")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, apperr.NotFoundf("certificate file not found")
	}
	return raw, nil
}
