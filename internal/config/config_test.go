package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	flag "github.com/spf13/pflag"

	"go-reins/internal/agent"
)

// newFlags builds a flag set with the same names and defaults as the
// root command registers.
func newFlags(t *testing.T) *flag.FlagSet {
	t.Helper()
	f := flag.NewFlagSet("go-reins", flag.ContinueOnError)
	f.String("backend", "ollama", "")
	f.String("url", "", "")
	f.String("model", "gemma4", "")
	f.Bool("yes", false, "")
	f.Bool("history", false, "")
	f.Bool("turns", false, "")
	f.String("log-level", "error", "")
	f.String("tools", "", "")
	f.String("tool-risk", "", "")
	f.Int("max-turns", agent.DefaultMaxTurns, "")
	f.String("config", "", "")
	return f
}

// isolate gives the test a fake HOME without a config file and no
// GO_REINS_* variables (empty values are ignored by applyEnv, same
// as unset).
func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	for _, key := range []string{"BACKEND", "URL", "MODEL", "YES", "HISTORY", "TURNS", "LOG_LEVEL", "MAX_TURNS"} {
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
	if !reflect.DeepEqual(c, Defaults()) {
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

func TestToolsFromFlag(t *testing.T) {
	isolate(t)
	flags := newFlags(t)
	if err := flags.Parse([]string{"--tools", " read , shell "}); err != nil {
		t.Fatal(err)
	}
	c, err := Load(flags)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Tools, []string{"read", "shell"}) {
		t.Errorf("Tools = %v, want [read shell]", c.Tools)
	}
}

func TestToolsFromEnv(t *testing.T) {
	isolate(t)
	t.Setenv(EnvPrefix+"_TOOLS", "read")
	c, err := Load(newFlags(t))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Tools, []string{"read"}) {
		t.Errorf("Tools = %v, want [read]", c.Tools)
	}
}

func TestToolsFromFile(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "conf.yaml")
	if err := os.WriteFile(path, []byte("tools: [read, shell]\ntool-risk:\n  shell: read-only\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	flags := newFlags(t)
	if err := flags.Parse([]string{"--config", path}); err != nil {
		t.Fatal(err)
	}
	c, err := Load(flags)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Tools, []string{"read", "shell"}) {
		t.Errorf("Tools = %v", c.Tools)
	}
	if !reflect.DeepEqual(c.ToolRisk, map[string]string{"shell": "read-only"}) {
		t.Errorf("ToolRisk = %v", c.ToolRisk)
	}
}

func TestToolsFlagOverridesEnv(t *testing.T) {
	isolate(t)
	t.Setenv(EnvPrefix+"_TOOLS", "read,shell")
	flags := newFlags(t)
	if err := flags.Parse([]string{"--tools", "shell"}); err != nil {
		t.Fatal(err)
	}
	c, err := Load(flags)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Tools, []string{"shell"}) {
		t.Errorf("Tools = %v, want flag to win", c.Tools)
	}
}

