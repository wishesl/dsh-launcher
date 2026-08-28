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
