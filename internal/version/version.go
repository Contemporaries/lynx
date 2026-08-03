// Package version holds build identity for lynx binaries.
package version

// Version is overridden at link time via -ldflags "-X .../internal/version.Version=v2.1.0".
var Version = "2.1.0"
