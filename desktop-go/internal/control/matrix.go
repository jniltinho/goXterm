package control

// Permission groups (rxt_core::control::Group).
const (
	GroupRead        = "read"
	GroupStoreWrite  = "store_write"
	GroupLive        = "live"
	GroupContentRead = "content_read"
	GroupGuiOnly     = "gui_only"
)

// opGroup maps each wire op discriminant (serde snake_case variant name) to its permission group.
var opGroup = map[string]string{
	"list_sessions":        GroupRead,
	"show_session":         GroupRead,
	"list_folders":         GroupRead,
	"list_tunnels":         GroupRead,
	"export":               GroupRead,
	"add_session":          GroupStoreWrite,
	"edit_session":         GroupStoreWrite,
	"remove_session":       GroupStoreWrite,
	"import_mobaxterm_ini": GroupStoreWrite,
	"open":                 GroupLive,
	"list_tabs":            GroupLive,
	"send":                 GroupLive,
	"wait":                 GroupLive,
	"close":                GroupLive,
	"tunnel_start":         GroupLive,
	"tunnel_stop":          GroupLive,
	"serial_send_break":    GroupLive,
	"serial_set_lines":     GroupLive,
	"serial_list_ports":    GroupLive,
	"read":                 GroupContentRead,
	"screenshot":           GroupContentRead,
	"files_ls":             GroupContentRead,
	"files_get":            GroupContentRead,
	"files_put":            GroupContentRead,
	"gui_only":             GroupGuiOnly,
}

// Group returns the permission group of a wire op. An unknown op is treated as GUI-only, so it is
// denied to every agent entry point (fail closed).
func Group(op string) string {
	if g, ok := opGroup[op]; ok {
		return g
	}
	return GroupGuiOnly
}

// Allowed is the fixed permission matrix (agent-control spec), identical to rxt_core::control::allowed.
func Allowed(group, entry string) bool {
	switch group {
	case GroupRead:
		return true
	case GroupStoreWrite:
		return entry == EntryGui || entry == EntryCliOffline
	case GroupLive, GroupContentRead:
		return entry == EntryGui || entry == EntryCliLive || entry == EntryMcpLive
	case GroupGuiOnly:
		return entry == EntryGui
	default:
		return false
	}
}
