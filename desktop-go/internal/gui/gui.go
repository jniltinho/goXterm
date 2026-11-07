// Package gui opens the rustXterm desktop window on Wails v3: the embedded Vite Plus / Vue SPA in the system webview,
// kept on the app by the navigation guard. Window-state persistence, bound services and the protocol UIs are added by
// the later phases of migrate-gui-to-go-wails; this is the scaffold.
package gui

import (
	"embed"
	"io/fs"
	"log/slog"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// dist is the built SPA, written here by `make frontend` (frontend/ → internal/gui/dist). Kept out of git.
//
//go:embed all:dist
var assets embed.FS

// Run opens the window. safeGPU (from --safe-gpu or RXT_SAFE_GPU) starts the webview with GPU acceleration off.
func Run(version string, safeGPU bool) error {
	dist, err := fs.Sub(assets, "dist")
	if err != nil {
		return err
	}
	svc := &App{version: version}
	opts := application.Options{
		Name:        "rustXterm",
		Description: "rustXterm desktop",
		LogLevel:    slog.LevelInfo, // never Debug: Wails logs binding arguments there
		Services:    []application.Service{application.NewService(svc)},
		Assets:      application.AssetOptions{Handler: application.AssetFileServerFS(dist), DisableLogging: true},
		Linux:       application.LinuxOptions{ProgramName: "rustxterm"},
		Mac:         application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
	}
	winOpts := application.WebviewWindowOptions{
		Title: "rustXterm", Width: 1280, Height: 800, MinWidth: 800, MinHeight: 600,
		// the webview needs none of these; deny by default (D9 web trust boundary)
		Permissions: map[application.PermissionType]application.Permission{
			application.PermissionMicrophone:    application.PermissionDeny,
			application.PermissionCamera:        application.PermissionDeny,
			application.PermissionGeolocation:   application.PermissionDeny,
			application.PermissionNotifications: application.PermissionDeny,
			application.PermissionClipboardRead: application.PermissionDeny,
		},
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
	// Agent control channel (CLI/MCP), off unless the agent_control setting is on; removed on shutdown.
	svc.startControl(app.Logger)
	app.OnShutdown(svc.stopControl)
	// the app's own page loads only once the navigation guard is on (a blank page first where it needs the webview)
	winOpts.URL, winOpts.HTML = guardedStart()
	win = app.Window.NewWithOptions(winOpts)
	// WindowShow can fire more than once (map/expose/focus); do the guard and the first navigation exactly once,
	// otherwise SetURL reloads the page on every event and the content flickers.
	var once sync.Once
	win.OnWindowEvent(events.Common.WindowShow, func(*application.WindowEvent) {
		once.Do(func() {
			if err := guardNavigation(win); err != nil {
				app.Logger.Error(err.Error())
				app.Dialog.Error().SetTitle("rustXterm").SetMessage("The app cannot start safely: " + err.Error()).Show()
				app.Quit()
				return
			}
			if winOpts.URL == "" {
				win.SetURL("/")
			}
		})
	})
	return app.Run()
}
