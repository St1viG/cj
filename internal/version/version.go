// Package version reports the build version shared by the kondukter,
// kondukter-agent and kctl binaries.
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// Version is stamped at link time by the Makefile:
//
//	-ldflags "-X github.com/St1viG/cj/internal/version.Version=$(git describe --tags --always --dirty)"
//
// It stays empty in plain `go build` and `go run` builds.
var Version string

// Get returns the link-time version, else the main-module version the Go
// toolchain recorded from VCS, else "dev".
func Get() string {
	if Version != "" {
		return Version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}

// String formats the one-line banner each binary prints, for example
// "kctl 20f75a9-dirty (darwin/arm64, go1.27.3)".
func String(binary string) string {
	return fmt.Sprintf("%s %s (%s/%s, %s)", binary, Get(), runtime.GOOS, runtime.GOARCH, runtime.Version())
}
