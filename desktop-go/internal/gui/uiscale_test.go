package gui

import (
	"math"
	"testing"
)

func TestZoomFromXrdb(t *testing.T) {
	for _, c := range []struct {
		xrdb, gdk string
		want      float64
	}{
		{"Xft.dpi:\t96\n", "", 1},
		{"Xcursor.size: 24\nXft.dpi:\t120\n", "", 1.25},
		{"Xft.dpi: 192\n", "2", 1},
		{"no dpi here\n", "", 1},
		{"Xft.dpi: 72\n", "", 1},
	} {
		if got := zoomFromXrdb(c.xrdb, c.gdk); got != c.want {
			t.Errorf("%q gdk=%q: %v, want %v", c.xrdb, c.gdk, got, c.want)
		}
	}
	two := "DP-0 connected 2560x1440+2560+0 (normal left inverted right x axis y axis) 597mm x 336mm\n" +
		"DP-4 connected primary 2560x1440+0+0 (normal left inverted right x axis y axis) 597mm x 336mm\n"
	if got := zoomFromXrandr(two, ""); math.Abs(got-14.0/12) > 1e-9 {
		t.Errorf("27\" 1440p: %v, want 14/12 (winit)", got)
	}
	if got := zoomFromXrandr("eDP connected primary 1920x1080+0+0 (normal) 0mm x 0mm\n", ""); got != 1 {
		t.Errorf("unknown size: %v", got)
	}
	if uiZoom("150") != 1.5 || uiZoom("100") != 1 {
		t.Fatal("fixed sizes")
	}
}
