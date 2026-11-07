package gui

import (
	"reflect"
	"testing"
)

func TestParseFcFamilies(t *testing.T) {
	out := "DejaVu Sans Mono\nNoto Mono\nUbuntu Mono,Ubuntu Mono Regular\nDejaVu Sans Mono\nIBM Plex Mono,IBM Plex Mono Text\n\n"
	want := []string{"DejaVu Sans Mono", "IBM Plex Mono", "Noto Mono", "Ubuntu Mono"}
	if got := parseFcFamilies(out); !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
}
