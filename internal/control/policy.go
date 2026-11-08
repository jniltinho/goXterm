package control

// Policy is a session's agent policy as the authorization boundary sees it: "deny" | "ask" | "allow"
// plus the content-read flag (the store's policies table; anything unknown is treated as deny).
type Policy struct {
	Policy      string
	ContentRead bool
}

// Decision is the outcome of Authorize.
type Decision int

const (
	Allow         Decision = iota
	NeedsApproval          // allowed once the user approves it in the GUI (ask policy, write effect)
	Deny
)

// writes lists the ops with a write effect (rxt_core AgentOp::effects().writes()): remote writes
// (open, send, close, tunnel start, serial line control), local writes (tunnel stop, files get) and
// files put. Reads (read, wait, screenshot, files ls, list ports) are not here.
var writes = map[string]bool{
	"open": true, "send": true, "close": true, "tunnel_start": true, "tunnel_stop": true,
	"serial_send_break": true, "serial_set_lines": true, "files_get": true, "files_put": true,
}

// Writes reports whether op has a write effect (so an ask policy needs an approval).
func Writes(op string) bool { return writes[op] }

// TargetsSession reports whether op acts on one session and so is subject to its policy.
func TargetsSession(op string) bool {
	g := Group(op)
	return (g == GroupLive || g == GroupContentRead) && op != "list_tabs" && op != "serial_list_ports"
}

// Authorize is the single authorization boundary (Rust engine::control::authorize). policy is the
// target session's policy for ops that act on a session (nil there means no such session); other
// ops ignore it. The message explains a denial and is sent to the agent as is.
func Authorize(entry, op string, policy *Policy) (Decision, string) {
	g := Group(op)
	if entry == EntryGui || g == GroupGuiOnly {
		return Deny, op + " is GUI-only"
	}
	if !Allowed(g, entry) {
		return Deny, op + " is not allowed from " + entry
	}
	if entry == EntryCliOffline || entry == EntryMcpOffline || !TargetsSession(op) {
		return Allow, ""
	}
	if policy == nil {
		return Deny, "no such session"
	}
	switch policy.Policy {
	case "allow", "ask":
	default:
		return Deny, "denied by policy"
	}
	if g == GroupContentRead && !policy.ContentRead {
		return Deny, "content read is not allowed for this session"
	}
	if policy.Policy == "ask" && Writes(op) {
		return NeedsApproval, ""
	}
	return Allow, ""
}