func TestToolRiskFlagRejectsGarbage(t *testing.T) {
	isolate(t)
	flags := newFlags(t)
	if err := flags.Parse([]string{"--tool-risk", "shell"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(flags); err == nil {
		t.Error("want error for pair without =")
	}
}

func TestToolRiskEnvRejectsGarbage(t *testing.T) {
	isolate(t)
	t.Setenv(EnvPrefix+"_TOOL_RISK", "nonsense")
	if _, err := Load(newFlags(t)); err == nil {
		t.Error("want error for unparseable risk env var")
	}
}

// TestMaxTurnsPrecedence walks the whole chain for one key: a file
// value applies when nothing else is set, env outranks the file,
// and an explicitly set flag outranks both.
func TestMaxTurnsPrecedence(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	writeFile(t, dir, ".go-reins.yaml", "max-turns: 3\n")
	t.Setenv("HOME", dir)
	t.Setenv(EnvPrefix+"_MAX_TURNS", "5")

	flags := newFlags(t)
	if err := flags.Parse([]string{"--max-turns", "7"}); err != nil {
		t.Fatal(err)
	}
	c, err := Load(flags)
	if err != nil {
		t.Fatal(err)
	}
	if c.MaxTurns != 7 {
		t.Errorf("MaxTurns = %d, want 7 (flag must win)", c.MaxTurns)
	}

	flags = newFlags(t)
	if err := flags.Parse(nil); err != nil {
		t.Fatal(err)
	}
	c, err = Load(flags)
	if err != nil {
		t.Fatal(err)
	}
	if c.MaxTurns != 5 {
		t.Errorf("MaxTurns = %d, want 5 (env must outrank file)", c.MaxTurns)
	}

	t.Setenv(EnvPrefix+"_MAX_TURNS", "")
	flags = newFlags(t)
	if err := flags.Parse(nil); err != nil {
		t.Fatal(err)
	}
	c, err = Load(flags)
	if err != nil {
		t.Fatal(err)
	}
	if c.MaxTurns != 3 {
		t.Errorf("MaxTurns = %d, want 3 (file applies when flag and env are unset)", c.MaxTurns)
	}
}

// TestMaxTurnsRejectsInvalid fails at load, from every source: an
// unparseable env var, a non-positive value from env, flag, or file.
// A budget of 0 or less silently changing the default would be worse.
func TestMaxTurnsRejectsInvalid(t *testing.T) {
	t.Run("env garbage", func(t *testing.T) {
		isolate(t)
		t.Setenv(EnvPrefix+"_MAX_TURNS", "many")
		if _, err := Load(newFlags(t)); err == nil {
			t.Error("want error for unparseable max-turns env var")
		}
	})
	t.Run("env zero", func(t *testing.T) {
		isolate(t)
		t.Setenv(EnvPrefix+"_MAX_TURNS", "0")
		if _, err := Load(newFlags(t)); err == nil {
			t.Error("want error for max-turns=0 from env")
		}
	})
	t.Run("flag negative", func(t *testing.T) {
		isolate(t)
		flags := newFlags(t)
		if err := flags.Parse([]string{"--max-turns", "-1"}); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(flags); err == nil {
			t.Error("want error for negative max-turns flag")
		}
	})
	t.Run("file zero", func(t *testing.T) {
		isolate(t)
		dir := t.TempDir()
		writeFile(t, dir, ".go-reins.yaml", "max-turns: 0\n")
		t.Setenv("HOME", dir)
		if _, err := Load(newFlags(t)); err == nil {
			t.Error("want error for max-turns: 0 in config file")
		}
	})
}

// TestDefaultModel pins the out-of-the-box model: changing it is a
// product decision, not an accident.
func TestDefaultModel(t *testing.T) {
	if got := Defaults().Model; got != "gemma4" {
		t.Errorf("default model = %q, want gemma4", got)
	}
}

// TestValidate pins what the config package itself owns: log level
// and tool-risk vocabulary. Backend and model names stay with
// session, so adding a backend never widens this test.
func TestValidate(t *testing.T) {
	if err := Defaults().Validate(); err != nil {
		t.Errorf("defaults must validate: %v", err)
	}

	bad := map[string]Config{
		"log level":    {LogLevel: "chatty"},
		"tool risk":    {ToolRisk: map[string]string{"shell": "spicy"}},
		"risk per env": {LogLevel: "info", ToolRisk: map[string]string{"read": "mutating", "shell": "nope"}},
		"max turns":    {LogLevel: "info", MaxTurns: 0},
	}
	for name, cfg := range bad {
		if err := cfg.Validate(); err == nil {
			t.Errorf("Validate(%s) = nil, want error", name)
		}
	}
}

func TestLoadValidatesLogLevel(t *testing.T) {
	isolate(t)
	t.Setenv(EnvPrefix+"_LOG_LEVEL", "loud")
	if _, err := Load(newFlags(t)); err == nil {
		t.Error("want load error for invalid log level from env")
	}
}

func TestParseKeyValuesRejectsEmptySides(t *testing.T) {
	// Shape violations fail here. Content violations ("shell=="
	// yields the value "=", which is not a risk tier) are Validate's
	// job, not the parser's.
	for _, in := range []string{"=mutating", "shell="} {
		if _, err := parseKeyValues(in); err == nil {
			t.Errorf("parseKeyValues(%q) = nil error, want rejection", in)
		}
	}
	if m, err := parseKeyValues("shell=="); err != nil || m["shell"] != "=" {
		t.Errorf("parseKeyValues(shell==) = %v, %v; want value %q to pass through for validation", m, err, "=")
	}
}

func TestConfigFileRecordedNotPrinted(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "conf.yaml")
	if err := os.WriteFile(path, []byte("model: llama3.2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	flags := newFlags(t)
	if err := flags.Parse([]string{"--config", path}); err != nil {
		t.Fatal(err)
	}
	c, err := Load(flags)
	if err != nil {
		t.Fatal(err)
	}
	if c.ConfigFile != path {
		t.Errorf("ConfigFile = %q, want %q", c.ConfigFile, path)
	}
}
