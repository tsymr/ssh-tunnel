package main

import (
	"path/filepath"
	"testing"
)

func TestStoreReorderPersistsOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tunnels.json")
	store := NewStore(path)
	tunnels := []*Tunnel{
		{ID: "a", Label: "A", CreatedAt: 10, Order: 10, User: "u", Host: "h", LocalPort: 1001, RemotePort: 2001, ForwardMode: "local", AuthMethod: "key"},
		{ID: "b", Label: "B", CreatedAt: 20, Order: 20, User: "u", Host: "h", LocalPort: 1002, RemotePort: 2002, ForwardMode: "local", AuthMethod: "key"},
		{ID: "c", Label: "C", CreatedAt: 30, Order: 30, User: "u", Host: "h", LocalPort: 1003, RemotePort: 2003, ForwardMode: "local", AuthMethod: "key"},
	}
	for _, tunnel := range tunnels {
		if err := store.Put(tunnel); err != nil {
			t.Fatalf("put %s: %v", tunnel.ID, err)
		}
	}
	if err := store.Reorder([]string{"c", "a", "b"}); err != nil {
		t.Fatalf("reorder: %v", err)
	}

	reloaded := NewStore(path)
	if err := reloaded.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	mgr := NewManager(reloaded, t.TempDir())
	got := mgr.List()
	want := []string{"c", "a", "b"}
	if len(got) != len(want) {
		t.Fatalf("got %d tunnels, want %d", len(got), len(want))
	}
	for i, wantID := range want {
		if got[i].ID != wantID {
			t.Fatalf("got id at %d = %s, want %s", i, got[i].ID, wantID)
		}
		if got[i].Order != int64(i+1) {
			t.Fatalf("got order for %s = %d, want %d", got[i].ID, got[i].Order, i+1)
		}
	}
}
