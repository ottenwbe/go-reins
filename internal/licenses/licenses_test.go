package licenses

import (
	"os"
	"path/filepath"
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

func TestClassifyUnknown(t *testing.T) {
	if got := classify("made up license text"); got != "" {
		t.Errorf("classify unknown = %q, want \"\"", got)
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
