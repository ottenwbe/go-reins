// Package config resolves runtime configuration with an explicit
// precedence chain: CLI flags > environment (GO_REINS_*) > config
// file (YAML) > built-in defaults. It replaces viper: the whole chain
// is visible in this one file.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	flag "github.com/spf13/pflag"
	"go.yaml.in/yaml/v3"

	"go-reins/internal/agent"
)

// EnvPrefix is prepended to every config key to form its environment
// variable name, e.g. backend -> GO_REINS_BACKEND.
const EnvPrefix = "GO_REINS"

// Config is the resolved runtime configuration for one invocation.
type Config struct {
	Backend  string // ollama or llamacpp
	URL      string // backend base URL; empty = per-backend default
	Model    string // model name, e.g. llama3.2
	Yes      bool   // auto-approve tool calls
	NoStream bool   // disable streamed replies in the interactive views
	History  bool   // dump the conversation after the answer
	Turns    bool   // report how many turns the run took
	LogLevel string // zap level: debug, info, warn, error
	MaxTurns int    // turn budget per Run/Step; at least 1

	// Tools selects which agent tools load, by name. Nil means all
	// available tools; an explicitly empty list loads none.
	Tools []string
	// ToolRisk overrides the risk tier of individual tools, by name:
	// "read-only" or "mutating". Keys for unknown tools are an error
	// at assembly time.
	ToolRisk map[string]string

	// ConfigFile is the path of the YAML file that was applied,
	// empty when none was. The loader records it instead of
	// printing, so the CLI layer decides what to do with the fact.
	ConfigFile string
}

// Defaults returns the configuration used when nothing else is set.
func Defaults() Config {
	return Config{
		Backend:  "ollama",
		Model:    "gemma4",
		LogLevel: "error",
		MaxTurns: agent.DefaultMaxTurns,
	}
}

// Load resolves the configuration and validates the result. The
// flag set supplies both the built-in defaults (registered at flag
// definition time) and the highest-priority overrides; a flag only
// wins when it was explicitly set (Flag.Changed), so a file or env
// value can supply what the user left at its default.
func Load(flags *flag.FlagSet) (Config, error) {
	c := Defaults()

	if err := applyFile(&c, flags); err != nil {
		return c, err
	}
	if err := applyEnv(&c); err != nil {
		return c, err
	}
	if err := applyFlags(&c, flags); err != nil {
		return c, err
	}
	if err := c.Validate(); err != nil {
		return c, err
	}
	return c, nil
}

// Validate reports whether the resolved configuration is internally
// consistent, so a malformed value fails at load time instead of
// deep inside a running command. It deliberately covers only what
// this package owns: backend and model vocabulary stay with session
// and the adapters, so adding a backend never touches this file.
func (c Config) Validate() error {
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("invalid log level %q: expected debug, info, warn, or error", c.LogLevel)
	}
	for tool, risk := range c.ToolRisk {
		if _, err := agent.ParseRisk(risk); err != nil {
			return fmt.Errorf("tool %q: %w", tool, err)
		}
	}
	if c.MaxTurns < 1 {
		return fmt.Errorf("invalid max-turns %d: must be at least 1", c.MaxTurns)
	}
	return nil
}

// envKey maps a config key to its environment variable name, e.g.
// log-level -> GO_REINS_LOG_LEVEL. One rule, one place.
func envKey(key string) string {
	return EnvPrefix + "_" + strings.ToUpper(strings.ReplaceAll(key, "-", "_"))
}

// fileConfig mirrors the YAML config file schema.
type fileConfig struct {
	Backend  string            `yaml:"backend"`
	URL      string            `yaml:"url"`
	Model    string            `yaml:"model"`
	Yes      bool              `yaml:"yes"`
	NoStream bool              `yaml:"no-stream"`
	History  bool              `yaml:"history"`
	Turns    bool              `yaml:"turns"`
	LogLevel string            `yaml:"log-level"`
	MaxTurns *int              `yaml:"max-turns"`
	Tools    []string          `yaml:"tools"`
	ToolRisk map[string]string `yaml:"tool-risk"`
}

// applyFile loads the YAML config file, if any, into c. An explicit
// --config path must exist and parse; the default search (home, then
// the working directory) may find nothing without complaint.
func applyFile(c *Config, flags *flag.FlagSet) error {
	if fl := flags.Lookup("config"); fl != nil && fl.Changed {
		return readInto(c, fl.Value.String())
	}
	for _, dir := range candidateDirs() {
		path := filepath.Join(dir, ".go-reins.yaml")
		if _, err := os.Stat(path); err == nil {
			return readInto(c, path)
		}
	}
	return nil
}

// candidateDirs lists the directories searched for the default config
// file, in order. An unreadable home directory just means "no config
// file", not an error.
func candidateDirs() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return []string{"."}
	}
	return []string{home, "."}
}

