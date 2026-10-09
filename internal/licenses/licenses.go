// Package licenses reports the open source license of every module
// the binary links against. The module list comes from the build
// info embedded in the binary, the license text is read from the
// local Go module cache, and the text is classified against a table
// of common SPDX license signatures. It is deliberately hand-rolled
// to keep the dependency tree small - the same reason config does
// not use viper.
//
// The package is split by responsibility: this file walks the build
// info, cache.go finds and reads license files, classify.go maps
// text to SPDX identifiers.
package licenses

import (
	"errors"
	"path/filepath"
	"runtime/debug"
	"sort"
)

// Entry is one dependency with its resolved license.
type Entry struct {
	Module  string
	Version string
	License string // SPDX identifier(s) joined with " / ", or "unknown"
}

// List returns the dependencies of the running binary with their
// licenses, sorted by module path. It requires the build info the Go
// toolchain embeds in module-built binaries and the modules in the
// local cache. A dual-licensed module reports every matching
// identifier (e.g. "Apache-2.0 / MIT").
func List() ([]Entry, error) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return nil, errors.New("no build info in binary; build with modules")
	}
	cache, err := moduleCache()
	if err != nil {
		return nil, err
	}
	return resolveAll(info, cache), nil
}

// resolveAll maps every dependency of the build info to its license,
// sorted by module path. Split from List so tests can drive it with
// a synthetic build info and a synthetic cache.
func resolveAll(info *debug.BuildInfo, cache string) []Entry {
	entries := make([]Entry, 0, len(info.Deps))
	for _, dep := range info.Deps {
		path, version := dep.Path, dep.Version
		if dep.Replace != nil {
			path, version = dep.Replace.Path, dep.Replace.Version
		}
		entries = append(entries, Entry{
			Module:  path,
			Version: version,
			License: resolve(cache, path, version),
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Module < entries[j].Module })
	return entries
}

// resolve finds the license of one module in the cache and
// classifies it. A missing cache entry, missing license file, or
// unrecognized text all yield "unknown" rather than an error - a
// licenses listing should degrade, not fail.
func resolve(cache, path, version string) string {
	if version == "" {
		return "unknown"
	}
	dir := filepath.Join(cache, cachePath(path)+"@"+version)
	text, ok := licenseText(dir)
	if !ok {
		return "unknown"
	}
	return classify(text)
}
