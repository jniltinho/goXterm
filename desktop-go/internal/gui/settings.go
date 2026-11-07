package gui

import "rustxterm-go/internal/store"

// GetSettings returns the global settings (config.toml) with sensible defaults filled for a fresh
// config, so the Settings dialog always shows usable values.
func (a *App) GetSettings() (store.Settings, error) {
	s, err := store.LoadSettings()
	if err != nil {
		return s, err
	}
	if s.Terminal.FontFamily == "" {
		s.Terminal.FontFamily = "DejaVu Sans Mono"
	}
	if s.Terminal.FontSize == 0 {
		s.Terminal.FontSize = 13
	}
	if s.Terminal.CursorShape == "" {
		s.Terminal.CursorShape = "beam"
	}
	if s.Terminal.ScrollbackLines == 0 {
		s.Terminal.ScrollbackLines = 10000
	}
	if s.Terminal.Scheme == "" {
		s.Terminal.Scheme = "xterm"
	}
	if s.RemoteMonitoringInterval == 0 {
		s.RemoteMonitoringInterval = 2
	}
	return s, nil
}

// SaveSettings writes the global settings back to config.toml.
func (a *App) SaveSettings(s store.Settings) error { return store.SaveSettings(s) }
