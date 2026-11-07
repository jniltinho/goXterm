// Package mobaini is the MobaXterm.ini bookmark importer (Rust crates/store/src/moba_ini.rs):
// a read-only preview of the sessions in [Bookmarks] / [Bookmarks_N] sections, as session JSON
// documents for the store. Sections about passwords or credentials are never read; the source file
// is never modified.
//
// A bookmark line is `name=#<icon>#<type>%<field>%...#<terminal field>%...#...`; the type is the
// first %-field (0 SSH, 1 Telnet, 4 RDP, 5 VNC, 6 FTP, 7 SFTP, 8 Serial, 14 WSL). ; % # | are escaped
// as __PTVIRG__ __PERCENT__ __DIEZE__ __PIPE__. SubRep= holds the folder path (\-separated).
package mobaini

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Entry is an entry that was skipped or imported with a warning.
type Entry struct {
	Line   int    `json:"line"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// Item is one importable session: its folder path ("/"-separated, "" = root) and session JSON.
type Item struct {
	Folder  string         `json:"folder"`
	Session map[string]any `json:"session"`
}

// Report is the import preview; nothing is written until the caller applies it.
type Report struct {
	Sessions []Item   `json:"sessions"`
	Folders  []string `json:"folders"` // distinct non-empty folder paths, in file order
	Skipped  []Entry  `json:"skipped"`
	Warnings []Entry  `json:"warnings"`
}

// Field positions in the %-list that starts with the type (Rust constants); checkboxes are -1 on.
const (
	fType, fHost, fPort, fUser                                  = 0, 1, 2, 3
	sshX11, sshCompression, sshRemoteCommand                    = 5, 6, 7
	sshGwHost, sshGwPort, sshGwUser, sshKeyFile, sshGwKeyFile   = 8, 9, 10, 14, 15
	ftpStartupFolder, ftpFTPS, ftpImplicitTLS                   = 5, 6, 19
	serPort, serSpeed, serDataBits, serParity, serStop, serFlow = 1, 2, 3, 4, 5, 6
	termFont, termFontSize                                      = 0, 1
	wslDist, wslUser, wslGraphical, wslFolder, wslCommands      = 1, 2, 3, 4, 5
)

var defaultPort = map[string]int{"ssh": 22, "telnet": 23, "sftp": 22, "ftp": 21, "ftps": 990, "rdp": 3389, "vnc": 5900}

// ImportFile reads path (read-only); a file that is not UTF-8 is decoded as Latin-1.
func ImportFile(path string) (Report, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Report{}, err
	}
	text := string(b)
	if !utf8.Valid(b) {
		r := make([]rune, len(b))
		for i, c := range b {
			r[i] = rune(c)
		}
		text = string(r)
	}
	return Parse(text), nil
}

// Parse builds the preview from ini text.
func Parse(text string) Report {
	var rep Report
	in, folder := false, ""
	for i, raw := range strings.Split(strings.TrimPrefix(text, "\ufeff"), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			s := strings.ToLower(line[1 : len(line)-1])
			sensitive := strings.Contains(s, "pass") || strings.Contains(s, "cred")
			in = !sensitive && (s == "bookmarks" || strings.HasPrefix(s, "bookmarks_"))
			folder = ""
			continue
		}
		if !in || line == "" || strings.HasPrefix(line, ";") {
			continue
		}
		at := func(name, reason string) Entry { return Entry{Line: i + 1, Name: name, Reason: reason} }
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			rep.Skipped = append(rep.Skipped, at("", "line has no `=`"))
			continue
		}
		name := unescape(strings.TrimSpace(k))
		switch name {
		case "SubRep": // applies to the lines after it
			var parts []string
			for _, p := range strings.Split(strings.TrimSpace(v), `\`) {
				if p != "" {
					parts = append(parts, p)
				}
			}
			folder = strings.Join(parts, "/")
			if folder != "" && !contains(rep.Folders, folder) {
				rep.Folders = append(rep.Folders, folder)
			}
		case "ImgNum":
		default:
			s, warns, err := bookmark(name, strings.TrimSpace(v))
			if err != nil {
				rep.Skipped = append(rep.Skipped, at(name, err.Error()))
				continue
			}
			for _, w := range warns {
				rep.Warnings = append(rep.Warnings, at(name, w))
			}
			rep.Sessions = append(rep.Sessions, Item{Folder: folder, Session: s})
		}
	}
	return rep
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func unescape(s string) string {
	return strings.NewReplacer("__PTVIRG__", ";", "__PERCENT__", "%", "__DIEZE__", "#", "__PIPE__", "|").Replace(s)
}

func split(s string) []string {
	f := strings.Split(s, "%")
	for i := range f {
		f[i] = unescape(f[i])
	}
	return f
}

// getter returns field i trimmed, "" when absent.
func getter(f []string) func(int) string {
	return func(i int) string {
		if i < len(f) {
			return strings.TrimSpace(f[i])
		}
		return ""
	}
}

func bookmark(name, value string) (map[string]any, []string, error) {
	if !strings.HasPrefix(value, "#") {
		return nil, nil, fmt.Errorf("not a bookmark (missing `#<icon>#`)")
	}
	parts := strings.Split(value[1:], "#") // [icon, fields, terminal, ...]
	fieldStr := ""
	if len(parts) > 1 {
		fieldStr = parts[1]
	}
	fields := split(fieldStr)
	get := getter(fields)
	code, err := strconv.Atoi(get(fType))
	if err != nil {
		return nil, nil, fmt.Errorf("invalid session type `%s`", get(fType))
	}
	protocol := map[int]string{0: "ssh", 4: "rdp", 5: "vnc", 6: "ftp", 7: "sftp", 1: "telnet"}[code]
	switch code {
	case 8:
		return serial(name, get)
	case 14:
		return wsl(name, get)
	case 2:
		return nil, nil, fmt.Errorf("unsupported session type rsh (2)")
	case 11:
		return nil, nil, fmt.Errorf("unsupported session type browser (11)")
	}
	if protocol == "" {
		return nil, nil, fmt.Errorf("unsupported session type %d", code)
	}
	if len(fields) <= fPort {
		return nil, nil, fmt.Errorf("fewer fields than expected (%d < %d)", len(fields), fPort+1)
	}
	flag := func(i int) bool { return get(i) == "-1" }
	if protocol == "ftp" && flag(ftpFTPS) {
		protocol = "ftps"
	}
	host := get(fHost)
	if host == "" {
		return nil, nil, fmt.Errorf("empty host")
	}
	port := defaultPort[protocol]
	if p := get(fPort); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 65535 {
			return nil, nil, fmt.Errorf("invalid port `%s`", p)
		}
		port = n
	}
	s := map[string]any{"id": 0, "name": name, "protocol": protocol, "host": host, "port": port}
	if u := get(fUser); u != "" && protocol != "vnc" {
		s["user"] = u
	}
	switch protocol {
	case "ssh":
		ssh := map[string]any{"x11_forwarding": flag(sshX11), "compression": flag(sshCompression)}
		if c := get(sshRemoteCommand); c != "" {
			ssh["remote_command"] = c
		}
		if k := get(sshKeyFile); k != "" {
			ssh["key_file"] = k
		}
		hop := func(i, n int) string {
			p := strings.Split(get(i), "|")
			if n < len(p) {
				return strings.TrimSpace(p[n])
			}
			return ""
		}
		var jumps []map[string]any
		if g := get(sshGwHost); g != "" {
			for n := range strings.Split(g, "|") {
				h := hop(sshGwHost, n)
				if h == "" {
					continue
				}
				jp, err := strconv.Atoi(hop(sshGwPort, n))
				if err != nil {
					jp = 22
				}
				j := map[string]any{"host": h, "port": jp, "user": hop(sshGwUser, n)}
				if kf := hop(sshGwKeyFile, n); kf != "" {
					j["key_file"] = kf
				}
				jumps = append(jumps, j)
			}
		}
		if len(jumps) > 0 {
			ssh["jump_hosts"] = jumps
		}
		s["ssh"] = ssh
		if len(parts) > 2 {
			tget := getter(split(parts[2]))
			term := map[string]any{}
			if f := tget(termFont); f != "" && !strings.EqualFold(f, "MobaFont") {
				term["font_family"] = f
			}
			if v, err := strconv.ParseFloat(tget(termFontSize), 64); err == nil && v > 0 {
				term["font_size"] = v
			}
			if len(term) > 0 {
				s["terminal"] = term
			}
		}
	case "ftp", "ftps":
		ftp := map[string]any{"tls": "explicit"}
		if flag(ftpImplicitTLS) {
			ftp["tls"] = "implicit"
		}
		if f := get(ftpStartupFolder); f != "" {
			ftp["startup_folder"] = f
		}
		s["ftp"] = ftp
	}
	return s, nil, nil // passwords and telnet auto-login are never imported
}

