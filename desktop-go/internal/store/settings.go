package store

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

const settingsFile = "config.toml"

// Settings mirrors the Rust config.toml (rxt-store settings). Unknown keys are ignored; secrets are
// never stored here. Only the fields the Go app needs so far are typed; the rest load and are kept
// by round-tripping the raw table when settings are written (phase 2 write path, task 2.1).
type Settings struct {
	Theme                    string        `toml:"theme"`
	LogLevel                 string        `toml:"log_level"`
	AgentControl             bool          `toml:"agent_control"`
	CopyOnSelect             bool          `toml:"copy_on_select"`
	RightClickPaste          bool          `toml:"right_click_paste"`
	PasteGuard               bool          `toml:"paste_guard"`
	ConfirmClose             bool          `toml:"confirm_close"`
	SidebarOpen              bool          `toml:"sidebar_open"`
	SidebarWidth             float64       `toml:"sidebar_width"`
	SidebarPage              string        `toml:"sidebar_page"`
	UIScale                  string        `toml:"ui_scale"`
	RemoteMonitoring         bool          `toml:"remote_monitoring"`
	RemoteMonitoringInterval int           `toml:"remote_monitoring_interval_secs"`
	Notifications            bool          `toml:"notifications"`
	GPUBackend               string        `toml:"gpu_backend"`
	FirstRunThemeDone        bool          `toml:"first_run_theme_done"`
	Terminal                 TerminalPrefs `toml:"terminal"`
}

// TerminalPrefs mirrors the [terminal] table.
type TerminalPrefs struct {
	FontFamily      string  `toml:"font_family"`
	FontSize        float64 `toml:"font_size"`
	CursorShape     string  `toml:"cursor_shape"`
	CursorBlink     bool    `toml:"cursor_blink"`
	Scheme          string  `toml:"scheme"`
	ScrollbackLines int     `toml:"scrollback_lines"`
	LogRaw          bool    `toml:"log_raw"`
}

// LoadSettings reads config.toml from the config dir. A missing file is not an error (defaults).
func LoadSettings() (Settings, error) {
	return loadSettingsFrom(filepath.Join(ConfigDir(), settingsFile))
}

func loadSettingsFrom(path string) (Settings, error) {
	var s Settings
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	_, err = toml.Decode(string(raw), &s)
	return s, err
}
