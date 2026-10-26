package platform

import "runtime"

// Version and Commit are overridden at build time through -ldflags
// "-X github.com/Casper-Mars/echuan/internal/platform.Version=..." (see
// Makefile). A plain "go build" reports dev/none.
var (
	Version = "dev"
	Commit  = "none"
)

// GoVersion reports the Go runtime version the binary was built with.
func GoVersion() string { return runtime.Version() }
