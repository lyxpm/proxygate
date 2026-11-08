// Package system implements backup staging and restore-on-start.
package system

import (
	"archive/zip"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/proxygate/proxygate/internal/storage"
)

// PendingFile is the staged restore archive inside the data directory.
const PendingFile = "config/restore-pending.zip"

var allowedRoots = map[string]bool{
	"database":     true,
	"certificates": true,
	"acme":         true,
	"config":       true,
	"logs":         true,
}

// ScheduleRestore validates an uploaded backup and stages it for the next
// start. It never replaces live files: the running database stays untouched.
func ScheduleRestore(dataDir string, r io.Reader, maxBytes int64) error {
	tmp, err := os.CreateTemp(filepath.Join(dataDir, "tmp"), "restore-*.zip")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	written, err := io.Copy(tmp, io.LimitReader(r, maxBytes+1))
	if err != nil {
		tmp.Close()
		return fmt.Errorf("read backup: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if written > maxBytes {
		return fmt.Errorf("backup exceeds %d bytes", maxBytes)
	}
	zr, err := zip.OpenReader(tmpName)
	if err != nil {
		return fmt.Errorf("open backup zip: %w", err)
	}
	if err := validateArchive(zr.File); err != nil {
		zr.Close()
		return err
	}
	if err := validateDatabase(dataDir, zr.File); err != nil {
		zr.Close()
		return err
	}
	if err := zr.Close(); err != nil {
		return err
	}
	pending := filepath.Join(dataDir, filepath.FromSlash(PendingFile))
	if err := os.MkdirAll(filepath.Dir(pending), 0o750); err != nil {
		return err
	}
	if err := os.Rename(tmpName, pending); err != nil {
		return fmt.Errorf("stage backup: %w", err)
	}
	return nil
}

func validateArchive(files []*zip.File) error {
	hasDB := false
	var total uint64
	for _, f := range files {
		total += f.UncompressedSize64
		if f.UncompressedSize64 > 1<<30 || total > 2<<30 {
			return errors.New("backup expands beyond the allowed size")
		}
		name := path.Clean(filepath.ToSlash(f.Name))
		if name == "." || strings.HasPrefix(name, "../") || strings.HasPrefix(name, "/") {
			return fmt.Errorf("unsafe path in backup: %q", f.Name)
		}
		if strings.Contains(name, "..") {
			return fmt.Errorf("unsafe path in backup: %q", f.Name)
		}
		root := strings.SplitN(name, "/", 2)[0]
		if !allowedRoots[root] {
			return fmt.Errorf("unexpected entry in backup: %q", f.Name)
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlinks are not allowed in backup: %q", f.Name)
		}
		if name == "database/proxygate.db" || name == "database/proxy-manager.db" {
			hasDB = true
		}
		if name == PendingFile {
			return fmt.Errorf("backup must not contain %s", PendingFile)
		}
		if name == "config/master.key" {
			if err := validateMasterKey(f); err != nil {
				return err
			}
		}
	}
	if !hasDB {
		return errors.New("backup is missing database/proxygate.db")
	}
	return nil
}

func validateDatabase(dataDir string, files []*zip.File) error {
	var dbFile *zip.File
	for _, f := range files {
		clean := path.Clean(filepath.ToSlash(f.Name))
		if clean == "database/proxygate.db" || clean == "database/proxy-manager.db" {
			dbFile = f
			break
		}
	}
	if dbFile == nil {
		return errors.New("backup database is missing")
	}
	dir, err := os.MkdirTemp(filepath.Join(dataDir, "tmp"), "restore-check-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	dst := filepath.Join(dir, "check.db")
	if err := extractFile(dbFile, dst); err != nil {
		return fmt.Errorf("extract database: %w", err)
	}
	store, err := storage.Open(dst, false)
	if err != nil {
		return fmt.Errorf("backup database is not usable: %w", err)
	}
	defer store.Close()
	var count int64
	if err := store.DB.Raw("SELECT count(*) FROM sites").Scan(&count).Error; err != nil {
		return fmt.Errorf("backup database schema is invalid: %w", err)
	}
	return nil
}

func validateMasterKey(f *zip.File) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	raw, err := io.ReadAll(io.LimitReader(rc, 1024))
	if err != nil {
		return err
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(decoded) != 32 {
		return errors.New("backup config/master.key is not a valid base64 32-byte key")
	}
	return nil
}

// ApplyPendingRestore extracts a staged archive into dataDir. It is invoked by
// config.Load before the database is opened.
func ApplyPendingRestore(dataDir string) error {
	pending := filepath.Join(dataDir, filepath.FromSlash(PendingFile))
	if _, err := os.Stat(pending); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	zr, err := zip.OpenReader(pending)
	if err != nil {
		return fmt.Errorf("open pending restore: %w", err)
	}
	if err := validateArchive(zr.File); err != nil {
		zr.Close()
		return err
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		_ = os.Remove(filepath.Join(dataDir, "database", "proxygate.db"+suffix))
		_ = os.Remove(filepath.Join(dataDir, "database", "proxy-manager.db"+suffix))
	}
	for _, f := range zr.File {
		name := path.Clean(filepath.ToSlash(f.Name))
		if name == "." || strings.HasSuffix(name, "/") {
			continue
		}
		target := filepath.Join(dataDir, filepath.FromSlash(name))
		if err := ensureInside(dataDir, target); err != nil {
			zr.Close()
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			zr.Close()
			return err
		}
		if err := extractFileAtomic(f, target); err != nil {
			zr.Close()
			return fmt.Errorf("restore %s: %w", name, err)
		}
	}
	if err := zr.Close(); err != nil {
		return err
	}
	for _, name := range []string{"proxygate.db", "proxy-manager.db"} {
		dbPath := filepath.Join(dataDir, "database", name)
		if _, err := os.Stat(dbPath); err == nil {
			if err := storage.ResetConfigVersionsFile(dbPath); err != nil {
				return fmt.Errorf("reset restored config versions: %w", err)
			}
			break
		}
	}
	applied := filepath.Join(dataDir, "backups", "restore-applied-"+time.Now().Format("20060102-150405")+".zip")
	if err := os.MkdirAll(filepath.Dir(applied), 0o750); err != nil || os.Rename(pending, applied) != nil {
		_ = os.Remove(pending)
	}
	return nil
}

func ensureInside(root, target string) error {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("restore path escapes data directory: %q", target)
	}
	return nil
}

func extractFile(f *zip.File, dst string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func extractFileAtomic(f *zip.File, dst string) error {
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".restore-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	rc, err := f.Open()
	if err != nil {
		tmp.Close()
		return err
	}
	if _, err := io.Copy(tmp, rc); err != nil {
		rc.Close()
		tmp.Close()
		return err
	}
	rc.Close()
	mode := os.FileMode(0o640)
	if strings.HasSuffix(dst, "master.key") || strings.HasSuffix(dst, ".key") {
		mode = 0o600
	}
	if err := tmp.Chmod(mode); err != nil {
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
	return os.Rename(name, dst)
}
