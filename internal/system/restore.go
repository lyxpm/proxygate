// Package system implements backup staging and restore-on-start.
package system

import (
	"archive/zip"
	"encoding/base64"
	"encoding/json"
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

// restoreRoots are the top-level data directories that a backup may replace.
var restoreRoots = []string{"database", "certificates", "acme", "config", "logs"}

type restoreJournal struct {
	Stage             string   `json:"stage"`
	Rollback          string   `json:"rollback"`
	Current           string   `json:"current,omitempty"`
	CurrentOldExisted bool     `json:"currentOldExisted,omitempty"`
	Swaps             []string `json:"swaps,omitempty"`
	Committed         bool     `json:"committed"`
	Pending           string   `json:"pending"`
}

const restoreJournalName = ".restore-journal.json"

// ApplyPendingRestore stages, validates and then swaps complete top-level
// directories. A journal makes interrupted swaps recoverable on the next
// process start.
func ApplyPendingRestore(dataDir string) error {
	if err := recoverRestoreJournal(dataDir); err != nil {
		return err
	}
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
	ts := time.Now().Format("20060102-150405.000000")
	stage := filepath.Join(dataDir, ".restore-stage-"+ts)
	rollback := filepath.Join(dataDir, ".restore-rollback-"+ts)
	pendingTemp := filepath.Join(dataDir, "tmp", "restore-pending-"+ts+".zip")
	if err := os.MkdirAll(stage, 0o700); err != nil {
		zr.Close()
		return err
	}
	cleanupStage := true
	defer func() {
		if cleanupStage {
			_ = os.RemoveAll(stage)
		}
	}()
	for _, f := range zr.File {
		name := path.Clean(filepath.ToSlash(f.Name))
		if name == "." || strings.HasSuffix(name, "/") {
			continue
		}
		target := filepath.Join(stage, filepath.FromSlash(name))
		if err := ensureInside(stage, target); err != nil {
			zr.Close()
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			zr.Close()
			return err
		}
		if err := extractFileAtomic(f, target); err != nil {
			zr.Close()
			return fmt.Errorf("stage %s: %w", name, err)
		}
	}
	if err := zr.Close(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(pendingTemp), 0o750); err != nil {
		return err
	}
	if err := os.Rename(pending, pendingTemp); err != nil {
		return fmt.Errorf("move pending restore: %w", err)
	}
	if err := os.MkdirAll(rollback, 0o700); err != nil {
		_ = os.Rename(pendingTemp, pending)
		return err
	}
	j := restoreJournal{Stage: stage, Rollback: rollback, Pending: pendingTemp}
	if err := writeRestoreJournal(dataDir, j); err != nil {
		_ = os.Rename(pendingTemp, pending)
		return err
	}
	fail := func(err error) error {
		if rbErr := rollbackRestore(dataDir, j); rbErr != nil {
			return fmt.Errorf("%w; restore rollback failed: %v", err, rbErr)
		}
		return err
	}
	for _, root := range restoreRoots {
		staged := filepath.Join(stage, root)
		if _, err := os.Stat(staged); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return fail(err)
		}
		j.Current = root
		current := filepath.Join(dataDir, root)
		_, statErr := os.Stat(current)
		j.CurrentOldExisted = statErr == nil
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return fail(statErr)
		}
		if err := writeRestoreJournal(dataDir, j); err != nil {
			return fail(err)
		}
		old := filepath.Join(rollback, root)
		if j.CurrentOldExisted {
			if err := os.MkdirAll(filepath.Dir(old), 0o700); err != nil {
				return fail(err)
			}
			if err := os.Rename(current, old); err != nil {
				return fail(err)
			}
		}
		if err := os.Rename(staged, current); err != nil {
			_ = os.Rename(old, current)
			return fail(err)
		}
		j.Swaps = append(j.Swaps, root)
		j.Current = ""
		j.CurrentOldExisted = false
		if err := writeRestoreJournal(dataDir, j); err != nil {
			return fail(err)
		}
	}
	for _, name := range []string{"proxygate.db", "proxy-manager.db"} {
		dbPath := filepath.Join(dataDir, "database", name)
		if _, err := os.Stat(dbPath); err == nil {
			if err := storage.ResetConfigVersionsFile(dbPath); err != nil {
				return fail(fmt.Errorf("reset restored config versions: %w", err))
			}
			break
		}
	}
	j.Committed = true
	if err := writeRestoreJournal(dataDir, j); err != nil {
		return fail(err)
	}
	cleanupStage = false
	_ = os.RemoveAll(stage)
	_ = os.RemoveAll(rollback)
	if err := archiveAppliedRestore(dataDir, pendingTemp); err != nil {
		// The restore itself is committed; keep the archive for diagnostics.
		return err
	}
	_ = os.Remove(filepath.Join(dataDir, restoreJournalName))
	return nil
}

