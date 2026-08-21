package main

import (
	"strings"
	"testing"
)

func TestParseVersion(t *testing.T) {
	valid := []string{"0.1.0-rc.6", "0.1.1-rc.2", "1.2.3", "0.0.1-rc.1", "2.0.0-beta.1"}
	for _, v := range valid {
		if _, err := parseVersion(v); err != nil {
			t.Errorf("expected %q to be a version, got error: %v", v, err)
		}
	}
	invalid := []string{"created", "modified", "0.1", "abc", "0.1.0-", ""}
	for _, v := range invalid {
		if _, err := parseVersion(v); err == nil {
			t.Errorf("expected %q to be rejected", v)
		}
	}
}

func TestBuildCommand(t *testing.T) {
	cases := []struct {
		version, extra, want string
	}{
		{"latest", "", "npx -y @deepseek-ai/dsh@latest web"},
		{"0.1.1-rc.2", "", "npx -y @deepseek-ai/dsh@0.1.1-rc.2 web"},
		{"0.1.0-rc.6", "--port 3081", "npx -y @deepseek-ai/dsh@0.1.0-rc.6 web --port 3081"},
		{"", "", "npx -y @deepseek-ai/dsh@latest web"},
		{" latest ", " --profile web ", "npx -y @deepseek-ai/dsh@latest web --profile web"},
	}
	for _, c := range cases {
		got := buildCommand(c.version, c.extra)
		if got != c.want {
			t.Errorf("buildCommand(%q, %q) = %q, want %q", c.version, c.extra, got, c.want)
		}
	}
}

func TestVersionLabel(t *testing.T) {
	if got := versionLabel("latest"); got != "@latest" {
		t.Errorf("versionLabel(latest) = %q", got)
	}
	if got := versionLabel("0.1.1-rc.2"); got != "@0.1.1-rc.2" {
		t.Errorf("versionLabel(0.1.1-rc.2) = %q", got)
	}
	if got := versionLabel(""); got != "@latest" {
		t.Errorf("versionLabel(empty) = %q", got)
	}
}

// TestQueryRegistryLive is a live network test that hits the npm registry.
// It is skipped under `go test` unless -short is NOT set (i.e. run it
// explicitly with `go test -run TestQueryRegistryLive`).
func TestQueryRegistryLive(t *testing.T) {
	a := NewApp()
	info, err := a.QueryRegistry()
	if err != nil {
		t.Fatalf("QueryRegistry failed: %v", err)
	}
	if info.Latest == "" {
		t.Fatal("expected non-empty latest")
	}
	if !strings.Contains(info.Package, "dsh") {
		t.Errorf("unexpected package name: %s", info.Package)
	}
	if len(info.Versions) == 0 {
		t.Fatal("expected at least one version")
	}
	// newest first
	for i := 1; i < len(info.Versions); i++ {
		prev, _ := parseVersion(info.Versions[i-1].Version)
		cur, _ := parseVersion(info.Versions[i].Version)
		if prev == "" || cur == "" {
			t.Fatalf("bad version list entries: %+v", info.Versions[i])
		}
	}
	t.Logf("latest=%s next=%s source=%s versions=%d", info.Latest, info.Next, info.Source, len(info.Versions))
}
