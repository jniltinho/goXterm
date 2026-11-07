// Package store reads the existing rustXterm store in place, without changing its formats:
// the SQLCipher database (rustxterm.db) unlocked through the keys.v1 envelope, and the TOML
// settings. It mirrors the Rust crates rxt-store and rxt-platform (migrate-gui-to-go-wails phase 2).
package store

import (
	"os"
	"path/filepath"
	"runtime"
)

// appDir is the per-app subdirectory under the XDG config/data homes (Rust APP_DIR).
const appDir = "rustxterm"

// ConfigDir is $XDG_CONFIG_HOME/rustxterm or ~/.config/rustxterm on Linux, the platform config dir
// elsewhere (Rust dirs::config_dir(): ~/Library/Application Support on macOS, %APPDATA% on Windows).
func ConfigDir() string { return filepath.Join(platformDir("XDG_CONFIG_HOME", ".config"), appDir) }

// DataDir is $XDG_DATA_HOME/rustxterm or ~/.local/share/rustxterm on Linux (Rust dirs::data_dir(),
// which is the same folder as the config dir on macOS and Windows).
func DataDir() string { return filepath.Join(platformDir("XDG_DATA_HOME", ".local/share"), appDir) }

func platformDir(xdgEnv, xdgFallback string) string {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		if d, err := os.UserConfigDir(); err == nil {
			return d
		}
	}
	return xdgHome(xdgEnv, xdgFallback)
}

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
