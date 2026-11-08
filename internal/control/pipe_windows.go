//go:build windows

package control

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Named-pipe transport (Rust platform-windows control, design W4): `\\.\pipe\rustxterm-<SID>-control`,
// exclusive first instance, remote clients rejected, a protected DACL for the user's SID only. Each
// connection is authenticated by its own security context (impersonation for identification, after
// reading the 4-byte frame length): same user, unrestricted, not AppContainer, integrity not below
// ours (PeerRefusal); otherwise it gets one denied response with the reason and is closed. The
// client checks the server process's user and elevation (ServerRefusal). While a connection is
// handed out a spare instance keeps listening.

var (
	advapi32              = windows.NewLazySystemDLL("advapi32.dll")
	kernel32              = windows.NewLazySystemDLL("kernel32.dll")
	procImpersonateClient = advapi32.NewProc("ImpersonateNamedPipeClient")
	procIsTokenRestricted = advapi32.NewProc("IsTokenRestricted")
	procWaitNamedPipe     = kernel32.NewProc("WaitNamedPipeW")
)

const (
	tokenIsAppContainer = 29 // TOKEN_INFORMATION_CLASS TokenIsAppContainer (after TokenLogonSid)
	busyWait            = 2 * time.Second
	preAuth             = 5 * time.Second
	refusalLinger       = time.Second
	pipeBuffer          = 64 << 10
)

var errNotRunning = errors.New("no running instance with agent control enabled")

// IsNotRunning reports whether err means there is no running instance to connect to.
func IsNotRunning(err error) bool { return errors.Is(err, errNotRunning) }

// RuntimeDir has no meaning on Windows (the pipe name comes from the user SID); it never fails so
// Call reaches Connect.
func RuntimeDir() (string, error) { return "", nil }

// Listener accepts same-user control connections on the per-user pipe.
type Listener struct {
	name    string
	me      Identity
	closeEv windows.Handle

	mu      sync.Mutex
	next    windows.Handle // the instance waiting for the next client
	waiting bool           // Accept is blocked connecting next
	closed  bool
}

// Listen creates the first pipe instance; an existing one (ours or a squatter's) means in use.
func Listen(string) (*Listener, error) {
	me, err := currentIdentity()
	if err != nil {
		return nil, fmt.Errorf("agent control: cannot read the current user identity: %w", err)
	}
	name := pipeName(me.SID)
	h, err := createPipe(name, me.SID, true)
	if err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return nil, fmt.Errorf("agent control pipe %s is in use", name)
		}
		return nil, fmt.Errorf("agent control pipe %s: %w", name, err)
	}
	ev, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	return &Listener{name: name, me: me, closeEv: ev, next: h}, nil
}

func createPipe(name, sid string, first bool) (windows.Handle, error) {
	sd, err := windows.SecurityDescriptorFromString(pipeSDDL(sid))
	if err != nil {
		return 0, err
	}
	sa := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	flags := uint32(windows.PIPE_ACCESS_DUPLEX | windows.FILE_FLAG_OVERLAPPED)
	if first {
		flags |= windows.FILE_FLAG_FIRST_PIPE_INSTANCE
	}
	n, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	mode := uint32(windows.PIPE_TYPE_BYTE | windows.PIPE_READMODE_BYTE | windows.PIPE_WAIT | windows.PIPE_REJECT_REMOTE_CLIENTS)
	return windows.CreateNamedPipe(n, flags, mode, windows.PIPE_UNLIMITED_INSTANCES, pipeBuffer, pipeBuffer, 0, sa)
}

// Accept returns the next authenticated same-user connection.
func (l *Listener) Accept() (Conn, error) {
	for {
		l.mu.Lock()
		if l.closed {
			l.mu.Unlock()
			return nil, net.ErrClosed
		}
		h := l.next
		l.waiting = true
		l.mu.Unlock()
		err := l.connect(h)
		l.mu.Lock()
		l.waiting = false
		if l.closed {
			windows.CloseHandle(h)
			l.mu.Unlock()
			return nil, net.ErrClosed
		}
		// A fresh instance takes over listening before the connected one is handed out.
		fresh, ferr := createPipe(l.name, l.me.SID, false)
		if ferr != nil {
			l.mu.Unlock()
			windows.CloseHandle(h)
			return nil, ferr
		}
		l.next = fresh
		l.mu.Unlock()
		if err != nil {
			windows.CloseHandle(h)
			continue // a client that went away before we saw it
		}
		// shortcut: authentication runs inline, so a silent same-user client delays the next accept by
		// up to preAuth (as in the Rust app); other users cannot open the pipe at all.
		if c := l.authenticate(h); c != nil {
			return c, nil
		}
	}
}

