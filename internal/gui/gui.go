// Package gui opens the rustXterm desktop window on Wails v3: the embedded Vite Plus / Vue SPA in the system webview,
// kept on the app by the navigation guard. Window-state persistence, bound services and the protocol UIs are added by
// the later phases of migrate-gui-to-go-wails; this is the scaffold.
package gui

import (
	"embed"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"rustxterm-go/internal/store"
)

// dist is the built SPA, written here by `make frontend` (frontend/ → internal/gui/dist). Kept out of git.
//
//go:embed all:dist
var assets embed.FS

// appID is the GTK application id: Wayland compositors match it to the .desktop file for the window icon
// (installDesktopEntry), X11 to the entry's StartupWMClass.
const appID = "io.github.jniltinho.goxterm"

// Run opens the window. safeGPU (from --safe-gpu or RXT_SAFE_GPU) starts the webview with GPU acceleration off.
func Run(version string, safeGPU bool) error {
	dist, err := fs.Sub(assets, "dist")
	if err != nil {
		return err
	}
	svc := &App{version: version}
	icon, _ := fs.ReadFile(dist, "icons/logo.png") // window/taskbar icon; nil (Wails default) in a stub build
	if _, err := installDesktopEntry(icon); err != nil {
		slog.Warn("desktop entry not installed", "error", err) // only the Linux title-bar/dock icon depends on it
	}
	opts := application.Options{
		Name:        "goXterm",
		Description: "goXterm desktop",
		Icon:        icon,
		LogLevel:    slog.LevelInfo, // never Debug: Wails logs binding arguments there
		Services:    []application.Service{application.NewService(svc)},
		Assets:      application.AssetOptions{Handler: application.AssetFileServerFS(dist), DisableLogging: true},
		Linux:       application.LinuxOptions{ApplicationID: appID}, // the program name follows it
		Mac:         application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
	}
	winOpts := application.WebviewWindowOptions{
		Title: "goXterm", Width: 1280, Height: 800, MinWidth: 800, MinHeight: 600,
		// the webview needs none of these; deny by default (D9 web trust boundary)
		Permissions: map[application.PermissionType]application.Permission{
			application.PermissionMicrophone:    application.PermissionDeny,
			application.PermissionCamera:        application.PermissionDeny,
			application.PermissionGeolocation:   application.PermissionDeny,
			application.PermissionNotifications: application.PermissionDeny,
			application.PermissionClipboardRead: application.PermissionDeny,
		},
	}
	// Settings › Display › Rendering backend "Software (CPU)" (shared config.toml value "cpu").
	if s, err := store.LoadSettings(); err == nil && s.GPUBackend == "cpu" {
		safeGPU = true
	}
	if safeGPU {
		safeGraphics(&opts, &winOpts)
	}
	var win *application.WebviewWindow
	opts.SingleInstance = &application.SingleInstanceOptions{
		UniqueID: "io.github.jniltinho.rustxterm.desktop",
		OnSecondInstanceLaunch: func(application.SecondInstanceData) {
			if win != nil {
				win.Restore()
				win.Focus()
			}
		},
	}
	app := application.New(opts)
	svc.emit = func(name string, data any) { app.Event.Emit(name, data) }
	svc.pickFile = func() (string, error) {
		d := app.Dialog.OpenFile().CanChooseFiles(true).SetTitle("Select SSH private key")
		if home, err := os.UserHomeDir(); err == nil {
			d.SetDirectory(filepath.Join(home, ".ssh"))
		}
		return d.PromptForSingleSelection()
	}
	svc.quit = app.Quit
	svc.clip = app.Clipboard
	svc.openPath = func(title, dir string) (string, error) {
		d := app.Dialog.OpenFile().CanChooseFiles(true).SetTitle(title)
		if dir != "" {
			d.SetDirectory(dir)
		} else if home, err := os.UserHomeDir(); err == nil {
			d.SetDirectory(home)
		}
		return d.PromptForSingleSelection()
	}
	svc.savePath = func(name string) (string, error) {
		d := app.Dialog.SaveFile().CanCreateDirectories(true).SetFilename(name)
		if home, err := os.UserHomeDir(); err == nil {
			d.SetDirectory(home)
		}
		return d.PromptForSingleSelection()
	}
	// Agent control channel (CLI/MCP), off unless the agent_control setting is on; removed on shutdown.
	svc.startControl(app.Logger)
	app.OnShutdown(svc.stopControl)
	// the app's own page loads only once the navigation guard is on (a blank page first where it needs the webview)
	winOpts.URL, winOpts.HTML = guardedStart()
	if s, err := store.LoadSettings(); err == nil {
		winOpts.Zoom = uiZoom(s.UIScale) // Settings › Display › Interface size, from the first paint
	}
	win = app.Window.NewWithOptions(winOpts)
	svc.zoom = func(f float64) { win.SetZoom(f) }
	// WindowShow can fire more than once (map/expose/focus); do the guard and the first navigation exactly once,
	// otherwise SetURL reloads the page on every event and the content flickers.
	var once sync.Once
	win.OnWindowEvent(events.Common.WindowShow, func(*application.WindowEvent) {
		once.Do(func() {
			if err := guardNavigation(win); err != nil {
				app.Logger.Error(err.Error())
				app.Dialog.Error().SetTitle("goXterm").SetMessage("The app cannot start safely: " + err.Error()).Show()
				app.Quit()
				return
			}
			if winOpts.URL == "" {
				win.SetURL("/")
			}
			if s, err := win.GetScreen(); err == nil && s != nil {
				if b, ok := fitWorkArea(win.Bounds(), s.WorkArea); ok {
					win.SetBounds(b)
				}
			}
		})
	})
	return app.Run()
}

// fitWorkArea shrinks and centres a window that does not fit its screen's work area: the 1280×800 window centred
// on a smaller screen (1366×768, 1920×1080 at 150 %) put its title bar off-screen, so it could not be dragged (nor
// moved to another monitor).
func fitWorkArea(b, wa application.Rect) (application.Rect, bool) {
	if b.Width <= wa.Width && b.Height <= wa.Height && b.Y >= wa.Y {
		return b, false
	}
	b.Width, b.Height = min(b.Width, wa.Width), min(b.Height, wa.Height)
	b.X, b.Y = wa.X+(wa.Width-b.Width)/2, wa.Y+(wa.Height-b.Height)/2
	return b, true
}
