package gui

import (
	"os/exec"
	"runtime"
	"sort"
	"strings"
)

// SystemMonoFonts lists the monospace families installed on this machine (Linux: fontconfig), for
// Settings › Terminal › Font family next to the bundled faces. Other platforms return nil; the SPA
// probes the usual macOS/Windows faces itself.
func (a *App) SystemMonoFonts() []string {
	if runtime.GOOS != "linux" {
		return nil
	}
	out, err := exec.Command("fc-list", ":spacing=mono", "family").Output()
	if err != nil {
		return nil
	}
	return parseFcFamilies(string(out))
}

// parseFcFamilies reads `fc-list : family` output: one font per line, aliases comma-separated (the
// first is the family name); unique, sorted.
func parseFcFamilies(out string) []string {
	seen := map[string]bool{}
	var names []string
	for _, l := range strings.Split(out, "\n") {
		name := strings.TrimSpace(strings.Split(l, ",")[0])
		name = strings.ReplaceAll(name, `\-`, "-")
		if name != "" && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
