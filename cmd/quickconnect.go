package cmd

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"unicode"
)

// schemePorts are the quick-connect schemes (Rust Protocol::ALL, local excluded) and default ports.
var schemePorts = map[string]int{"ssh": 22, "telnet": 23, "sftp": 22, "ftp": 21, "ftps": 990, "rdp": 3389, "vnc": 5900, "serial": 0, "wsl": 0}

// invalid is a parse error with the Rust rxt_core::Error::Invalid text; it is a usage error (2).
func invalid(format string, a ...any) error { return usage("invalid input: "+format, a...) }

// parseSessionURL is the quick-connect parser (Rust rxt_core::quick_connect::parse): `[user@]host[:port]`,
// `scheme://[user@]host[:port]`, `serial://<device>[?baud=&data=&parity=&stop=&flow=]` or
// `wsl://[distribution]`. It returns an unsaved session document (id 0).
func parseSessionURL(input string) (map[string]any, error) {
	input = strings.TrimSpace(input)
	protocol, rest := "ssh", input
	if scheme, r, ok := strings.Cut(input, "://"); ok {
		protocol, rest = strings.ToLower(scheme), r
		if _, known := schemePorts[protocol]; !known {
			return nil, invalid("unknown protocol '%s'", scheme)
		}
	}
	switch protocol {
	case "serial":
		return parseSerialURL(rest)
	case "wsl":
		dist := strings.TrimSpace(strings.TrimRight(rest, "/"))
		if strings.ContainsFunc(dist, unicode.IsSpace) {
			return nil, invalid("invalid distribution '%s'", dist)
		}
		s := map[string]any{"id": 0, "name": "WSL", "protocol": "wsl", "host": "", "port": 0, "wsl": map[string]any{}}
		if dist != "" {
			s["name"], s["wsl"] = dist, map[string]any{"distribution": dist}
		}
		return s, nil
	}
	rest = strings.TrimRight(rest, "/")
	var user *string
	hostport := rest
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		if at == 0 {
			return nil, invalid("empty user name")
		}
		u := rest[:at]
		user, hostport = &u, rest[at+1:]
	}
	host, port, err := splitHostPort(hostport)
	if err != nil {
		return nil, err
	}
	if host == "" {
		return nil, invalid("missing host")
	}
	if strings.ContainsFunc(host, func(r rune) bool { return unicode.IsSpace(r) || r == '/' }) {
		return nil, invalid("invalid host '%s'", host)
	}
	if port == 0 {
		port = schemePorts[protocol]
	}
	s := map[string]any{"id": 0, "name": host, "protocol": protocol, "host": host, "port": port}
	if user != nil {
		s["user"], s["name"] = *user, *user+"@"+host
	}
	return s, nil
}

// splitHostPort splits host[:port] and [ipv6]:port; a bare IPv6 address is all host.
func splitHostPort(s string) (string, int, error) {
	if inner, ok := strings.CutPrefix(s, "["); ok {
		host, after, ok := strings.Cut(inner, "]")
		if !ok {
			return "", 0, invalid("unclosed '['")
		}
		if p, ok := strings.CutPrefix(after, ":"); ok {
			n, err := parsePort(p)
			return host, n, err
		}
		if after != "" {
			return "", 0, invalid("unexpected '%s'", after)
		}
		return host, 0, nil
	}
	i := strings.LastIndex(s, ":")
	if i < 0 || strings.Contains(s[:i], ":") {
		return s, 0, nil
	}
	n, err := parsePort(s[i+1:])
	return s[:i], n, err
}

func parsePort(p string) (int, error) {
	n, err := strconv.ParseUint(p, 10, 16)
	if err != nil || n == 0 {
		return 0, invalid("invalid port '%s'", p)
	}
	return int(n), nil
}

// parseSerialURL builds a serial session: a bare device name is taken under /dev (as typed on Windows).
func parseSerialURL(rest string) (map[string]any, error) {
	device, query, _ := strings.Cut(rest, "?")
	device = strings.TrimRight(device, "/")
	if device == "" || strings.ContainsFunc(device, unicode.IsSpace) {
		return nil, invalid("missing serial device")
	}
	if runtime.GOOS != "windows" && !strings.HasPrefix(device, "/") {
		device = "/dev/" + device
	}
	line := map[string]any{"baud": 115200, "data_bits": "8", "parity": "none", "stop_bits": "1", "flow": "none"}
	parity := map[string]string{"n": "none", "none": "none", "o": "odd", "odd": "odd", "e": "even", "even": "even", "m": "mark", "mark": "mark", "s": "space", "space": "space"}
	flow := map[string]string{"none": "none", "n": "none", "rtscts": "hardware", "hardware": "hardware", "hw": "hardware", "xonxoff": "software", "software": "software", "sw": "software"}
	for _, pair := range strings.Split(query, "&") {
		if pair == "" {
			continue
		}
		key, value, _ := strings.Cut(pair, "=")
		bad := invalid("invalid %s '%s'", key, value)
		switch key {
		case "baud":
			b, err := strconv.ParseUint(value, 10, 32)
			if err != nil || b == 0 {
				return nil, bad
			}
			line["baud"] = int(b)
		case "data":
			if len(value) != 1 || value < "5" || value > "8" {
				return nil, bad
			}
			line["data_bits"] = value
		case "parity":
			if line["parity"] = parity[strings.ToLower(value)]; line["parity"] == "" {
				return nil, bad
			}
		case "stop":
			if value != "1" && value != "2" {
				return nil, bad
			}
			line["stop_bits"] = value
		case "flow":
			k := strings.NewReplacer("-", "", "_", "", "/", "").Replace(strings.ToLower(value))
			if line["flow"] = flow[k]; line["flow"] == "" {
				return nil, bad
			}
		default:
			return nil, invalid("unknown serial parameter '%s'", key)
		}
	}
	name := device[strings.LastIndex(device, "/")+1:]
	return map[string]any{"id": 0, "name": fmt.Sprintf("%s @ %v", name, line["baud"]), "protocol": "serial", "host": "", "port": 0,
		"serial": map[string]any{"port": device, "line": line}}, nil
}
