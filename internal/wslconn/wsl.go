// Package wslconn is the WSL session backend (Rust crates/platform/src/wsl.rs + platform-windows):
// option validation and the wsl.exe arguments (pure, tested on every platform), the distribution
// list, and — on Windows only — the launcher facts. The terminal itself is a localconn ConPTY.
package wslconn

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Options is the session's "wsl" object (Rust WslOptions); empty fields keep WSL's defaults.
type Options struct {
	Distribution    string `json:"distribution,omitempty"`     // "" = the default distribution
	User            string `json:"user,omitempty"`             // "" = the distribution's default user
	StartFolder     string `json:"start_folder,omitempty"`     // Linux folder, e.g. ~ or /srv/app
	StartupCommands string `json:"startup_commands,omitempty"` // lines typed once the shell is ready
}

// Label is the distribution for display ("Default" when unset).
func (o Options) Label() string {
	if o.Distribution == "" {
		return "Default"
	}
	return o.Distribution
}

// Distribution is one installed distribution.
type Distribution struct {
	Name    string `json:"name"`
	Default bool   `json:"default"`
	Version int    `json:"version"` // 1 or 2; 0 = unknown
}

// ErrUnavailable means WSL cannot run here (any OS but Windows, or no WSL service installed).
var ErrUnavailable = errors.New("WSL is only available on Windows with WSL installed")

// ValidateDistribution applies WSL's own registration rule.
func ValidateDistribution(name string) error {
	if name == "" || strings.IndexFunc(name, func(r rune) bool {
		return !(r < 128 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-'))
	}) >= 0 {
		return fmt.Errorf("invalid distribution name '%s'", name)
	}
	return nil
}

// ValidateUser accepts POSIX-style names (letters, digits, _ . -, not starting with a digit, - or .),
// with an optional trailing $.
func ValidateUser(name string) error {
	core := strings.TrimSuffix(name, "$")
	ok := core != "" && (isAlpha(core[0]) || core[0] == '_')
	for i := 0; ok && i < len(core); i++ {
		c := core[i]
		ok = isAlpha(c) || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '-'
	}
	if !ok {
		return fmt.Errorf("invalid user name '%s'", name)
	}
	return nil
}

func isAlpha(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

// controlFree refuses C0 controls (CR/LF included) and DEL: a line editor would act on them.
func controlFree(s, what string) error {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return fmt.Errorf("control character in %s", what)
		}
	}
	return nil
}

// cdFallback is the start-folder line for a WSL without --cd (the tilde stays unquoted).
func cdFallback(folder string) (string, error) {
	if strings.Contains(folder, "'") {
		return "", errors.New("start folder containing ' needs a newer WSL with --cd")
	}
	switch {
	case folder == "~":
		return "cd", nil
	case strings.HasPrefix(folder, "~/"):
		return "cd ~/'" + folder[2:] + "'", nil
	}
	return "cd '" + folder + "'", nil
}

// LaunchArgs builds the wsl.exe arguments and the lines to type once the shell is ready. hasCD says
// whether the installed WSL supports --cd. Nothing follows "--", so the user's login shell starts.
func LaunchArgs(o Options, hasCD bool) (args, startup []string, err error) {
	if o.Distribution != "" {
		if err := ValidateDistribution(o.Distribution); err != nil {
			return nil, nil, err
		}
		args = append(args, "-d", o.Distribution)
	}
	if o.User != "" {
		if err := ValidateUser(o.User); err != nil {
			return nil, nil, err
		}
		args = append(args, "-u", o.User)
	}
	// no start folder: the Linux home, not the Windows folder wsl.exe inherits (/mnt/c/Users/<name>)
	folder := o.StartFolder
	if folder == "" {
		folder = "~"
	}
	if err := controlFree(folder, "start folder"); err != nil {
		return nil, nil, err
	}
	if hasCD {
		args = append(args, "--cd", folder)
	} else {
		line, err := cdFallback(folder)
		if err != nil {
			return nil, nil, err
		}
		startup = append(startup, line)
	}
	for _, line := range strings.Split(o.StartupCommands, "\n") {
		if err := controlFree(line, "startup command"); err != nil {
			return nil, nil, err
		}
		if line != "" {
			startup = append(startup, line)
		}
	}
	return args, startup, nil
}

// DecodeOutput reads wsl.exe output: UTF-16LE with a FF FE BOM or NUL bytes, else UTF-8.
func DecodeOutput(b []byte) string {
	body, wide := b, false
	if len(b) >= 2 && b[0] == 0xff && b[1] == 0xfe {
		body, wide = b[2:], true
	} else if strings.IndexByte(string(b), 0) >= 0 {
		wide = true
	}
	if !wide {
		return string(body)
	}
	u := make([]uint16, len(body)/2)
	for i := range u {
		u[i] = uint16(body[2*i]) | uint16(body[2*i+1])<<8
	}
	return string(utf16.Decode(u))
}

// ParseList reads `wsl.exe --list --verbose`: the header is skipped by position (it may be
// localized), * marks the default, the name is the first column and the version the last.
func ParseList(b []byte) []Distribution {
	var out []Distribution
	header := true
	for _, line := range strings.Split(DecodeOutput(b), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if header {
			header = false
			continue
		}
		t := strings.TrimLeft(line, " \t")
		def := strings.HasPrefix(t, "*")
		cols := strings.Fields(strings.TrimLeft(t, "*"))
		if len(cols) == 0 {
			continue
		}
		d := Distribution{Name: cols[0], Default: def}
		if len(cols) > 1 {
			d.Version, _ = strconv.Atoi(cols[len(cols)-1])
		}
		out = append(out, d)
	}
	return out
}
