// Package config loads and validates Jalon's configuration file.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"
)

// Config is the full Jalon configuration.
type Config struct {
	// Context tunes the compaction advisor and the context report.
	Context Context `yaml:"context"`
}

// Default returns the configuration used when no config file exists.
func Default() Config {
	return Config{Context: defaultContext()}
}

// DefaultPath returns ~/.jalon/config.yaml, or config.yaml in the working
// directory when the home directory cannot be resolved.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "config.yaml"
	}
	return filepath.Join(home, ".jalon", "config.yaml")
}

// Load reads the config file at path, applies defaults to unset fields and
// validates the result. A missing file yields an error wrapping fs.ErrNotExist.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	return Parse(path, data)
}

// Parse is Load on a file's content: path only names the file in the errors.
// Empty content yields the defaults.
func Parse(path string, data []byte) (Config, error) {
	cfg := Default()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return cfg, nil
}

// Validate reports the first inconsistency found in the configuration.
func (c Config) Validate() error {
	return c.Context.validate()
}