func readInto(c *Config, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config file: %w", err)
	}
	var fc fileConfig
	if err := yaml.Unmarshal(data, &fc); err != nil {
		return fmt.Errorf("parse config file %s: %w", path, err)
	}
	if fc.Backend != "" {
		c.Backend = fc.Backend
	}
	if fc.URL != "" {
		c.URL = fc.URL
	}
	if fc.Model != "" {
		c.Model = fc.Model
	}
	if fc.LogLevel != "" {
		c.LogLevel = fc.LogLevel
	}
	// A pointer keeps an explicit "max-turns: 0" distinct from an
	// absent key: zero must fail validation, not silently fall back
	// to the default.
	if fc.MaxTurns != nil {
		c.MaxTurns = *fc.MaxTurns
	}
	// Booleans have no "unset" distinct from false, so a file entry
	// is taken as an explicit choice.
	c.Yes = fc.Yes
	c.NoStream = fc.NoStream
	c.History = fc.History
	c.Turns = fc.Turns
	// Slices and maps are nil when absent, which keeps "not set"
	// distinct from "set to empty".
	if fc.Tools != nil {
		c.Tools = fc.Tools
	}
	if fc.ToolRisk != nil {
		c.ToolRisk = fc.ToolRisk
	}

	// Record, don't print: the CLI layer decides what a used config
	// file is worth telling the user.
	c.ConfigFile = path
	return nil
}

// applyEnv overrides file values with GO_REINS_* variables. Empty
// variables are ignored; boolean variables must parse.
func applyEnv(c *Config) error {
	str := func(key string) string {
		if v, ok := os.LookupEnv(envKey(key)); ok {
			return v
		}
		return ""
	}

	if v := str("backend"); v != "" {
		c.Backend = v
	}
	if v := str("url"); v != "" {
		c.URL = v
	}
	if v := str("model"); v != "" {
		c.Model = v
	}
	if v := str("log-level"); v != "" {
		c.LogLevel = v
	}
	if v := str("max-turns"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("env %s: %w", envKey("max-turns"), err)
		}
		c.MaxTurns = n
	}
	boolean := func(key string, target *bool) error {
		v := str(key)
		if v == "" {
			return nil
		}
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("env %s: %w", envKey(key), err)
		}
		*target = b
		return nil
	}
	for key, target := range map[string]*bool{
		"yes":       &c.Yes,
		"no-stream": &c.NoStream,
		"history":   &c.History,
		"turns":     &c.Turns,
	} {
		if err := boolean(key, target); err != nil {
			return err
		}
	}
	if v := str("tools"); v != "" {
		c.Tools = splitList(v)
	}
	if v := str("tool-risk"); v != "" {
		risk, err := parseKeyValues(v)
		if err != nil {
			return fmt.Errorf("env %s: %w", envKey("tool-risk"), err)
		}
		c.ToolRisk = risk
	}
	return nil
}

// splitList turns "a, b ,c" into ["a","b","c"]; empty entries are
// dropped.
func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// parseKeyValues turns "shell=read-only,read=mutating" into a map.
// Both sides of each pair must be non-empty - a bare key, a bare
// value, or junk like "=x" fails loudly instead of quietly becoming
// a map entry.
func parseKeyValues(s string) (map[string]string, error) {
	out := make(map[string]string)
	for _, pair := range splitList(s) {
		k, v, ok := strings.Cut(pair, "=")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if !ok || k == "" || v == "" {
			return nil, fmt.Errorf("expected name=value, got %q", pair)
		}
		out[k] = v
	}
	return out, nil
}

// applyFlags overlays explicitly set flags; they outrank everything.
func applyFlags(c *Config, flags *flag.FlagSet) error {
	if fl := flags.Lookup("backend"); fl != nil && fl.Changed {
		c.Backend = fl.Value.String()
	}
	if fl := flags.Lookup("url"); fl != nil && fl.Changed {
		c.URL = fl.Value.String()
	}
	if fl := flags.Lookup("model"); fl != nil && fl.Changed {
		c.Model = fl.Value.String()
	}
	if fl := flags.Lookup("log-level"); fl != nil && fl.Changed {
		c.LogLevel = fl.Value.String()
	}
	// Ints: pflag already rejects non-numeric values at parse time;
	// parsing here is defense in depth, same as the bool flags.
	if fl := flags.Lookup("max-turns"); fl != nil && fl.Changed {
		n, err := strconv.Atoi(fl.Value.String())
		if err != nil {
			return fmt.Errorf("flag --max-turns: %w", err)
		}
		c.MaxTurns = n
	}
	// Bools: pflag already rejects invalid values at parse time, so
	// these errors are defense in depth - but they propagate anyway,
	// because silently coercing a parse failure to false is worse
	// than failing.
	boolFlag := func(name string, target *bool) error {
		fl := flags.Lookup(name)
		if fl == nil || !fl.Changed {
			return nil
		}
		b, err := strconv.ParseBool(fl.Value.String())
		if err != nil {
			return fmt.Errorf("flag --%s: %w", name, err)
		}
		*target = b
		return nil
	}
	for name, target := range map[string]*bool{
		"yes":       &c.Yes,
		"no-stream": &c.NoStream,
		"history":   &c.History,
		"turns":     &c.Turns,
	} {
		if err := boolFlag(name, target); err != nil {
			return err
		}
	}
	if fl := flags.Lookup("tools"); fl != nil && fl.Changed {
		c.Tools = splitList(fl.Value.String())
	}
	if fl := flags.Lookup("tool-risk"); fl != nil && fl.Changed {
		risk, err := parseKeyValues(fl.Value.String())
		if err != nil {
			return fmt.Errorf("flag --tool-risk: %w", err)
		}
		c.ToolRisk = risk
	}
	return nil
}
