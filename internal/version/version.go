package version

import "runtime"

// These are overridden at build time with -ldflags.
var (
	Version   = "dev"
	Commit    = "none"
	BuildDate = "unknown"
)

func String() string {
	return Version + " (" + Commit + ", " + BuildDate + ", " + runtime.Version() + ", " + runtime.GOOS + "/" + runtime.GOARCH + ")"
}
