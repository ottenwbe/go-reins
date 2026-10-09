package licenses

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
)

func TestClassifyKnownLicenses(t *testing.T) {
	cases := map[string]string{
		"MIT License\n\nCopyright (c) 2026 someone":                                                       "MIT",
		"Permission is hereby granted, free of charge, to any person":                                     "MIT",
		"Apache License\nVersion 2.0, January 2004":                                                       "Apache-2.0",
		"Mozilla Public License\nVersion 2.0":                                                             "MPL-2.0",
		"Boost Software License - Version 1.0":                                                            "BSL-1.0",
		"ISC License\n\nPermission to use, copy, modify, and/or distribute":                               "ISC",
		"This is free and unencumbered software released into the public domain.":                         "Unlicense",
		"Redistribution and use in source and binary forms, with or without\nmodification, are permitted": "BSD-2-Clause",
	}
	for text, want := range cases {
		if got := classify(text); got != want {
			t.Errorf("classify(%q) = %q, want %q", text, got, want)
		}
	}
}

func TestClassifyBSD3BeforeBSD2(t *testing.T) {
	text := `Redistribution and use in source and binary forms, with or
without modification, are permitted provided that the following
conditions are met: ... Neither the name of the copyright holder nor
the names of its contributors may be used to endorse or promote.`
	if got := classify(text); got != "BSD-3-Clause" {
		t.Errorf("classify BSD-3 = %q, want BSD-3-Clause", got)
	}
}

// TestClassifyDualLicense pins the refactor's headline behavior: a
// file carrying MIT and Apache text reports both, in pattern-table
// order - yaml.in ships exactly this shape.
func TestClassifyDualLicense(t *testing.T) {
	text := `This project is covered by two different licenses: MIT and Apache.

#### MIT License ####
Permission is hereby granted, free of charge, to any person

#### Apache License ####
Apache License
Version 2.0, January 2004`
	if got := classify(text); got != "Apache-2.0 / MIT" {
		t.Errorf("classify dual = %q, want %q", got, "Apache-2.0 / MIT")
	}
}

func TestClassifyUnknown(t *testing.T) {
	if got := classify("made up license text"); got != "unknown" {
		t.Errorf("classify unknown = %q, want unknown", got)
	}
}

func TestCachePathEscapesUppercase(t *testing.T) {
	if got := cachePath("github.com/Alice/Mod"); got != "github.com/!alice/!mod" {
		t.Errorf("cachePath = %q", got)
	}
	if got := cachePath("go.yaml.in/yaml/v3"); got != "go.yaml.in/yaml/v3" {
		t.Errorf("cachePath lowercase = %q", got)
	}
}

// TestResolveReadsCache builds a fake module cache with a LICENSE
// file and checks the full path resolution.
func TestResolveReadsCache(t *testing.T) {
	cache := t.TempDir()
	dir := filepath.Join(cache, "example.com", "foo", "v1.0.0@v1.0.0")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "LICENSE"), []byte("MIT License\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := resolve(cache, "example.com/foo/v1.0.0", "v1.0.0"); got != "MIT" {
		t.Errorf("resolve = %q, want MIT", got)
	}
}

func TestResolveDegradesToUnknown(t *testing.T) {
	cache := t.TempDir()
	if got := resolve(cache, "example.com/missing", "v1.0.0"); got != "unknown" {
		t.Errorf("missing module = %q, want unknown", got)
	}
	if got := resolve(cache, "example.com/noversion", ""); got != "unknown" {
		t.Errorf("empty version = %q, want unknown", got)
	}
}

// TestResolveAllSortsAndReports drives the whole pipeline with a
// synthetic build info and cache: sorted output, replaced modules
// resolved through their replacement, dual licenses reported.
func TestResolveAllSortsAndReports(t *testing.T) {
	cache := t.TempDir()
	dual := filepath.Join(cache, "example.com", "yaml@v3.1.0")
	if err := os.MkdirAll(dual, 0o755); err != nil {
		t.Fatal(err)
	}
	dualText := "MIT License\nPermission is hereby granted.\nApache License\nVersion 2.0"
	if err := os.WriteFile(filepath.Join(dual, "LICENSE"), []byte(dualText), 0o644); err != nil {
		t.Fatal(err)
	}

	info := &debug.BuildInfo{
		Deps: []*debug.Module{
			{Path: "example.com/yaml", Version: "v3.1.0"},
			{Path: "example.com/old", Version: "v0.1.0", Replace: &debug.Module{Path: "example.com/yaml", Version: "v3.1.0"}},
			{Path: "example.com/absent", Version: "v0.2.0"},
		},
	}
	entries := resolveAll(info, cache)

	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(entries))
	}
	if entries[0].Module != "example.com/absent" || entries[0].License != "unknown" {
		t.Errorf("first entry = %+v, want absent/unknown", entries[0])
	}
	if entries[1].Module != "example.com/yaml" || entries[1].License != "Apache-2.0 / MIT" {
		t.Errorf("replaced entry = %+v, want the replacement module with both licenses", entries[1])
	}
	if entries[2].License != "Apache-2.0 / MIT" {
		t.Errorf("dual entry = %+v, want both licenses", entries[2])
	}
}

func TestListSortsByModule(t *testing.T) {
	entries, err := List()
	if err != nil {
		t.Skipf("build info unavailable in this binary: %v", err)
	}
	// Test binaries carry build info but no dependency list; only a
	// real build of the command reports modules.
	if len(entries) == 0 {
		t.Skip("no dependency entries in this binary")
	}
	for i := 1; i < len(entries); i++ {
		if strings.Compare(entries[i-1].Module, entries[i].Module) > 0 {
			t.Fatalf("entries not sorted: %q > %q", entries[i-1].Module, entries[i].Module)
		}
	}
}
