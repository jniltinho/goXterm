//go:build !linux

package gui

import "github.com/wailsapp/wails/v3/pkg/application"

// guardNavigation has no native hook on Windows and macOS in Wails v3: the CSP and the SPA's link guard keep the
// window on the app there.
// shortcut: no native navigation cancel on Windows/macOS (migrate-gui-to-go-wails design D9 / task 3.4).
func guardNavigation(*application.WebviewWindow) error { return nil }

// guardedStart loads the app at once: there is no native guard to wait for.
func guardedStart() (start, html string) { return "/", "" }
