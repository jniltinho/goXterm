// Package store reads the existing rustXterm store in place, without changing its formats:
// the SQLCipher database (rustxterm.db) unlocked through the keys.v1 envelope, and the TOML
// settings. It mirrors the Rust crates rxt-store and rxt-platform (migrate-gui-to-go-wails phase 2).
package store

import (
	"os"
	"path/filepath"
)

// appDir is the per-app subdirectory under the XDG config/data homes (Rust APP_DIR).
const appDir = "rustxterm"

// ConfigDir is $XDG_CONFIG_HOME/rustxterm or ~/.config/rustxterm (Rust dirs::config_dir()).
func ConfigDir() string { return filepath.Join(xdgHome("XDG_CONFIG_HOME", ".config"), appDir) }

// DataDir is $XDG_DATA_HOME/rustxterm or ~/.local/share/rustxterm (Rust dirs::data_dir()).
func DataDir() string { return filepath.Join(xdgHome("XDG_DATA_HOME", ".local/share"), appDir) }

func xdgHome(env, fallback string) string {
	if v := os.Getenv(env); v != "" && filepath.IsAbs(v) {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	return filepath.Join(home, fallback)
}
