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
	History  bool   // dump the conversation after the answer
	Turns    bool   // report how many turns the run took
	LogLevel string // zap level: debug, info, warn, error
}

// Defaults returns the configuration used when nothing else is set.
func Defaults() Config {
	return Config{
		Backend:  "ollama",
		LogLevel: "error",
	}
}

// Load resolves the configuration. The flag set supplies both the
// built-in defaults (registered at flag definition time) and the
// highest-priority overrides; a flag only wins when it was explicitly
// set (Flag.Changed), so a file or env value can supply what the user
// left at its default.
func Load(flags *flag.FlagSet) (Config, error) {
	c := Defaults()

	if err := applyFile(&c, flags); err != nil {
		return c, err
	}
	if err := applyEnv(&c); err != nil {
		return c, err
	}
	applyFlags(&c, flags)
	return c, nil
}

// fileConfig mirrors the YAML config file schema.
type fileConfig struct {
	Backend  string `yaml:"backend"`
	URL      string `yaml:"url"`
	Model    string `yaml:"model"`
	Yes      bool   `yaml:"yes"`
	History  bool   `yaml:"history"`
	Turns    bool   `yaml:"turns"`
	LogLevel string `yaml:"log-level"`
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
	// Booleans have no "unset" distinct from false, so a file entry
	// is taken as an explicit choice.
	c.Yes = fc.Yes
	c.History = fc.History
	c.Turns = fc.Turns

	fmt.Fprintln(os.Stderr, "using config file:", path)
	return nil
}

// applyEnv overrides file values with GO_REINS_* variables. Empty
// variables are ignored; boolean variables must parse.
func applyEnv(c *Config) error {
	str := func(key string) string {
		if v, ok := os.LookupEnv(EnvPrefix + "_" + strings.ToUpper(strings.ReplaceAll(key, "-", "_"))); ok {
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
	boolean := func(key string, target *bool) error {
		v := str(key)
		if v == "" {
			return nil
		}
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("env %s_%s: %w", EnvPrefix, strings.ToUpper(key), err)
		}
		*target = b
		return nil
	}
	for key, target := range map[string]*bool{
		"yes":     &c.Yes,
		"history": &c.History,
		"turns":   &c.Turns,
	} {
		if err := boolean(key, target); err != nil {
			return err
		}
	}
	return nil
}

// applyFlags overlays explicitly set flags; they outrank everything.
func applyFlags(c *Config, flags *flag.FlagSet) {
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
	if fl := flags.Lookup("yes"); fl != nil && fl.Changed {
		c.Yes, _ = strconv.ParseBool(fl.Value.String())
	}
	if fl := flags.Lookup("history"); fl != nil && fl.Changed {
		c.History, _ = strconv.ParseBool(fl.Value.String())
	}
	if fl := flags.Lookup("turns"); fl != nil && fl.Changed {
		c.Turns, _ = strconv.ParseBool(fl.Value.String())
	}
}
