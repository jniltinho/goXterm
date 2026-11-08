//go:build lab

package gui

import "log/slog"

// StartAgentControlForLab serves agent control from a window-less App, for the MCP lab test
// (cmd/mcp_lab_test.go, build tag lab); never part of a release build.
func StartAgentControlForLab(log *slog.Logger) (stop func()) {
	a := &App{}
	a.startControl(log)
	return a.stopControl
}
