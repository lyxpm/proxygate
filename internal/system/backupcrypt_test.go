package system

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptedBackupRoundTrip(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "backup.zip")
	f, err := os.Create(src)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("database/proxygate.db")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("not-a-real-db")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "backup.pgbak")
	if err := EncryptBackupFile(src, dst, "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	enc, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !IsEncryptedBackup(enc) {
		t.Fatal("expected encrypted backup magic")
	}
	plain, err := DecryptBackup(bytes.NewReader(enc), "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(plain)
	_ = plain.Close()
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("decrypted backup does not match source")
	}
	plain, err = DecryptBackup(bytes.NewReader(enc), "wrong password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(plain); err == nil {
		t.Fatal("wrong password should fail while reading")
	}
	_ = plain.Close()
}
