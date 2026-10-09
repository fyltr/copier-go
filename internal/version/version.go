// Package version holds the build-time version string.
package version

import "runtime/debug"

// Version is set by the linker at build time via -ldflags. Without it (e.g.
// `go install github.com/fyltr/copier-go/cmd/copier@v0.1.0`), the module
// version recorded in the build info is used.
var Version = "dev"

// Upstream is the upstream Copier version whose behavior this port tracks.
// Templates' `_min_copier_version` is checked against it, not against Version.
const Upstream = "9.18.2"

const modulePath = "github.com/fyltr/copier-go"

func init() {
	if Version != "dev" {
		return
	}
	if v := moduleVersion(); v != "" && v != "(devel)" {
		Version = v
	}
}

// moduleVersion returns the copier-go module version from the build info,
// whether copier-go is the main module or a dependency.
func moduleVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	if bi.Main.Path == modulePath {
		return bi.Main.Version
	}
	for _, dep := range bi.Deps {
		if dep.Path == modulePath {
			if dep.Replace != nil {
				return dep.Replace.Version
			}
			return dep.Version
		}
	}
	return ""
}
