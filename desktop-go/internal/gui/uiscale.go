package gui

import (
	"math"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// uiZoom is the webview zoom for Settings › Display › Interface size (Rust UiScale): 100/125/150/200 are
// absolute; Auto follows the desktop's scale like egui/winit, which on X11 reads Xft.dpi — WebKitGTK
// does not apply that fractional scale itself, so it is applied here as zoom (on top of GDK_SCALE).
func uiZoom(scale string) float64 {
	switch scale {
	case "100":
		return 1
	case "125":
		return 1.25
	case "150":
		return 1.5
	case "200":
		return 2
	}
	return autoZoom()
}

func autoZoom() float64 {
	if runtime.GOOS != "linux" || os.Getenv("WAYLAND_DISPLAY") != "" && os.Getenv("GDK_BACKEND") != "x11" {
		return 1 // macOS/Windows webviews and GTK on Wayland already follow the system scale
	}
	gdk := os.Getenv("GDK_SCALE")
	if out, err := exec.Command("xrdb", "-query").Output(); err == nil && strings.Contains(string(out), "Xft.dpi") {
		return zoomFromXrdb(string(out), gdk)
	}
	if out, err := exec.Command("xrandr", "--query").Output(); err == nil {
		return zoomFromXrandr(string(out), gdk)
	}
	return 1
}

var monitorRe = regexp.MustCompile(`(?m)^\S+ connected( primary)? (\d+)x(\d+)\+\d+\+\d+ .*? (\d+)mm x (\d+)mm`)

// zoomFromXrandr is winit's X11 fallback without Xft.dpi: the monitor's pixel density
// (√(px²/mm²)), as a multiple of 96 dpi rounded to twelfths — the primary monitor, else the first.
func zoomFromXrandr(xrandr, gdkScale string) float64 {
	ms := monitorRe.FindAllStringSubmatch(xrandr, -1)
	if len(ms) == 0 {
		return 1
	}
	m := ms[0]
	for _, c := range ms {
		if c[1] != "" {
			m = c
		}
	}
	f := func(s string) float64 { v, _ := strconv.ParseFloat(s, 64); return v }
	w, h, wmm, hmm := f(m[2]), f(m[3]), f(m[4]), f(m[5])
	if wmm <= 0 || hmm <= 0 {
		return 1
	}
	ppmm := math.Sqrt(w * h / (wmm * hmm))
	scale := math.Round(ppmm*(12*25.4/96)) / 12
	g, err := strconv.Atoi(gdkScale)
	if err != nil || g < 1 {
		g = 1
	}
	return min(3, max(1, scale/float64(g)))
}

// zoomFromXrdb turns `xrdb -query` output into a zoom: Xft.dpi/96 divided by the integer GDK scale the
// webview already renders at, kept within 1..3.
func zoomFromXrdb(xrdb, gdkScale string) float64 {
	dpi := 96.0
	for _, l := range strings.Split(xrdb, "\n") {
		if k, v, ok := strings.Cut(l, ":"); ok && strings.TrimSpace(k) == "Xft.dpi" {
			if d, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && d > 0 {
				dpi = d
			}
		}
	}
	g, err := strconv.Atoi(gdkScale)
	if err != nil || g < 1 {
		g = 1
	}
	return min(3, max(1, dpi/96/float64(g)))
}