func serial(name string, get func(int) string) (map[string]any, []string, error) {
	port := strings.Fields(get(serPort)) // the app may append a description after the port name
	if len(port) == 0 {
		return nil, nil, fmt.Errorf("empty serial port")
	}
	var warns []string
	line := map[string]any{"baud": 115200, "data_bits": "8", "parity": "none", "stop_bits": "1", "flow": "none"}
	setting := func(what string, i int, apply func(string) bool) {
		if v := get(i); v != "" && !apply(v) {
			warns = append(warns, fmt.Sprintf("unrecognized %s `%s`; using the default", what, v))
		}
	}
	setting("speed", serSpeed, func(v string) bool {
		b, err := strconv.Atoi(v)
		if err != nil || b <= 0 {
			return false
		}
		line["baud"] = b
		return true
	})
	setting("data bits", serDataBits, func(v string) bool {
		if v < "5" || v > "8" || len(v) != 1 {
			return false
		}
		line["data_bits"] = v
		return true
	})
	setting("parity", serParity, func(v string) bool {
		p := map[string]string{"n": "none", "none": "none", "o": "odd", "odd": "odd", "e": "even", "even": "even",
			"m": "mark", "mark": "mark", "s": "space", "space": "space"}[strings.ToLower(v)]
		if p == "" {
			return false
		}
		line["parity"] = p
		return true
	})
	setting("stop bits", serStop, func(v string) bool {
		if v != "1" && v != "2" {
			return false
		}
		line["stop_bits"] = v
		return true
	})
	setting("flow control", serFlow, func(v string) bool {
		k := strings.NewReplacer("-", "", "_", "", "/", "").Replace(strings.ToLower(v))
		f := map[string]string{"none": "none", "n": "none", "rtscts": "hardware", "hardware": "hardware", "hw": "hardware",
			"xonxoff": "software", "software": "software", "sw": "software"}[k]
		if f == "" {
			return false
		}
		line["flow"] = f
		return true
	})
	p := port[0]
	if len(p) > 3 && strings.EqualFold(p[:3], "COM") && strings.Trim(p[3:], "0123456789") == "" {
		warns = append(warns, fmt.Sprintf("Windows port name %s must be replaced with a Linux device path (e.g. /dev/ttyUSB0)", p))
	}
	s := map[string]any{"id": 0, "name": name, "protocol": "serial", "host": p, "port": 0,
		"serial": map[string]any{"port": p, "line": line}}
	return s, warns, nil
}

func wsl(name string, get func(int) string) (map[string]any, []string, error) {
	o := map[string]any{}
	if d := get(wslDist); d != "" && !strings.EqualFold(d, "Default") {
		o["distribution"] = d
	}
	for k, i := range map[string]int{"user": wslUser, "start_folder": wslFolder, "startup_commands": wslCommands} {
		if v := get(i); v != "" {
			o[k] = v
		}
	}
	var warns []string
	if env := get(wslGraphical); env != "" && !strings.EqualFold(env, "Interactive shell") {
		warns = append(warns, fmt.Sprintf("graphical environment `%s` is not supported; the interactive shell is used", env))
	}
	host, _ := o["distribution"].(string)
	return map[string]any{"id": 0, "name": name, "protocol": "wsl", "host": host, "port": 0, "wsl": o}, warns, nil
}
