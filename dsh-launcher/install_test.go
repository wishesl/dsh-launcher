package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestInstallToDirectoryLive exercises the full InstallToDirectory method
// against the pre-installed poc directory (fast: packages already in the
// pnpm store). Skips when the poc dir is absent.
func TestInstallToDirectoryLive(t *testing.T) {
	if testing.Short() {
		t.Skip("live test")
	}
	// Tests run with CWD = the package dir, so a repo-relative path works and
	// keeps machine-specific absolute paths out of the repo.
	poc := filepath.Join("build", "bin", "dsh-vsn", "poc-0.1.1-rc2")
	if _, err := os.Stat(filepath.Join(poc, "node_modules")); err != nil {
		t.Skip("poc dir not installed, skipping live install test")
	}

	dir := t.TempDir()
	store := &instanceStore{path: filepath.Join(dir, "instances.json")}
	app := &App{store: store, processes: map[string]*managedProcess{}}

	// Point a test instance at the poc directory (version already installed).
	store.add(Instance{
		ID:        "t1",
		Name:      "poc",
		Directory: poc,
		Version:   "0.1.1-rc.2",
		PkgMgr:    "local", // recommended mode: install a local copy to the dir
	})

	list, err := app.InstallToDirectory("t1")
	if err != nil {
		t.Fatalf("InstallToDirectory failed: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(list))
	}
	if got := list[0].LocalVersion; got != "0.1.1-rc.2" {
		t.Errorf("localVersion = %q, want 0.1.1-rc.2", got)
	}
	bin := filepath.Join(poc, "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js")
	if _, err := os.Stat(bin); err != nil {
		t.Errorf("readable source missing: %v", err)
	}
	t.Logf("OK: localVersion=%s, source readable", list[0].LocalVersion)
}
