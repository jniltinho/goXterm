package gui

import "rustxterm-go/internal/store"

// GetSettings returns the global settings (config.toml) with sensible defaults filled for a fresh
// config, so the Settings dialog always shows usable values.
func (a *App) GetSettings() (store.Settings, error) {
	s, err := store.LoadSettings()
	if err != nil {
		return s, err
	}
	// "" (or "monospace") is the Rust default face, Cascadia Mono; the SPA resolves it.
	if s.Terminal.FontSize == 0 {
		s.Terminal.FontSize = 13
	}
	if s.Terminal.CursorShape == "" {
		s.Terminal.CursorShape = "beam"
	}
	if s.Terminal.ScrollbackLines == 0 {
		s.Terminal.ScrollbackLines = 10000
	}
	// "" follows the UI theme (Rust default). Earlier Go builds wrote their own scheme names.
	if n, ok := legacyScheme[s.Terminal.Scheme]; ok {
		s.Terminal.Scheme = n
	}
	if s.RemoteMonitoringInterval == 0 {
		s.RemoteMonitoringInterval = 2
	}
	if s.Theme == "" {
		s.Theme = "system"
	}
	if s.UIScale == "" {
		s.UIScale = "auto"
	}
	switch s.GPUBackend { // the Rust app's values; "hardware"/"software" were an earlier Go-only spelling
	case "", "hardware":
		s.GPUBackend = "auto"
	case "software":
		s.GPUBackend = "cpu"
	}
	on := true
	if s.ToolbarCaptions == nil {
		s.ToolbarCaptions = &on
	}
	if s.ShowTabNumbers == nil {
		s.ShowTabNumbers = &on
	}
	if s.ShowCloseButtons == nil {
		s.ShowCloseButtons = &on
	}
	return s, nil
}

// SaveSettings writes the global settings back to config.toml.
func (a *App) SaveSettings(s store.Settings) error {
	if err := store.SaveSettings(s); err != nil {
		return err
	}
	if a.zoom != nil {
		a.zoom(uiZoom(s.UIScale)) // Interface size applies at once, as in the Rust app
	}
	return nil
}

// Diag is the read-only info shown in Settings → Diagnostics.
type Diag struct {
	Version   string `json:"version"`
	DataDir   string `json:"dataDir"`
	Locked    bool   `json:"locked"`
	Sessions  int    `json:"sessions"`
	Folders   int    `json:"folders"`
	SSHTrust  int    `json:"sshTrust"`
	CertTrust int    `json:"certTrust"`
}

// Diagnostics returns store counts and paths for the Diagnostics tab.
func (a *App) Diagnostics() Diag {
	d := Diag{Version: a.version, DataDir: store.DataDir(), Locked: a.Locked()}
	if st, err := openStore(); err == nil {
		defer st.Close()
		if rows, err := st.Sessions(); err == nil {
			d.Sessions = len(rows)
		}
		if rows, err := st.Folders(); err == nil {
			d.Folders = len(rows)
		}
		d.SSHTrust, _ = st.SSHTrustCount()
		d.CertTrust, _ = st.CertTrustCount()
	}
	return d
}

// legacyScheme maps the scheme names of early Go builds to the Rust catalog names.
var legacyScheme = map[string]string{
	"dark": "rustxterm-dark", "light": "rustxterm-light",
	"solarized-dark": "Solarized Dark", "solarized-light": "Solarized Light",
}
