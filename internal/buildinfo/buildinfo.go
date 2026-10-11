// Package buildinfo reads how the running binary was built: the
// version stamped in via -ldflags, the git revision and commit time
// the Go toolchain embeds when building from a git repository
// (-buildvcs), the Go version, and the platform. The cmd layer owns
// only the presentation.
package buildinfo

import (
	"runtime"
	"runtime/debug"
)

// Info is the build story of one binary.
type Info struct {
	Version       string // release version stamped in via -ldflags
	Module        string
	ModuleVersion string
	Commit        string // short SHA, with " (dirty)" for an unclean tree
	Built         string // VCS commit time, empty for source-tarball builds
	Go            string
	Platform      string
	Available     bool // false when the binary carries no build info
}

// Collect gathers the build info of the running binary. version is
// the value the build stamped in via -ldflags (usually "dev").
func Collect(version string) Info {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return Info{Version: version}
	}
	return FromBuildInfo(version, info)
}

// FromBuildInfo maps a build info document into Info. Split from
// Collect so tests can drive it with a synthetic document.
func FromBuildInfo(version string, info *debug.BuildInfo) Info {
	rev, built, modified := vcsSettings(info)
	return Info{
		Version:       version,
		Module:        info.Main.Path,
		ModuleVersion: info.Main.Version,
		Commit:        commitLabel(rev, modified),
		Built:         built,
		Go:            info.GoVersion,
		Platform:      runtime.GOOS + "/" + runtime.GOARCH,
		Available:     true,
	}
}

// vcsSettings pulls the git revision, commit time, and dirty flag
// the Go toolchain embeds when building from a git repository
// (-buildvcs defaults to on). All three are empty for source
// tarball builds.
func vcsSettings(info *debug.BuildInfo) (revision, built, modified string) {
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.time":
			built = s.Value
		case "vcs.modified":
			modified = s.Value
		}
	}
	return revision, built, modified
}

// commitLabel shortens the full SHA to something readable and
// marks a build from a dirty working tree.
func commitLabel(rev, modified string) string {
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if modified == "true" {
		return rev + " (dirty)"
	}
	return rev
}
