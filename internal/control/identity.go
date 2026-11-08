package control

import "slices"

// Identity is the security context of one side of the Windows control pipe (Rust platform-windows
// Identity): user SID, restricting SIDs or AppContainer, integrity RID and the enabled / deny-only
// group SIDs. The rules below are pure so they are tested on every platform.
type Identity struct {
	SID        string
	Restricted bool
	Integrity  uint32
	Enabled    []string
	DenyOnly   []string
}

// differentElevation is the refusal when the two sides run at a different elevation.
const differentElevation = "agent control runs at a different elevation (run the CLI and rustXterm both elevated or both not)"

const (
	mediumIntegrity = 0x2000         // below it a token is sandboxed, not just non-elevated
	administrators  = "S-1-5-32-544" // deny-only in a UAC-filtered (non-elevated) admin token
)

// moreFiltered lists the groups enabled for a that are deny-only for b (b is more filtered than a).
func moreFiltered(b, a *Identity) []string {
	var out []string
	for _, g := range b.DenyOnly {
		if slices.Contains(a.Enabled, g) {
			out = append(out, g)
		}
	}
	return out
}

// PeerRefusal is why the client of the control pipe is refused from me's side, "" when it may be
// trusted: same user SID, no restricting SIDs or AppContainer, integrity not below mine and not
// more filtered than me. An unknown identity (nil) is refused.
func PeerRefusal(peer, me *Identity) string {
	if peer == nil {
		return "agent control could not identify the client"
	}
	filtered := moreFiltered(peer, me)
	if peer.SID != me.SID {
		return "agent control serves only the user running rustXterm"
	}
	if peer.Restricted || slices.ContainsFunc(filtered, func(g string) bool { return g != administrators }) {
		return "agent control refuses restricted, filtered or AppContainer clients"
	}
	if peer.Integrity < me.Integrity && peer.Integrity < mediumIntegrity {
		return "agent control refuses low-integrity clients"
	}
	if peer.Integrity < me.Integrity || len(filtered) > 0 {
		return differentElevation
	}
	return ""
}

// ServerRefusal is the client-side check of the pipe server, "" when it may be trusted: the same
// policy as for clients and the same elevation on both sides.
func ServerRefusal(server, me *Identity) string {
	if server != nil && server.SID == me.SID && !server.Restricted &&
		(server.Integrity != me.Integrity ||
			slices.Contains(moreFiltered(server, me), administrators) ||
			slices.Contains(moreFiltered(me, server), administrators)) {
		return differentElevation
	}
	if PeerRefusal(server, me) == "" {
		return ""
	}
	return "control pipe is not served by rustXterm of this user"
}

// pipeName / pipeSDDL are the per-user pipe and its owner-only protected DACL (Rust pipe_name /
// pipe_sddl), so the Rust and Go apps share one endpoint.
func pipeName(sid string) string { return `\\.\pipe\rustxterm-` + sid + `-control` }
func pipeSDDL(sid string) string { return "D:P(A;;GA;;;" + sid + ")" }
