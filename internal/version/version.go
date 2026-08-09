// Package version holds build identity for lynx binaries.
package version

// Version is overridden at link time via -ldflags "-X .../internal/version.Version=v2.2.2".
var Version = "2.2.2"
