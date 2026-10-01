package main

import "embed"

// embeddedSelfRestart carries the dsh-launcher-plugin plugin sources bundled
// into the launcher binary. InstallSelfRestartPlugin materializes them into
// the profile's .dsh-builtin directory and installs via the regular pnpm
// command pipeline (see self_restart_install.go). The embed/ directory is the
// single source of the plugin package — keep it in sync with any future
// refactor (capability changes must bump its package.json version: the
// installed-copy vs embedded copy comparison in capabilities.go relies on it).
//
//go:embed embed/dsh-launcher-plugin
var embeddedSelfRestart embed.FS
