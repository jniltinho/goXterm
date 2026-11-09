package gui

import (
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestFitWorkArea(t *testing.T) {
	wa := application.Rect{X: 1920, Y: 0, Width: 1280, Height: 752} // second monitor, taskbar at the bottom
	if b, ok := fitWorkArea(application.Rect{X: 1920, Y: -24, Width: 1280, Height: 800}, wa); !ok ||
		b != (application.Rect{X: 1920, Y: 0, Width: 1280, Height: 752}) {
		t.Fatalf("too tall: %+v %v", b, ok)
	}
	if b, ok := fitWorkArea(application.Rect{X: 2000, Y: 100, Width: 800, Height: 600}, wa); ok || b.X != 2000 {
		t.Fatalf("fits: %+v %v", b, ok)
	}
}