func writeRestoreJournal(dataDir string, j restoreJournal) error {
	raw, err := json.Marshal(j)
	if err != nil {
		return err
	}
	path := filepath.Join(dataDir, restoreJournalName)
	tmp, err := os.CreateTemp(dataDir, ".restore-journal-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(raw); err != nil {
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

func readRestoreJournal(dataDir string) (restoreJournal, bool, error) {
	var j restoreJournal
	raw, err := os.ReadFile(filepath.Join(dataDir, restoreJournalName))
	if errors.Is(err, os.ErrNotExist) {
		return j, false, nil
	}
	if err != nil {
		return j, false, err
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		return j, false, err
	}
	return j, true, nil
}

func recoverRestoreJournal(dataDir string) error {
	j, ok, err := readRestoreJournal(dataDir)
	if err != nil {
		return fmt.Errorf("read restore journal: %w", err)
	}
	if !ok {
		return nil
	}
	if !j.Committed {
		if err := rollbackRestore(dataDir, j); err != nil {
			return fmt.Errorf("recover interrupted restore: %w", err)
		}
		return nil
	}
	_ = os.RemoveAll(j.Stage)
	_ = os.RemoveAll(j.Rollback)
	if j.Pending != "" {
		_ = archiveAppliedRestore(dataDir, j.Pending)
	}
	return os.Remove(filepath.Join(dataDir, restoreJournalName))
}

func rollbackRestore(dataDir string, j restoreJournal) error {
	// The current directory may have been swapped but not yet recorded.
	if j.Current != "" && allowedRoots[j.Current] {
		if err := rollbackCurrent(dataDir, j); err != nil {
			return err
		}
	}
	for i := len(j.Swaps) - 1; i >= 0; i-- {
		if err := rollbackRoot(dataDir, j.Rollback, j.Swaps[i]); err != nil {
			return err
		}
	}
	_ = os.RemoveAll(j.Stage)
	_ = os.RemoveAll(j.Rollback)
	if j.Pending != "" {
		_ = archiveAppliedRestore(dataDir, j.Pending)
	}
	return os.Remove(filepath.Join(dataDir, restoreJournalName))
}

func rollbackCurrent(dataDir string, j restoreJournal) error {
	if !allowedRoots[j.Current] {
		return fmt.Errorf("unexpected restore root %q", j.Current)
	}
	current := filepath.Join(dataDir, j.Current)
	old := filepath.Join(j.Rollback, j.Current)
	if _, err := os.Stat(old); err == nil {
		if err := os.RemoveAll(current); err != nil {
			return err
		}
		return os.Rename(old, current)
	}
	if !j.CurrentOldExisted {
		// The old root did not exist; remove a possibly installed new root.
		return os.RemoveAll(current)
	}
	// The old root still exists in place because the rename had not happened.
	return nil
}

func rollbackRoot(dataDir, rollback, root string) error {
	if !allowedRoots[root] {
		return fmt.Errorf("unexpected restore root %q", root)
	}
	current := filepath.Join(dataDir, root)
	old := filepath.Join(rollback, root)
	if _, err := os.Stat(old); err == nil {
		if err := os.RemoveAll(current); err != nil {
			return err
		}
		return os.Rename(old, current)
	}
	// The old directory did not exist. Remove the partially installed one.
	return os.RemoveAll(current)
}

func archiveAppliedRestore(dataDir, pending string) error {
	if _, err := os.Stat(pending); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	applied := filepath.Join(dataDir, "backups", "restore-applied-"+time.Now().Format("20060102-150405")+".zip")
	if err := os.MkdirAll(filepath.Dir(applied), 0o750); err != nil {
		return err
	}
	if err := os.Rename(pending, applied); err != nil {
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
