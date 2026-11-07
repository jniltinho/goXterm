package store

import (
	"bytes"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

const settingsFile = "config.toml"

// Settings mirrors the Rust config.toml (rxt-store settings). Unknown keys are ignored; secrets are
// never stored here. Only the fields the Go app needs so far are typed; the rest load and are kept
// by round-tripping the raw table when settings are written (phase 2 write path, task 2.1).
type Settings struct {
	Theme                    string  `toml:"theme" json:"theme"`
	LogLevel                 string  `toml:"log_level" json:"logLevel"`
	AgentControl             bool    `toml:"agent_control" json:"agentControl"`
	CopyOnSelect             bool    `toml:"copy_on_select" json:"copyOnSelect"`
	RightClickPaste          bool    `toml:"right_click_paste" json:"rightClickPaste"`
	PasteGuard               bool    `toml:"paste_guard" json:"pasteGuard"`
	ConfirmClose             bool    `toml:"confirm_close" json:"confirmClose"`
	SidebarOpen              bool    `toml:"sidebar_open" json:"sidebarOpen"`
	SidebarWidth             float64 `toml:"sidebar_width" json:"sidebarWidth"`
	SidebarPage              string  `toml:"sidebar_page" json:"sidebarPage"`
	UIScale                  string  `toml:"ui_scale" json:"uiScale"`
	RemoteMonitoring         bool    `toml:"remote_monitoring" json:"remoteMonitoring"`
	RemoteMonitoringInterval int     `toml:"remote_monitoring_interval_secs" json:"remoteMonitoringInterval"`
	Notifications            bool    `toml:"notifications" json:"notifications"`
	GPUBackend               string  `toml:"gpu_backend" json:"gpuBackend"`
	FirstRunThemeDone        bool    `toml:"first_run_theme_done" json:"firstRunThemeDone"`
	// Toolbar/tabs appearance (nil = default on); shown under Settings → Display.
	ToolbarCaptions  *bool         `toml:"toolbar_captions" json:"toolbarCaptions"`
	ShowTabNumbers   *bool         `toml:"show_tab_numbers" json:"showTabNumbers"`
	ShowCloseButtons *bool         `toml:"show_close_buttons" json:"showCloseButtons"`
	Terminal         TerminalPrefs `toml:"terminal" json:"terminal"`
}

// TerminalPrefs mirrors the [terminal] table.
type TerminalPrefs struct {
	FontFamily      string  `toml:"font_family" json:"fontFamily"`
	FontSize        float64 `toml:"font_size" json:"fontSize"`
	CursorShape     string  `toml:"cursor_shape" json:"cursorShape"`
	CursorBlink     bool    `toml:"cursor_blink" json:"cursorBlink"`
	Scheme          string  `toml:"scheme" json:"scheme"`
	ScrollbackLines int     `toml:"scrollback_lines" json:"scrollbackLines"`
	LogRaw          bool    `toml:"log_raw" json:"logRaw"`
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

// SaveSettings writes config.toml, preserving any keys the Go app does not type (the Rust app's other
// settings are round-tripped through the raw table). config.toml is plain TOML, not encrypted.
func SaveSettings(s Settings) error {
	path := filepath.Join(ConfigDir(), settingsFile)
	m := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil {
		_, _ = toml.Decode(string(raw), &m)
	}
	m["theme"] = s.Theme
	m["agent_control"] = s.AgentControl
	m["copy_on_select"] = s.CopyOnSelect
	m["right_click_paste"] = s.RightClickPaste
	m["paste_guard"] = s.PasteGuard
	m["confirm_close"] = s.ConfirmClose
	m["remote_monitoring"] = s.RemoteMonitoring
	m["remote_monitoring_interval_secs"] = s.RemoteMonitoringInterval
	m["notifications"] = s.Notifications
	m["toolbar_captions"] = derefOr(s.ToolbarCaptions, true)
	m["show_tab_numbers"] = derefOr(s.ShowTabNumbers, true)
	m["show_close_buttons"] = derefOr(s.ShowCloseButtons, true)
	term, _ := m["terminal"].(map[string]any)
	if term == nil {
		term = map[string]any{}
	}
	term["font_family"] = s.Terminal.FontFamily
	term["font_size"] = s.Terminal.FontSize
	term["cursor_shape"] = s.Terminal.CursorShape
	term["cursor_blink"] = s.Terminal.CursorBlink
	term["scheme"] = s.Terminal.Scheme
	term["scrollback_lines"] = s.Terminal.ScrollbackLines
	m["terminal"] = term

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(m); err != nil {
		return err
	}
	if err := os.MkdirAll(ConfigDir(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// derefOr returns *p, or def when p is nil (for settings that default on).
func derefOr(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}
