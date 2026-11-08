package storage

import (
	"testing"
)

func TestResetConfigVersionsFile(t *testing.T) {
	path := t.TempDir() + "/proxygate.db"
	store, err := Open(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateConfigVersion(t.Context(), &ConfigVersion{Snapshot: "{}", Checksum: "x"}); err != nil {
		t.Fatal(err)
	}
	if latest, err := store.LatestConfigVersion(t.Context()); err != nil || latest.Version != 1 {
		t.Fatalf("seed version: %+v %v", latest, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ResetConfigVersionsFile(path); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if _, err := restored.LatestConfigVersion(t.Context()); err == nil {
		t.Fatal("config versions were not cleared")
	}
}
