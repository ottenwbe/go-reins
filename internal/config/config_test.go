package config

import (
	"os"
	"path/filepath"
	"testing"

	flag "github.com/spf13/pflag"
)

// newFlags builds a flag set with the same names and defaults as the
// root command registers.
func newFlags(t *testing.T) *flag.FlagSet {
	t.Helper()
	f := flag.NewFlagSet("go-reins", flag.ContinueOnError)
	f.String("backend", "ollama", "")
	f.String("url", "", "")
	f.String("model", "", "")
	f.Bool("yes", false, "")
	f.Bool("history", false, "")
	f.Bool("turns", false, "")
	f.String("log-level", "error", "")
	f.String("config", "", "")
	return f
}

// isolate gives the test a fake HOME without a config file and no
// GO_REINS_* variables (empty values are ignored by applyEnv, same
// as unset).
func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	for _, key := range []string{"BACKEND", "URL", "MODEL", "YES", "HISTORY", "TURNS", "LOG_LEVEL"} {
		t.Setenv(EnvPrefix+"_"+key, "")
	}
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadDefaults(t *testing.T) {
	isolate(t)
	c, err := Load(newFlags(t))
	if err != nil {
		t.Fatal(err)
	}
	if c != Defaults() {
		t.Errorf("want defaults, got %+v", c)
	}
}

func TestLoadFileOverridesDefaults(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	writeFile(t, dir, ".go-reins.yaml", "backend: llamacpp\nmodel: qwen2.5\nlog-level: debug\nyes: true\n")
	// Point HOME at the dir containing the file.
	t.Setenv("HOME", dir)
	c, err := Load(newFlags(t))
	if err != nil {
		t.Fatal(err)
	}
	if c.Backend != "llamacpp" || c.Model != "qwen2.5" || c.LogLevel != "debug" || !c.Yes {
		t.Errorf("file values not applied: %+v", c)
	}
}

func TestLoadEnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	isolate(t)
	writeFile(t, dir, ".go-reins.yaml", "backend: llamacpp\nmodel: qwen2.5\n")
	t.Setenv("HOME", dir)
	t.Setenv(EnvPrefix+"_MODEL", "llama3.2")

	c, err := Load(newFlags(t))
	if err != nil {
		t.Fatal(err)
	}
	if c.Backend != "llamacpp" {
		t.Errorf("file value lost: %+v", c)
	}
	if c.Model != "llama3.2" {
		t.Errorf("env did not override file: %+v", c)
	}
}

func TestLoadFlagOverridesEnv(t *testing.T) {
	isolate(t)
	t.Setenv(EnvPrefix+"_MODEL", "llama3.2")
	flags := newFlags(t)
	if err := flags.Parse([]string{"--model", "qwen2.5", "--yes"}); err != nil {
		t.Fatal(err)
	}

	c, err := Load(flags)
	if err != nil {
		t.Fatal(err)
	}
	if c.Model != "qwen2.5" {
		t.Errorf("flag did not override env: %+v", c)
	}
	if !c.Yes {
		t.Errorf("--yes not applied: %+v", c)
	}
}

func TestLoadEnvBooleanRejectsGarbage(t *testing.T) {
	isolate(t)
	t.Setenv(EnvPrefix+"_YES", "garbage")
	if _, err := Load(newFlags(t)); err == nil {
		t.Error("want error for unparseable boolean env var")
	}
}

func TestLoadExplicitConfigFileMustExist(t *testing.T) {
	isolate(t)
	flags := newFlags(t)
	if err := flags.Parse([]string{"--config", filepath.Join(t.TempDir(), "missing.yaml")}); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(flags); err == nil {
		t.Error("want error for missing explicit config file")
	}
}
