package config

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/proxygate/proxygate/internal/storage"
	"github.com/proxygate/proxygate/internal/system"
)

func TestLoadAppliesPendingRestore(t *testing.T) {
	dataDir := t.TempDir()
	for _, sub := range []string{"database", "config", "tmp", "certificates", "acme", "backups"} {
		if err := os.MkdirAll(filepath.Join(dataDir, sub), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	dbPath := filepath.Join(dataDir, "database", "proxygate.db")
	store, err := storage.Open(dbPath, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	dbBytes, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{7}, 32)
	encodedKey := base64.StdEncoding.EncodeToString(key) + "\n"

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name string, data []byte) {
		f, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	write("database/proxygate.db", dbBytes)
	write("config/master.key", []byte(encodedKey))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := system.ScheduleRestore(dataDir, bytes.NewReader(buf.Bytes()), int64(buf.Len())); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PG_DATA_DIR", dataDir)
	t.Setenv("PG_ADMIN_PASSWORD", "")
	t.Setenv("PG_MASTER_KEY", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(cfg.MasterKey, key) {
		t.Fatalf("restored master key mismatch")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "config", "restore-pending.zip")); !os.IsNotExist(err) {
		t.Fatalf("pending archive should be consumed: %v", err)
	}
}
