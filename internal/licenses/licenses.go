// Package licenses reports the open source license of every module
// the binary links against. The module list comes from the build
// info embedded in the binary, the license text is read from the
// local Go module cache, and the text is classified against a table
// of common SPDX license signatures. It is deliberately hand-rolled
// to keep the dependency tree small - the same reason config does
// not use viper.
package licenses

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"
)

// Entry is one dependency with its resolved license.
type Entry struct {
	Module  string
	Version string
	License string // SPDX identifier, or "unknown"
}

// List returns the dependencies of the running binary with their
// licenses, sorted by module path. It requires the build info the Go
// toolchain embeds in module-built binaries and the modules in the
// local cache.
func List() ([]Entry, error) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return nil, errors.New("no build info in binary; build with modules")
	}
	cache, err := moduleCache()
	if err != nil {
		return nil, err
	}

	var entries []Entry
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
	return entries, nil
}

// moduleCache locates the local module cache: $GOMODCACHE, else the
// first entry of $GOPATH, else the default ~/go.
func moduleCache() (string, error) {
	if c := os.Getenv("GOMODCACHE"); c != "" {
		return c, nil
	}
	if p := os.Getenv("GOPATH"); p != "" {
		first := filepath.SplitList(p)[0]
		return filepath.Join(first, "pkg", "mod"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "go", "pkg", "mod"), nil
}

// licenseFilePrefixes lists the base names a license file may carry,
// matched case-insensitively with any extension ("LICENSE",
// "COPYING", "LICENSE.md", ...).
var licenseFilePrefixes = []string{"LICENSE", "LICENCE", "COPYING", "COPYRIGHT"}

// maxSample bounds how much of a license file is read: the signature
// phrases live at the top of every known form.
const maxSample = 64 * 1024

// resolve finds the license of one module in the cache and
// classifies it. A missing cache entry, missing license file, or
// unrecognized text all yield "unknown" rather than an error - a
// licenses listing should degrade, not fail.
func resolve(cache, path, version string) string {
	if version == "" {
		return "unknown"
	}
	dir := filepath.Join(cache, cachePath(path)+"@"+version)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "unknown"
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.ToUpper(e.Name())
		base := strings.TrimSuffix(name, filepath.Ext(name))
		if !matchesAny(base, licenseFilePrefixes) {
			continue
		}
		text, err := readHead(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		if id := classify(text); id != "" {
			return id
		}
	}
	return "unknown"
}

// matchesAny reports whether name equals one of the prefixes or
// starts with one followed by a separator, so "LICENSE-MIT" and
// "COPYING.LESSER" also qualify.
func matchesAny(name string, prefixes []string) bool {
	for _, p := range prefixes {
		if name == p || strings.HasPrefix(name, p+"-") || strings.HasPrefix(name, p+"_") {
			return true
		}
	}
	return false
}

func readHead(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]byte, maxSample)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return "", err
	}
	return string(buf[:n]), nil
}

// cachePath encodes a module path the way the module cache does:
// every uppercase letter becomes a bang plus its lowercase form.
func cachePath(path string) string {
	var b strings.Builder
	for _, r := range path {
		if r >= 'A' && r <= 'Z' {
			b.WriteByte('!')
			b.WriteRune(r - 'A' + 'a')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// patterns pairs signature text (matched case-insensitively) with
// SPDX identifiers. Order matters: signatures contained in longer
// licenses come first (AGPL before GPL, BSD-3 before BSD-2).
var patterns = []struct {
	id string
	re *regexp.Regexp
}{
	{"MPL-2.0", regexp.MustCompile(`(?s)Mozilla Public License.{0,80}Version 2\.0`)},
	{"Apache-2.0", regexp.MustCompile(`(?s)Apache License.{0,80}Version 2\.0`)},
	{"AGPL-3.0-only", regexp.MustCompile(`(?s)GNU AFFERO General Public License.{0,80}Version 3`)},
	{"GPL-3.0-only", regexp.MustCompile(`(?s)GNU General Public License.{0,80}Version 3`)},
	{"GPL-2.0-only", regexp.MustCompile(`(?s)GNU General Public License.{0,80}Version 2`)},
	{"LGPL-3.0-only", regexp.MustCompile(`(?s)GNU LESSER GENERAL PUBLIC LICENSE.{0,80}Version 3`)},
	{"LGPL-2.1-only", regexp.MustCompile(`(?s)GNU LESSER GENERAL PUBLIC LICENSE.{0,80}Version 2\.1`)},
	{"BSD-3-Clause", regexp.MustCompile(`(?s)Redistribution and use in source and binary forms.{0,600}Neither the name`)},
	{"BSD-2-Clause", regexp.MustCompile(`Redistribution and use in source and binary forms`)},
	{"MIT", regexp.MustCompile(`MIT License|Permission is hereby granted, free of charge`)},
	{"ISC", regexp.MustCompile(`ISC License|Permission to use, copy, modify, and/or distribute this software`)},
	{"Unlicense", regexp.MustCompile(`unlicense\.org|free and unencumbered software released into the public domain`)},
	{"Zlib", regexp.MustCompile(`Zlib License|This software is provided 'as-is'`)},
	{"BSL-1.0", regexp.MustCompile(`Boost Software License`)},
}

// classify maps license text to an SPDX identifier, or "" when no
// signature matches.
func classify(text string) string {
	for _, p := range patterns {
		if p.re.MatchString(text) {
			return p.id
		}
	}
	return ""
}
