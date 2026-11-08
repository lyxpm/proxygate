package handler

import (
	"archive/zip"
	"bytes"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/proxygate/proxygate/internal/api/response"
	"github.com/proxygate/proxygate/internal/apperr"
	"github.com/proxygate/proxygate/internal/auth"
	"github.com/proxygate/proxygate/internal/storage"
)

// ExportCertificateKey exports a ZIP containing the certificate chain and
// private key. It requires an administrator session plus password re-auth.
func (s *Server) ExportCertificateKey(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if u == nil || auth.RoleOf(u) != "admin" {
		s.fail(w, r, apperr.New(apperr.Forbidden, "需要管理员权限"))
		return
	}
	id, err := parseID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if err := response.Decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if !s.verifyReauth(w, r, in.Password) {
		s.audit(r, "export_certificate_key", "certificate", id, "failure", nil)
		return
	}
	cert, err := s.Certs.Get(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	certPEM, err := s.readCertificatePEM(cert)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	keyPEM, err := s.readPrivateKeyPEM(cert)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if err := addZipFile(zw, "cert.pem", certPEM); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := addZipFile(zw, "private.key", keyPEM); err != nil {
		s.fail(w, r, err)
		return
	}
	readme := []byte("Proxy Manager certificate export\n\ncert.pem: certificate chain\nprivate.key: private key, keep secret\n")
	if err := addZipFile(zw, "README.txt", readme); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := zw.Close(); err != nil {
		s.fail(w, r, err)
		return
	}
	name := safeFileName(cert.Name)
	if name == "" {
		name = fmt.Sprintf("certificate-%d", cert.ID)
	}
	s.audit(r, "export_certificate_key", "certificate", cert.ID, "success", nil)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-key.zip"`, name))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", buf.Len()))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

func addZipFile(zw *zip.Writer, name string, data []byte) error {
	h := &zip.FileHeader{Name: name, Method: zip.Deflate}
	h.SetMode(0o600)
	h.Modified = time.Now()
	f, err := zw.CreateHeader(h)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	return err
}

func (s *Server) readPrivateKeyPEM(cert *storage.Certificate) ([]byte, error) {
	base, err := filepath.EvalSymlinks(filepath.Clean(s.Config.DataDir))
	if err != nil {
		return nil, apperr.New(apperr.Internal, "data directory is unavailable")
	}
	path := filepath.Clean(cert.PrivateKeyPath)
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return nil, apperr.NotFoundf("private key file not found")
	}
	if base == "" || (path != base && !strings.HasPrefix(path, base+string(os.PathSeparator))) {
		return nil, apperr.New(apperr.Internal, "private key path is outside the data directory")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, apperr.NotFoundf("private key file not found")
	}
	return raw, nil
}

func safeFileName(name string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, name)
}
