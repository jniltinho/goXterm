// Package fsentry is the shared directory-entry type for the file browser (local, SFTP and FTP).
package fsentry

// Entry is one directory entry. Never holds file contents.
type Entry struct {
	Name    string `json:"name"`
	Dir     bool   `json:"dir"`
	Size    int64  `json:"size"`
	Mode    string `json:"mode"`
	ModTime int64  `json:"modTime"` // Unix seconds
}
