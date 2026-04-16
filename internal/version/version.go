// Package version exposes build-time version info. Set via -ldflags at
// build time: -X github.com/polyxmedia/mnemos/internal/version.Version=...
package version

// Version is the current build version. "dev" by default; overridden via
// -ldflags during release builds.
var Version = "dev"
