package cmd

import (
	"runtime/debug"
	"testing"
)

func TestVcsSettings(t *testing.T) {
	info := &debug.BuildInfo{
		Main: debug.Module{Path: "go-reins", Version: "(devel)"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "bf45b49b625476667c54b880051cb87dcbbe7d7f"},
			{Key: "vcs.time", Value: "2026-10-07T20:15:00Z"},
			{Key: "vcs.modified", Value: "true"},
		},
	}
	rev, built, modified := vcsSettings(info)
	if rev != "bf45b49b625476667c54b880051cb87dcbbe7d7f" {
		t.Errorf("revision = %q", rev)
	}
	if built != "2026-10-07T20:15:00Z" {
		t.Errorf("built = %q", built)
	}
	if modified != "true" {
		t.Errorf("modified = %q", modified)
	}
}

func TestVcsSettingsAbsent(t *testing.T) {
	rev, built, modified := vcsSettings(&debug.BuildInfo{})
	if rev != "" || built != "" || modified != "" {
		t.Errorf("want all empty, got %q %q %q", rev, built, modified)
	}
}

func TestCommitLabel(t *testing.T) {
	cases := []struct {
		rev, modified, want string
	}{
		{"bf45b49b625476667c54b880051cb87dcbbe7d7f", "true", "bf45b49b6254 (dirty)"},
		{"bf45b49b625476667c54b880051cb87dcbbe7d7f", "false", "bf45b49b6254"},
		{"", "false", ""},
	}
	for _, c := range cases {
		if got := commitLabel(c.rev, c.modified); got != c.want {
			t.Errorf("commitLabel(%q, %q) = %q, want %q", c.rev, c.modified, got, c.want)
		}
	}
}
