package gui

import (
	"encoding/json"
	"fmt"

	"golang.org/x/crypto/ssh"

	"rustxterm-go/internal/sshconn"
	"rustxterm-go/internal/tunnelconn"
)

// tunRun is a running tunnel plus the SSH client it rides (one client per running tunnel; closed on
// stop). The engine lives in internal/tunnelconn; this layer only wires it to the store and the SPA.
type tunRun struct {
	t      *tunnelconn.Tunnel
	client *ssh.Client
}

// Tunnels returns every saved tunnel with a "running" flag reflecting live state.
func (a *App) Tunnels() ([]map[string]any, error) {
	st, err := openStore()
	if err != nil {
		return nil, err
	}
	rows, err := st.Tunnels()
	st.Close()
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []map[string]any{} // JSON [] rather than null for "no tunnels"
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, m := range rows {
		m["running"] = a.tun[tunKey(asInt64(m["via"]), asString(m["name"]))] != nil
	}
	return rows, nil
}

// TunnelSave inserts or replaces a tunnel (JSON document), then returns the refreshed list.
func (a *App) TunnelSave(js string) error {
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	return st.SaveTunnel(js)
}

// TunnelDelete removes a tunnel (stopping it first if running).
func (a *App) TunnelDelete(via int64, name string) error {
	_ = a.TunnelStop(via, name)
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	return st.DeleteTunnel(via, name)
}

// TunnelStart opens (if needed) an SSH client to the tunnel's gateway session and starts the forward.
// js is the tunnel document (as TunnelSave stores it).
func (a *App) TunnelStart(js string) error {
	var m map[string]any
	if err := json.Unmarshal([]byte(js), &m); err != nil {
		return fmt.Errorf("invalid tunnel json: %w", err)
	}
	return a.startTunnel(m)
}

// TunnelStop stops a running tunnel and closes its SSH client. A tunnel that is not running is a no-op.
func (a *App) TunnelStop(via int64, name string) error {
	a.mu.Lock()
	run := a.tun[tunKey(via, name)]
	delete(a.tun, tunKey(via, name))
	a.mu.Unlock()
	if run == nil {
		return nil
	}
	_ = run.t.Close()
	_ = run.client.Close()
	a.emitTunnel(via, name, "stopped", "")
	return nil
}

// TunnelStartAll / TunnelStopAll operate on every saved tunnel.
func (a *App) TunnelStartAll() error {
	st, err := openStore()
	if err != nil {
		return err
	}
	rows, err := st.Tunnels()
	st.Close()
	if err != nil {
		return err
	}
	var firstErr error
	for _, m := range rows {
		if err := a.startTunnel(m); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (a *App) TunnelStopAll() error {
	a.mu.Lock()
	runs := make([]*tunRun, 0, len(a.tun))
	for k, run := range a.tun {
		runs = append(runs, run)
		delete(a.tun, k)
	}
	a.mu.Unlock()
	for _, run := range runs {
		_ = run.t.Close()
		_ = run.client.Close()
	}
	a.emit("tunnel:allstopped", nil)
	return nil
}

// startTunnel builds the Spec from the tunnel document, dials the gateway session and starts the
// forward, emitting state to the SPA. A tunnel already running is restarted.
func (a *App) startTunnel(m map[string]any) error {
	via := asInt64(m["via"])
	name := asString(m["name"])
	if via == 0 || name == "" {
		return fmt.Errorf("tunnel needs a via session and a name")
	}
	_ = a.TunnelStop(via, name) // restart if already up
	a.emitTunnel(via, name, "starting", "")

	client, err := a.sshClientForSession(via)
	if err != nil {
		a.emitTunnel(via, name, "error", err.Error())
		return err
	}
	spec := tunnelconn.Spec{
		Kind:              asString(m["kind"]),
		BindAddr:          asStringOr(m["bind_addr"], "127.0.0.1"),
		BindPort:          int(asInt64(m["bind_port"])),
		DestHost:          asString(m["dest_host"]),
		DestPort:          int(asInt64(m["dest_port"])),
		ExposureConfirmed: asBool(m["exposure_confirmed"]),
	}
	if spec.Kind == "" {
		spec.Kind = "local"
	}
	t, err := tunnelconn.Start(client, spec)
	if err != nil {
		client.Close()
		a.emitTunnel(via, name, "error", err.Error())
		return err
	}
	a.mu.Lock()
	if a.tun == nil {
		a.tun = map[string]*tunRun{}
	}
	a.tun[tunKey(via, name)] = &tunRun{t: t, client: client}
	a.mu.Unlock()
	a.emitTunnel(via, name, "running", "")
	return nil
}

// sshClientForSession dials the SSH gateway for a saved session (same auth/host-key path as the
// terminal), returning a bare client for port-forwarding (no shell).
func (a *App) sshClientForSession(id int64) (*ssh.Client, error) {
	m, password, err := sessionRawByID(id)
	if err != nil {
		return nil, err
	}
	sv := viewOf(m)
	if sv.Protocol != "ssh" {
		return nil, fmt.Errorf("tunnel gateway %q is not an SSH session", sv.Name)
	}
	sshOpt := subMap(m, "ssh")
	return sshconn.DialClient(sshconn.Config{
		Host: sv.Host, Port: sv.Port, User: sv.User, Password: password,
		KeyFile: asString(sshOpt["key_file"]), UseAgent: asBool(sshOpt["use_agent"]),
		AskPassword: a.askPassword(sv.Host, sv.User),
		HostKey:     a.sshHostKeyCallback(sv.Host, sv.Port),
	})
}

func (a *App) emitTunnel(via int64, name, state, errMsg string) {
	if a.emit != nil {
		a.emit("tunnel:state", map[string]any{"via": via, "name": name, "state": state, "error": errMsg})
	}
}

func tunKey(via int64, name string) string { return fmt.Sprintf("%d\x00%s", via, name) }

func asStringOr(v any, def string) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return def
}
