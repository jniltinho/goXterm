// Package fsentry is the shared directory-entry type for the file browser (local, SFTP and FTP).
package fsentry

import "os"

// Entry is one directory entry. Never holds file contents.
type Entry struct {
	Name    string `json:"name"`
	Dir     bool   `json:"dir"`
	Size    int64  `json:"size"`
	Mode    string `json:"mode"`            // ls style, e.g. "drwxr-xr-x"
	ModTime int64  `json:"modTime"`         // Unix seconds
	Owner   string `json:"owner,omitempty"` // user name (or uid) where the protocol has it
	Group   string `json:"group,omitempty"`
	Link    bool   `json:"link,omitempty"` // a symbolic link
}

// LsMode renders a file mode like `ls -l` ("drwxr-xr-x", "lrwxrwxrwx"); Go's own String uses other
// letters for links and devices ("Lrwx…", "Dcrw…").
func LsMode(m os.FileMode) string {
	t := byte('-')
	switch {
	case m&os.ModeDir != 0:
		t = 'd'
	case m&os.ModeSymlink != 0:
		t = 'l'
	case m&os.ModeNamedPipe != 0:
		t = 'p'
	case m&os.ModeSocket != 0:
		t = 's'
	case m&os.ModeCharDevice != 0:
		t = 'c'
	case m&os.ModeDevice != 0:
		t = 'b'
	}
	b := []byte{t}
	const rwx = "rwxrwxrwx"
	for i := 0; i < 9; i++ {
		if m&(1<<uint(8-i)) != 0 {
			b = append(b, rwx[i])
		} else {
			b = append(b, '-')
		}
	}
	return string(b)
}
