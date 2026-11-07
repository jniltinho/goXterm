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
	seed := "theme = \"dark\"\nsidebar_width = 240.0\n[terminal]\nfont_size = 11.0\n"
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

	// the untyped key (sidebar_width) must survive the save
	raw, _ := os.ReadFile(filepath.Join(cfgDir, settingsFile))
	if !strings.Contains(string(raw), "sidebar_width") {
		t.Fatalf("untyped key sidebar_width was dropped:\n%s", raw)
	}
}
