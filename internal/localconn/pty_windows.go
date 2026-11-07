//go:build windows

package localconn

import (
	"errors"
	"os"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// conPTY is a program attached to a Windows pseudo-console (ConPTY, Windows 10 1809+): the console
// renders to VT sequences on the output pipe and reads VT input from the input pipe.
type conPTY struct {
	hpc       windows.Handle
	in        *os.File   // our write end → console input
	out       *os.File   // our read end ← console output
	mu        sync.Mutex // guards proc, closed by the exit watcher
	proc      windows.Handle
	exit      uint32
	done      chan struct{}
	closeOnce sync.Once
}

func coord(cols, rows int) windows.Coord { return windows.Coord{X: int16(cols), Y: int16(rows)} }

func startPTY(path string, args []string, dir string, cols, rows int) (ptyProc, error) {
	var inR, inW, outR, outW windows.Handle
	if err := windows.CreatePipe(&inR, &inW, nil, 0); err != nil {
		return nil, err
	}
	if err := windows.CreatePipe(&outR, &outW, nil, 0); err != nil {
		windows.CloseHandle(inR)
		windows.CloseHandle(inW)
		return nil, err
	}
	var hpc windows.Handle
	err := windows.CreatePseudoConsole(coord(cols, rows), inR, outW, 0, &hpc)
	// The console holds its own duplicates of these two ends.
	windows.CloseHandle(inR)
	windows.CloseHandle(outW)
	if err != nil {
		windows.CloseHandle(inW)
		windows.CloseHandle(outR)
		return nil, err
	}
	fail := func(err error) (ptyProc, error) {
		windows.ClosePseudoConsole(hpc)
		windows.CloseHandle(inW)
		windows.CloseHandle(outR)
		return nil, err
	}
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return fail(err)
	}
	defer attrs.Delete()
	// lpValue is the HPCON value itself (not a pointer to it): reinterpret the handle's bits.
	hpcValue := *(*unsafe.Pointer)(unsafe.Pointer(&hpc))
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, hpcValue, unsafe.Sizeof(hpc)); err != nil {
		return fail(err)
	}
	si := &windows.StartupInfoEx{ProcThreadAttributeList: attrs.List()}
	si.Cb = uint32(unsafe.Sizeof(*si))
	// Null std handles: the child must use the pseudo-console, not whatever handles this GUI process has.
	si.Flags = windows.STARTF_USESTDHANDLES
	cmdline, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(append([]string{path}, args...)))
	if err != nil {
		return fail(err)
	}
	var cwd *uint16
	if dir != "" {
		if cwd, err = windows.UTF16PtrFromString(dir); err != nil {
			return fail(err)
		}
	}
	pi := &windows.ProcessInformation{}
	if err := windows.CreateProcess(nil, cmdline, nil, nil, false, windows.EXTENDED_STARTUPINFO_PRESENT, nil, cwd, &si.StartupInfo, pi); err != nil {
		return fail(err)
	}
	windows.CloseHandle(pi.Thread)
	c := &conPTY{
		hpc: hpc, proc: pi.Process, done: make(chan struct{}),
		in: os.NewFile(uintptr(inW), "conpty-in"), out: os.NewFile(uintptr(outR), "conpty-out"),
	}
	go func() {
		_, _ = windows.WaitForSingleObject(pi.Process, windows.INFINITE)
		_ = windows.GetExitCodeProcess(pi.Process, &c.exit)
		c.mu.Lock()
		windows.CloseHandle(c.proc)
		c.proc = 0
		c.mu.Unlock()
		close(c.done)
		c.closeConsole() // flushes the console and ends the output pipe, so Read returns EOF
	}()
	return c, nil
}

func (c *conPTY) closeConsole() { c.closeOnce.Do(func() { windows.ClosePseudoConsole(c.hpc) }) }

func (c *conPTY) Read(p []byte) (int, error)  { return c.out.Read(p) }
func (c *conPTY) Write(p []byte) (int, error) { return c.in.Write(p) }

func (c *conPTY) Resize(cols, rows int) error {
	return windows.ResizePseudoConsole(c.hpc, coord(cols, rows))
}

func (c *conPTY) Wait() (int, error) {
	<-c.done
	return int(c.exit), nil
}

func (c *conPTY) Kill() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.proc == 0 {
		return nil
	}
	err := windows.TerminateProcess(c.proc, 1)
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) { // already exiting
		return nil
	}
	return err
}

func (c *conPTY) Close() error {
	c.closeConsole()
	c.in.Close()
	return c.out.Close()
}
