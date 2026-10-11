package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestFromBuildInfoMapsFields(t *testing.T) {
	info := &debug.BuildInfo{
		Main:      debug.Module{Path: "go-reins", Version: "(devel)"},
		GoVersion: "go1.27.1",
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "bf45b49b625476667c54b880051cb87dcbbe7d7f"},
			{Key: "vcs.time", Value: "2026-10-07T20:15:00Z"},
			{Key: "vcs.modified", Value: "true"},
		},
	}
	bi := FromBuildInfo("v1.0.0", info)
	if bi.Version != "v1.0.0" || bi.Module != "go-reins" || bi.ModuleVersion != "(devel)" {
		t.Errorf("identity fields = %+v", bi)
	}
	if bi.Commit != "bf45b49b6254 (dirty)" {
		t.Errorf("Commit = %q", bi.Commit)
	}
	if bi.Built != "2026-10-07T20:15:00Z" {
		t.Errorf("Built = %q", bi.Built)
	}
	if bi.Go != "go1.27.1" {
		t.Errorf("Go = %q", bi.Go)
	}
	if bi.Platform == "" || !bi.Available {
		t.Errorf("Platform/Available = %q/%v", bi.Platform, bi.Available)
	}
}

func TestFromBuildInfoWithoutVcs(t *testing.T) {
	bi := FromBuildInfo("dev", &debug.BuildInfo{Main: debug.Module{Path: "go-reins"}})
	if bi.Commit != "" || bi.Built != "" {
		t.Errorf("want empty vcs fields, got %+v", bi)
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

func TestCollectRuns(t *testing.T) {
	// Test binaries carry build info, so Collect must return an
	// available Info with the version passed through.
	bi := Collect("test")
	if !bi.Available {
		t.Fatalf("Collect: build info unavailable in test binary: %+v", bi)
	}
	if bi.Version != "test" {
		t.Errorf("Version = %q, want pass-through", bi.Version)
	}
}
