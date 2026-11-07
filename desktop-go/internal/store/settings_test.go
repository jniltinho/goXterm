package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSettingsRoundTrip checks SaveSettings writes the typed keys and LoadSettings reads them back,
// and that a key the Go app does not type is preserved (round-tripped) through a save.
func TestSettingsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	cfgDir := filepath.Join(dir, appDir)
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// seed a config with a key the Go Settings struct does not write back
	seed := "theme = \"dark\"\nsidebar_width = 240.0\nkeyring_hint = true\n[terminal]\nfont_size = 11.0\n"
	if err := os.WriteFile(filepath.Join(cfgDir, settingsFile), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	s.Terminal.FontFamily = "Hack"
	s.Terminal.FontSize = 15
	s.CopyOnSelect = true
	s.RemoteMonitoring = true
	s.RemoteMonitoringInterval = 3
	if s.SidebarOpen == nil || !*s.SidebarOpen || s.SidebarWidth != 240 {
		t.Fatalf("sidebar defaults: open %v width %v", s.SidebarOpen, s.SidebarWidth)
	}
	closed := false
	s.SidebarOpen, s.SidebarWidth, s.SidebarPage, s.GPUBackend = &closed, 300, "sftp", "cpu"
	if err := SaveSettings(s); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.Terminal.FontFamily != "Hack" || got.Terminal.FontSize != 15 || !got.CopyOnSelect ||
		!got.RemoteMonitoring || got.RemoteMonitoringInterval != 3 {
		t.Fatalf("typed keys not persisted: %+v", got)
	}
	if got.SidebarOpen == nil || *got.SidebarOpen || got.SidebarWidth != 300 || got.SidebarPage != "sftp" || got.GPUBackend != "cpu" {
		t.Fatalf("layout/display keys not persisted: %+v", got)
	}

	// a key the Go app does not type must survive the save
	raw, _ := os.ReadFile(filepath.Join(cfgDir, settingsFile))
	if !strings.Contains(string(raw), "keyring_hint") {
		t.Fatalf("existing key dropped:\n%s", raw)
	}
}

// Keys missing from config.toml take the Rust defaults (on for the toggles the Rust app enables).
func TestSettingsDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	os.MkdirAll(filepath.Join(dir, appDir), 0o755)
	os.WriteFile(filepath.Join(dir, appDir, settingsFile), []byte("theme = \"dark\"\ncopy_on_select = false\n"), 0o644)
	s, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.CopyOnSelect || !s.RightClickPaste || !s.PasteGuard || !s.ConfirmClose || !s.SftpAutoSwitch ||
		!s.ShowSessionBanner || !s.RemoteMonitoring || !s.Terminal.CursorBlink || !s.FirstRunThemeDone ||
		s.SidebarOpen == nil || !*s.SidebarOpen || s.DesktopRelease != "Ctrl+Alt" {
		t.Fatalf("defaults not applied: %+v", s)
	}
}
