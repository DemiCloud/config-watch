package config

import (
	"bytes"
	"fmt"
	"os"

	"github.com/pelletier/go-toml/v2"
)

// Load reads and validates the instance config file at path.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	dec := toml.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, err
	}

	if cfg.Path == "" {
		return nil, fmt.Errorf("path is required")
	}
	if cfg.CheckCmd == "" {
		return nil, fmt.Errorf("check_cmd is required")
	}
	if cfg.ReloadCmd == "" {
		return nil, fmt.Errorf("reload_cmd is required")
	}

	return &cfg, nil
}
