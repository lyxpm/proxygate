package system

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/proxygate/proxygate/internal/storage"
)

func makeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		f, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func prepareDataDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"tmp", "database", "config", "certificates", "acme", "backups"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestScheduleAndApplyRestore(t *testing.T) {
	source := prepareDataDir(t)
	dbPath := filepath.Join(source, "database", "proxygate.db")
	store, err := storage.Open(dbPath, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateConfigVersion(t.Context(), &storage.ConfigVersion{Snapshot: "{}", Checksum: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	dbBytes, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	keyValue := base64.StdEncoding.EncodeToString(make([]byte, 32)) + "\n"
	archive := makeZip(t, map[string]string{
		"database/proxygate.db":   string(dbBytes),
		"config/master.key":       keyValue,
		"certificates/1/cert.pem": "cert",
	})
	target := prepareDataDir(t)
	if err := ScheduleRestore(target, bytes.NewReader(archive), int64(len(archive))); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, filepath.FromSlash(PendingFile))); err != nil {
		t.Fatalf("pending archive missing: %v", err)
	}
	if err := ApplyPendingRestore(target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, filepath.FromSlash(PendingFile))); !os.IsNotExist(err) {
		t.Fatalf("pending archive should be consumed: %v", err)
	}
	key, err := os.ReadFile(filepath.Join(target, "config", "master.key"))
	if err != nil || string(key) != keyValue {
		t.Fatalf("master key restore failed: %q %v", key, err)
	}
	restored, err := storage.Open(filepath.Join(target, "database", "proxygate.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if _, err := restored.ListSites(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := restored.LatestConfigVersion(t.Context()); err == nil {
		t.Fatal("restored config versions should be reset")
	}
}

func TestScheduleRejectsUnsafeAndIncompleteArchives(t *testing.T) {
	target := prepareDataDir(t)
	zipSlip := makeZip(t, map[string]string{
		"database/proxygate.db": "x",
		"database/../../evil":   "x",
	})
	if err := ScheduleRestore(target, bytes.NewReader(zipSlip), int64(len(zipSlip))); err == nil {
		t.Fatal("expected zip slip rejection")
	}
	missing := makeZip(t, map[string]string{"config/master.key": "x"})
	if err := ScheduleRestore(target, bytes.NewReader(missing), int64(len(missing))); err == nil {
		t.Fatal("expected missing database rejection")
	}
	badDB := makeZip(t, map[string]string{"database/proxygate.db": "not sqlite"})
	if err := ScheduleRestore(target, bytes.NewReader(badDB), int64(len(badDB))); err == nil {
		t.Fatal("expected invalid database rejection")
	}
	badKey := makeZip(t, map[string]string{
		"database/proxygate.db": "x",
		"config/master.key":     "not-base64",
	})
	if err := ScheduleRestore(target, bytes.NewReader(badKey), int64(len(badKey))); err == nil {
		t.Fatal("expected invalid master key rejection")
	}
}
