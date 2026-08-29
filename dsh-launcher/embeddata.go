package main

import "embed"

// embeddedSelfRestart carries the dsh-self-mcp plugin sources bundled into
// the launcher binary. InstallSelfRestartPlugin materializes them into the
// profile's .dsh-builtin directory and installs via the regular pnpm command
// pipeline (see self_restart_install.go). The embed/ directory is the single
// source of the plugin package — keep it in sync with any future refactor.
//
//go:embed embed/dsh-self-mcp
var embeddedSelfRestart embed.FS
