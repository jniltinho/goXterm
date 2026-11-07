package gui

import "testing"

func TestRdpSize(t *testing.T) {
	fixed := map[string]any{"fixed": map[string]any{"width": float64(1024), "height": float64(768)}}
	cases := []struct {
		name       string
		res        any
		tabW, tabH int
		w, h       int
	}{
		{"fixed resolution wins", fixed, 1900, 1000, 1024, 768},
		{"fit to tab (string form)", "fittotab", 1851, 1003, 1848, 1003},
		{"fit to tab (absent)", nil, 1600, 900, 1600, 900},
		{"tiny tab clamps up", nil, 300, 200, 640, 480},
		{"huge tab clamps down", nil, 9000, 5000, 4096, 2048},
		{"no layout yet", nil, 0, 0, 1280, 800},
	}
	for _, c := range cases {
		if w, h := rdpSize(c.res, c.tabW, c.tabH); w != c.w || h != c.h {
			t.Errorf("%s: got %dx%d, want %dx%d", c.name, w, h, c.w, c.h)
		}
	}
}
