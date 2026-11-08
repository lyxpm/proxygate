package application

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/proxygate/proxygate/internal/storage"
)

func TestUpdateMergesNodes(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(filepath.Join(t.TempDir(), "test.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc := NewService(store, func(context.Context) error { return nil }, nil)
	created, err := svc.Create(ctx, Input{
		Name: "web", Strategy: "round_robin",
		Nodes: []NodeInput{
			{Address: "http://127.0.0.1:8080", Weight: 1},
			{Address: "http://127.0.0.1:8081", Weight: 2},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	firstID := created.Nodes[0].ID
	updated, err := svc.Update(ctx, created.ID, Input{
		Name: "web2", Strategy: "weighted_round_robin",
		Nodes: []NodeInput{
			{ID: firstID, Address: "http://127.0.0.1:8080", Weight: 5},
			{Address: "http://127.0.0.1:8082", Weight: 1},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "web2" || len(updated.Nodes) != 2 {
		t.Fatalf("unexpected update: %+v", updated)
	}
	var kept *storage.ApplicationNode
	for i := range updated.Nodes {
		if updated.Nodes[i].ID == firstID {
			kept = &updated.Nodes[i]
		}
	}
	if kept == nil || kept.Weight != 5 {
		t.Fatalf("existing node not preserved: %+v", updated.Nodes)
	}
	if _, err := svc.Update(ctx, created.ID, Input{Name: "web", Nodes: nil}); err == nil {
		t.Fatal("expected validation error for empty nodes")
	}
}
