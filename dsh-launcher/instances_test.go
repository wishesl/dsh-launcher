package main

import (
	"path/filepath"
	"testing"
)

func TestResetRuntime(t *testing.T) {
	s := &instanceStore{path: filepath.Join(t.TempDir(), "instances.json")}
	s.loaded = []Instance{
		{ID: "a", Name: "x", Status: "ready", PID: 42, WebUrl: "http://127.0.0.1:3080"},
		{ID: "b", Name: "y", Status: "running", PID: 7, WebUrl: ""},
	}
	s.resetRuntime()
	for _, inst := range s.loaded {
		if inst.Status != "stopped" || inst.PID != 0 || inst.WebUrl != "" {
			t.Errorf("resetRuntime did not clear runtime fields: %+v", inst)
		}
	}
	if len(s.loaded) != 2 {
		t.Fatalf("resetRuntime changed instance count: %d", len(s.loaded))
	}
}

// Regression guard: iterating list()'s copies and assigning is a no-op, which
// is exactly how the old cold-start reset silently failed and leaked a stale
// "ready" status into the UI after a restart.
func TestResetRuntimeMustMutateStore(t *testing.T) {
	s := &instanceStore{path: filepath.Join(t.TempDir(), "instances.json")}
	s.loaded = []Instance{{ID: "a", Status: "ready", PID: 42, WebUrl: "http://127.0.0.1:3080"}}

	// The broken pattern (assign through list() copies) must NOT change the store.
	for _, inst := range s.list() {
		inst.Status = "stopped"
		inst.PID = 0
	}
	if s.loaded[0].Status != "ready" {
		t.Fatalf("precondition broken: list() copies should not mutate the store, got %+v", s.loaded[0])
	}

	// The real fix must mutate the store itself.
	s.resetRuntime()
	if s.loaded[0].Status != "stopped" || s.loaded[0].PID != 0 || s.loaded[0].WebUrl != "" {
		t.Errorf("resetRuntime failed to mutate the store: %+v", s.loaded[0])
	}
}

func TestInstanceStoreReorder(t *testing.T) {
	newStore := func() *instanceStore {
		s := &instanceStore{path: filepath.Join(t.TempDir(), "instances.json")}
		s.loaded = []Instance{{ID: "a"}, {ID: "b"}, {ID: "c"}}
		return s
	}
	order := func(s *instanceStore) string {
		out := ""
		for _, inst := range s.loaded {
			out += inst.ID
		}
		return out
	}

	t.Run("permutation rewrites the order", func(t *testing.T) {
		s := newStore()
		if !s.reorder([]string{"c", "a", "b"}) {
			t.Fatal("a valid permutation must be accepted")
		}
		if got := order(s); got != "cab" {
			t.Fatalf("order = %q, want cab", got)
		}
	})

	t.Run("same order is still accepted", func(t *testing.T) {
		s := newStore()
		if !s.reorder([]string{"a", "b", "c"}) {
			t.Fatal("an identity permutation must be accepted")
		}
		if got := order(s); got != "abc" {
			t.Fatalf("order = %q, want abc", got)
		}
	})

	// Every rejection path must leave the store exactly as it was: a stale
	// frontend list must never drop or duplicate an instance.
	for _, tc := range []struct {
		name string
		ids  []string
	}{
		{"short list", []string{"b", "a"}},
		{"long list", []string{"a", "b", "c", "a"}},
		{"unknown id", []string{"a", "b", "zzz"}},
		{"duplicate id", []string{"a", "a", "b"}},
		{"empty", nil},
	} {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			s := newStore()
			if s.reorder(tc.ids) {
				t.Fatalf("reorder(%v) must be rejected", tc.ids)
			}
			if got := order(s); got != "abc" {
				t.Fatalf("a rejected reorder must not touch the store, order = %q", got)
			}
		})
	}
}