// connect waits for a client on instance h, or for Close.
func (l *Listener) connect(h windows.Handle) error {
	ev, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(ev)
	ov := windows.Overlapped{HEvent: ev}
	switch err := windows.ConnectNamedPipe(h, &ov); {
	case err == nil, errors.Is(err, windows.ERROR_PIPE_CONNECTED):
		return nil
	case !errors.Is(err, windows.ERROR_IO_PENDING):
		return err
	}
	var n uint32
	r, err := windows.WaitForMultipleObjects([]windows.Handle{ev, l.closeEv}, false, windows.INFINITE)
	if err == nil && r == windows.WAIT_OBJECT_0 {
		return windows.GetOverlappedResult(h, &ov, &n, false)
	}
	_ = windows.CancelIoEx(h, &ov)
	_ = windows.GetOverlappedResult(h, &ov, &n, true)
	return net.ErrClosed
}

// authenticate does the bounded pre-authentication read, then the impersonation check.
func (l *Listener) authenticate(h windows.Handle) Conn {
	f := os.NewFile(uintptr(h), l.name) // overlapped handle: Go associates it with its I/O port
	_ = f.SetReadDeadline(time.Now().Add(preAuth))
	var head [4]byte
	if _, err := io.ReadFull(f, head[:]); err != nil {
		f.Close()
		return nil
	}
	_ = f.SetReadDeadline(time.Time{})
	var peer *Identity
	if id, err := pipeClientIdentity(h); err == nil {
		peer = &id
	}
	if reason := PeerRefusal(peer, &l.me); reason != "" {
		go refuse(f, reason)
		return nil
	}
	return &pipeConn{r: io.MultiReader(bytes.NewReader(head[:]), f), f: f, h: h, server: true}
}

// Close stops listening; the pipe disappears with its last instance.
func (l *Listener) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	_ = windows.SetEvent(l.closeEv)
	if !l.waiting {
		windows.CloseHandle(l.next) // otherwise Accept closes it when it wakes up
	}
	return nil
}

// Connect opens the per-user pipe and requires its server to be rustXterm of this same user.
func Connect(string) (Conn, error) {
	me, err := currentIdentity()
	if err != nil {
		return nil, fmt.Errorf("agent control: cannot read the current user identity: %w", err)
	}
	name := pipeName(me.SID)
	n, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(busyWait)
	var h windows.Handle
	for {
		h, err = windows.CreateFile(n, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING,
			windows.FILE_FLAG_OVERLAPPED|windows.SECURITY_SQOS_PRESENT|windows.SECURITY_IDENTIFICATION, 0)
		if err == nil {
			break
		}
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
			return nil, errNotRunning
		}
		if errors.Is(err, windows.ERROR_PIPE_BUSY) && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		return nil, fmt.Errorf("agent control pipe: %w", err)
	}
	var server *Identity
	var pid uint32
	if windows.GetNamedPipeServerProcessId(h, &pid) == nil {
		if id, err := processIdentity(pid); err == nil {
			server = &id
		}
	}
	if reason := ServerRefusal(server, &me); reason != "" {
		windows.CloseHandle(h)
		return nil, errors.New(reason)
	}
	f := os.NewFile(uintptr(h), name)
	return &pipeConn{r: f, f: f, h: h}, nil
}

// Live reports whether the pipe exists without taking an instance (WaitNamedPipe does not connect).
func Live() bool {
	me, err := currentIdentity()
	if err != nil {
		return false
	}
	n, err := windows.UTF16PtrFromString(pipeName(me.SID))
	if err != nil {
		return false
	}
	ok, _, callErr := procWaitNamedPipe.Call(uintptr(unsafe.Pointer(n)), 1)
	return ok != 0 || !errors.Is(callErr, windows.ERROR_FILE_NOT_FOUND)
}

// pipeConn is one connection; a server connection first replays the 4 bytes read while authenticating
// and flushes on close so the client can read the last response.
type pipeConn struct {
	r      io.Reader
	f      *os.File
	h      windows.Handle
	server bool
}

