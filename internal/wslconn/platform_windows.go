//go:build windows

package wslconn

import (
	"context"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows/registry"
)

const (
	lxss        = `Software\Microsoft\Windows\CurrentVersion\Lxss`
	wslService  = `SYSTEM\CurrentControlSet\Services\WslService` // Store WSL (has --cd)
	lxssManager = `SYSTEM\CurrentControlSet\Services\LxssManager`
	cdFloor     = 21286 // inbox WSL builds from here support --cd
)

// Exe is %SystemRoot%\System32\wsl.exe.
func Exe() string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return root + `\System32\wsl.exe`
}

func keyExists(root registry.Key, path string) bool {
	k, err := registry.OpenKey(root, path, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	k.Close()
	return true
}

// Available reports a registered WSL service (wsl.exe alone does not prove WSL is installed).
func Available() bool {
	return keyExists(registry.LOCAL_MACHINE, wslService) || keyExists(registry.LOCAL_MACHINE, lxssManager)
}

// SupportsCD: the Store WSL, or an inbox build from 21286.
func SupportsCD() bool {
	if keyExists(registry.LOCAL_MACHINE, wslService) {
		return true
	}
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	s, _, err := k.GetStringValue("CurrentBuildNumber")
	b, _ := strconv.Atoi(strings.TrimSpace(s))
	return err == nil && b >= cdFloor
}

// Distributions lists the Lxss registry entries (default first), else `wsl --list --verbose`.
func Distributions() ([]Distribution, error) {
	if !Available() {
		return nil, ErrUnavailable
	}
	if d, ok := registryDistributions(); ok {
		return d, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, Exe(), "--list", "--verbose")
	cmd.Env = append(os.Environ(), "WSL_UTF8=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return ParseList(out), nil
}

func registryDistributions() ([]Distribution, bool) {
	k, err := registry.OpenKey(registry.CURRENT_USER, lxss, registry.READ)
	if err != nil {
		return nil, err == registry.ErrNotExist
	}
	defer k.Close()
	def, _, _ := k.GetStringValue("DefaultDistribution")
	subs, err := k.ReadSubKeyNames(-1)
	if err != nil {
		return nil, false
	}
	var out []Distribution
	for _, sk := range subs {
		if !strings.HasPrefix(sk, "{") {
			continue
		}
		d, err := registry.OpenKey(k, sk, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		name, _, _ := d.GetStringValue("DistributionName")
		flags, _, ferr := d.GetIntegerValue("Flags")
		d.Close()
		if name == "" {
			continue
		}
		v := 0
		if ferr == nil { // bit 3 of Flags marks WSL 2 (Rust heuristic; --list --verbose is authoritative)
			v = 1
			if flags&0x8 != 0 {
				v = 2
			}
		}
		out = append(out, Distribution{Name: name, Default: strings.EqualFold(sk, def), Version: v})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Default != out[j].Default {
			return out[i].Default
		}
		return out[i].Name < out[j].Name
	})
	return out, true
}

// WorkDir is the Windows folder wsl.exe starts in: the profile, else the system drive root.
func WorkDir() string {
	if h := os.Getenv("USERPROFILE"); h != "" {
		if st, err := os.Stat(h); err == nil && st.IsDir() {
			return h
		}
	}
	if d := os.Getenv("SystemDrive"); d != "" {
		return d + `\`
	}
	return `C:\`
}
