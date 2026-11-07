package gui

import "sort"

// multiExec is the MultiExec state (toolbar toggle + per-tab selection), guarded by App.mu.
type multiExec struct {
	enabled  bool
	selected map[string]bool
}

// targets lists the terminals that receive input typed in tabID (always tabID itself).
func (m *multiExec) targets(tabID string) []string {
	if !m.enabled || !m.selected[tabID] {
		return []string{tabID}
	}
	out := make([]string, 0, len(m.selected))
	for id, on := range m.selected {
		if on {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// SetMultiExec turns MultiExec on or off (the selection is kept, as in the Rust app).
func (a *App) SetMultiExec(enabled bool) {
	a.mu.Lock()
	a.multi.enabled = enabled
	a.mu.Unlock()
}

// SelectMultiExec adds a terminal to (or removes it from) the MultiExec group.
func (a *App) SelectMultiExec(tabID string, selected bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.multi.selected == nil {
		a.multi.selected = map[string]bool{}
	}
	if selected {
		a.multi.selected[tabID] = true
	} else {
		delete(a.multi.selected, tabID)
	}
}
