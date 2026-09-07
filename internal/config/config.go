// Package config loads a single watch instance's TOML configuration file —
// the per-instance settings that used to live in an EnvironmentFile.
package config

// Config holds one watch instance's settings, as loaded from
// <config-dir>/<instance>.toml.
type Config struct {
	Path      string `toml:"path"`
	CheckCmd  string `toml:"check_cmd"`
	ReloadCmd string `toml:"reload_cmd"`
}
