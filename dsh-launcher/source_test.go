package main

import (
	"path/filepath"
	"testing"
)

func TestSourceStartCommand(t *testing.T) {
	cases := []struct {
		name string
		inst Instance
		want string
	}{
		{"defaults to pnpm dsh web", Instance{Source: true}, "pnpm dsh web"},
		{"custom start command", Instance{Source: true, StartCmd: "pnpm dsh web --port 3081"}, "pnpm dsh web --port 3081"},
		{"start command with extra args appended", Instance{Source: true, StartCmd: " pnpm dsh web ", ExtraArgs: "--profile web"}, "pnpm dsh web --profile web"},
	}
	for _, c := range cases {
		if got := sourceStartCommand(c.inst); got != c.want {
			t.Errorf("%s: sourceStartCommand(%+v) = %q, want %q", c.name, c.inst, got, c.want)
		}
	}
}

func TestEffectiveSourceCmd(t *testing.T) {
	if got := effectiveSourceCmd("   ", "pnpm install"); got != "pnpm install" {
		t.Errorf("empty cmd should fall back to default, got %q", got)
	}
	if got := effectiveSourceCmd(" npm i ", "pnpm install"); got != "npm i" {
		t.Errorf("trimmed custom cmd expected, got %q", got)
	}
}

// SaveInstance must persist source-mode defaults when commands are left blank.
func TestSaveInstanceSourceDefaults(t *testing.T) {
	dir := t.TempDir()
	store := &instanceStore{path: filepath.Join(dir, "instances.json")}
	app := &App{store: store, processes: map[string]*managedProcess{}}

	list, err := app.SaveInstance(Instance{Name: "src", Directory: dir, Source: true})
	if err != nil {
		t.Fatalf("SaveInstance: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(list))
	}
	got := list[0]
	if !got.Source {
		t.Error("source flag not persisted")
	}
	if got.InitCmd != defaultSourceInitCmd || got.BuildCmd != defaultSourceBuildCmd || got.StartCmd != defaultSourceStartCmd {
		t.Errorf("source defaults wrong: init=%q build=%q start=%q", got.InitCmd, got.BuildCmd, got.StartCmd)
	}
	if got.Version != "latest" {
		t.Errorf("source instance version should default to latest, got %q", got.Version)
	}
}

// The service probe must learn the port from the source-mode start command
// (pnpm dsh web --port 3081) exactly like it does from extraArgs.
func TestInstanceServiceURLSource(t *testing.T) {
	cases := []struct {
		name      string
		startCmd  string
		extraArgs string
		want      string
		wantOK    bool
	}{
		{"source default → 3080", "", "", "http://127.0.0.1:3080", true},
		{"source --port in start cmd", "pnpm dsh web --port 3099", "", "http://127.0.0.1:3099", true},
		{"source --port 0 → unknown", "pnpm dsh web --port 0", "", "", false},
		{"source start cmd port beats extraArgs", "pnpm dsh web --port 3099", "--port 3081", "http://127.0.0.1:3099", true},
	}
	for _, c := range cases {
		inst := &Instance{Source: true, StartCmd: c.startCmd, ExtraArgs: c.extraArgs}
		got, ok := instanceServiceURL(inst)
		if got != c.want || ok != c.wantOK {
			t.Errorf("%s: instanceServiceURL(%q, %q) = (%q, %v), want (%q, %v)",
				c.name, c.startCmd, c.extraArgs, got, ok, c.want, c.wantOK)
		}
	}
}
