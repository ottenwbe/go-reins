package licenses

import (
	"regexp"
	"strings"
)

// patterns pairs signature text (matched case-insensitively) with
// SPDX identifiers. Order matters twice over: signatures contained
// in longer licenses come first (AGPL before GPL, BSD-3 before
// BSD-2), and the reported list follows this order.
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

// subsumed maps a license id to the id of a longer license whose
// text contains its signature, so the narrower id is not also
// reported when the longer one matched: every BSD-3-Clause file
// matches the BSD-2-Clause signature too, but reporting both would
// be noise.
var subsumed = map[string]string{
	"BSD-2-Clause": "BSD-3-Clause",
}

// classifyAll returns every known license whose signature matches
// the text, in pattern-table order. Dual-licensed files (MIT and
// Apache in one LICENSE, as yaml.in ships) report both.
func classifyAll(text string) []string {
	var found []string
	for _, p := range patterns {
		if !p.re.MatchString(text) {
			continue
		}
		if s, ok := subsumed[p.id]; ok && containsID(found, s) {
			continue
		}
		found = append(found, p.id)
	}
	return found
}

// classify maps license text to its SPDX identifier(s) joined with
// " / ", or "unknown" when no signature matches.
func classify(text string) string {
	all := classifyAll(text)
	if len(all) == 0 {
		return "unknown"
	}
	return strings.Join(all, " / ")
}

func containsID(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}