func (c *pipeConn) Read(p []byte) (int, error)  { return c.r.Read(p) }
func (c *pipeConn) Write(p []byte) (int, error) { return c.f.Write(p) }
func (c *pipeConn) Close() error {
	if c.server {
		_ = windows.FlushFileBuffers(c.h)
	}
	return c.f.Close()
}

// refuse tells a refused client why, then closes, draining its request (bounded).
func refuse(f *os.File, reason string) {
	defer f.Close()
	_ = f.SetDeadline(time.Now().Add(refusalLinger))
	if err := WriteMessage(f, Err(0, CodeDenied, reason)); err == nil {
		_, _ = io.Copy(io.Discard, f)
	}
}

// pipeClientIdentity reads the security context the client connected with: impersonate it on this
// OS thread, open the thread token, revert — with nothing in between. Windows allows this only after
// data was read from the pipe.
func pipeClientIdentity(h windows.Handle) (Identity, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if r, _, err := procImpersonateClient.Call(uintptr(h)); r == 0 {
		return Identity{}, err
	}
	thread := windows.CurrentThread()
	var tok windows.Token
	// openAsSelf: access is checked against the process token, not the impersonated one.
	openErr := windows.OpenThreadToken(thread, windows.TOKEN_QUERY, true, &tok)
	if err := windows.RevertToSelf(); err != nil {
		panic("agent control: cannot revert the client impersonation") // never keep a client's identity
	}
	if openErr != nil {
		return Identity{}, openErr
	}
	defer tok.Close()
	return tokenIdentity(tok)
}

func currentIdentity() (Identity, error) {
	return processTokenIdentity(windows.CurrentProcess())
}

func processIdentity(pid uint32) (Identity, error) {
	p, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return Identity{}, err
	}
	defer windows.CloseHandle(p)
	return processTokenIdentity(p)
}

func processTokenIdentity(p windows.Handle) (Identity, error) {
	var tok windows.Token
	if err := windows.OpenProcessToken(p, windows.TOKEN_QUERY, &tok); err != nil {
		return Identity{}, err
	}
	defer tok.Close()
	return tokenIdentity(tok)
}

func tokenIdentity(tok windows.Token) (Identity, error) {
	var id Identity
	user, err := tok.GetTokenUser()
	if err != nil {
		return id, err
	}
	id.SID = user.User.Sid.String()
	if r, _, _ := procIsTokenRestricted.Call(uintptr(tok)); r != 0 {
		id.Restricted = true
	} else {
		buf, err := tokenInfo(tok, tokenIsAppContainer)
		if err != nil {
			return id, err
		}
		id.Restricted = *(*uint32)(unsafe.Pointer(&buf[0])) != 0
	}
	buf, err := tokenInfo(tok, windows.TokenIntegrityLevel)
	if err != nil {
		return id, err
	}
	label := (*windows.Tokenmandatorylabel)(unsafe.Pointer(&buf[0])).Label.Sid.String()
	rid, ok := strings.CutPrefix(label, "S-1-16-")
	v, perr := strconv.ParseUint(rid, 10, 32)
	if !ok || perr != nil {
		return id, fmt.Errorf("unexpected integrity label %s", label)
	}
	id.Integrity = uint32(v)
	groups, err := tok.GetTokenGroups()
	if err != nil {
		return id, err
	}
	for _, g := range groups.AllGroups() {
		switch {
		case g.Attributes&windows.SE_GROUP_USE_FOR_DENY_ONLY != 0:
			id.DenyOnly = append(id.DenyOnly, g.Sid.String())
		case g.Attributes&windows.SE_GROUP_ENABLED != 0:
			id.Enabled = append(id.Enabled, g.Sid.String())
		}
	}
	return id, nil
}

// tokenInfo reads one token information class into an 8-byte aligned buffer.
func tokenInfo(tok windows.Token, class uint32) ([]uint64, error) {
	var n uint32
	_ = windows.GetTokenInformation(tok, class, nil, 0, &n)
	if n == 0 {
		n = 8
	}
	buf := make([]uint64, (n+7)/8)
	if err := windows.GetTokenInformation(tok, class, (*byte)(unsafe.Pointer(&buf[0])), uint32(len(buf)*8), &n); err != nil {
		return nil, err
	}
	return buf, nil
}
