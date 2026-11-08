// Command guard is the publication guard (design D12; Go port of the archived `cargo xtask guard`):
// no commit touches the local-only folders, no file in the tree, in history or inside a release
// artifact is a copy of a file under base/ (by SHA-256) or holds a real absolute home path, and every
// icon, image, font and test fixture has an ATTRIBUTION.md row.
//
//	go run ./build/guard [--ci] [--artifact PATH]...
//
// --ci skips the base/ digest check (CI has no base/). Run it before every push (.githooks/pre-push).
package main

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var localOnly = []string{"base/", "docs/prints/"}

// Placeholder homes plus the fictional lab accounts (lab/README.md); real user homes still fail.
var homeAllowlist = []string{"/home/user/", "/home/builder/", "/Users/user/", "/home/alice/", "/home/bob/", "/home/carol/", "/home/tester/"}

var assetExts = map[string]bool{"png": true, "jpg": true, "jpeg": true, "gif": true, "bmp": true, "ico": true,
	"svg": true, "ttf": true, "otf": true, "woff": true, "woff2": true}

var homeRe = regexp.MustCompile(`/(?:home/[a-z_][a-z0-9_-]*|Users/[^/\x00-\x20]+)/`)

// Report collects problems (deduplicated) and informational notes.
type Report struct {
	Problems map[string]bool
	Notes    []string
}

func (r *Report) add(format string, a ...any) { r.Problems[fmt.Sprintf(format, a...)] = true }

type scanner struct{ forbidden map[[32]byte]string }

func (s scanner) scan(label string, b []byte, r *Report) {
	if src, ok := s.forbidden[sha256.Sum256(b)]; ok {
		r.add("%s: identical to forbidden file %s", label, src)
	}
	for _, m := range homeRe.FindAll(b, -1) {
		allowed := false
		for _, a := range homeAllowlist {
			if string(m) == a {
				allowed = true
				break
			}
		}
		if !allowed {
			r.add("%s: absolute home path %s", label, m)
			return
		}
	}
}

// Check runs every check for the repository at root.
func Check(root string, ci bool, artifacts []string) (*Report, error) {
	r := &Report{Problems: map[string]bool{}}
	forbidden := map[[32]byte]string{}
	if !ci {
		var err error
		if forbidden, err = forbiddenDigests(filepath.Join(root, "base")); err != nil {
			return nil, err
		}
	}
	switch {
	case ci:
		r.Notes = append(r.Notes, "CI mode: digest check skipped")
	case len(forbidden) == 0:
		r.Notes = append(r.Notes, "base/ absent or empty: digest check skipped")
	default:
		r.Notes = append(r.Notes, fmt.Sprintf("digest check against %d files under base/", len(forbidden)))
	}
	sc := scanner{forbidden}

	// (a) local-only paths in any commit.
	log, err := git(root, "log", "--all", "-m", "--no-renames", "--name-only", "--format=")
	if err != nil {
		return nil, err
	}
	for _, p := range strings.Split(string(log), "\n") {
		if isLocalOnly(p) {
			r.add("history: a commit touches local-only path %s", p)
		}
	}

	// Tracked plus untracked-but-not-ignored files.
	listed, err := git(root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	files := map[string]bool{}
	for _, f := range bytes.Split(listed, []byte{0}) {
		if len(f) > 0 {
			files[string(f)] = true
		}
	}
	for f := range files {
		if isLocalOnly(f) {
			r.add("file %s: under a local-only directory", f)
		}
		p := filepath.Join(root, f)
		if fi, err := os.Lstat(p); err == nil && fi.Mode().IsRegular() {
			b, err := os.ReadFile(p)
			if err != nil {
				return nil, err
			}
			sc.scan("file "+f, b, r)
		}
	}

	// (b) every historical blob.
	blobs, err := historyBlobs(root, func(oid, path string, b []byte) { sc.scan(fmt.Sprintf("history blob %s (%s)", oid, path), b, r) })
	if err != nil {
		return nil, err
	}
	r.Notes = append(r.Notes, fmt.Sprintf("scanned %d files and %d historical blobs", len(files), blobs))

	for _, a := range artifacts {
		n, err := scanArtifact(a, func(member string, b []byte) { sc.scan(fmt.Sprintf("artifact %s member %s", a, member), b, r) })
		if err != nil {
			return nil, err
		}
		r.Notes = append(r.Notes, fmt.Sprintf("scanned %d members of %s", n, a))
	}

	// (c) provenance of icons, images, fonts and fixtures.
	md, _ := os.ReadFile(filepath.Join(root, "ATTRIBUTION.md"))
	rows := attributionPaths(string(md))
	for f := range files {
		if !needsAttribution(f) {
			continue
		}
		ok := false
		for _, row := range rows {
			if globMatch(row, f) {
				ok = true
				break
			}
		}
		if !ok {
			r.add("file %s: no ATTRIBUTION.md row", f)
		}
	}
	return r, nil
}

func isLocalOnly(p string) bool {
	for _, l := range localOnly {
		if strings.HasPrefix(p, l) {
			return true
		}
	}
	return false
}

func git(root string, args ...string) ([]byte, error) {
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("git %s: %s", strings.Join(args, " "), ee.Stderr)
		}
		return nil, err
	}
	return out, nil
}

// forbiddenDigests: sha256 of every non-empty file under base/ except base/icons/.
func forbiddenDigests(base string) (map[[32]byte]string, error) {
	out := map[[32]byte]string{}
	if fi, err := os.Stat(base); err != nil || !fi.IsDir() {
		return out, nil
	}
	if real, err := filepath.EvalSymlinks(base); err == nil {
		base = real // WalkDir does not descend into a symlinked root
	}
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(base, p)
		if d.IsDir() {
			if rel == "icons" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if len(b) > 0 {
			out[sha256.Sum256(b)] = "base/" + filepath.ToSlash(rel)
		}
		return nil
	})
	return out, err
}

// historyBlobs feeds every blob reachable from any ref to f and returns the blob count.
func historyBlobs(root string, f func(oid, path string, b []byte)) (int, error) {
	list, err := git(root, "rev-list", "--objects", "--all")
	if err != nil {
		return 0, err
	}
	names := map[string]string{}
	var ids bytes.Buffer
	for _, l := range strings.Split(string(list), "\n") {
		if l == "" {
			continue
		}
		oid, path, _ := strings.Cut(l, " ")
		names[oid] = path
		ids.WriteString(oid + "\n")
	}
	cmd := exec.Command("git", "-C", root, "cat-file", "--batch")
	cmd.Stdin = &ids
	out, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	rd := bufio.NewReaderSize(out, 1<<20)
	count := 0
	for {
		header, err := rd.ReadString('\n')
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, err
		}
		parts := strings.Fields(header)
		if len(parts) < 3 {
			continue // "<oid> missing"
		}
		size, err := strconv.Atoi(parts[2])
		if err != nil {
			return 0, err
		}
		body := make([]byte, size+1)
		if _, err := io.ReadFull(rd, body); err != nil {
			return 0, err
		}
		if parts[1] == "blob" {
			count++
			p := names[parts[0]]
			if p == "" {
				p = "?"
			}
			f(parts[0], p, body[:size])
		}
	}
	return count, cmd.Wait()
}

// scanArtifact feeds each member of a tar(.gz), zip or AppImage (--appimage-extract) to f.
func scanArtifact(path string, f func(member string, b []byte)) (int, error) {
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		z, err := zip.OpenReader(path)
		if err != nil {
			return 0, err
		}
		defer z.Close()
		n := 0
		for _, m := range z.File {
			if m.FileInfo().IsDir() {
				continue
			}
			rc, err := m.Open()
			if err != nil {
				return n, err
			}
			b, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return n, err
			}
			f(m.Name, b)
			n++
		}
		return n, nil
	case strings.HasSuffix(lower, ".appimage"):
		dir, err := os.MkdirTemp("", "guard-appimage-")
		if err != nil {
			return 0, err
		}
		defer os.RemoveAll(dir)
		abs, _ := filepath.Abs(path)
		cmd := exec.Command(abs, "--appimage-extract")
		cmd.Dir = dir
		if err := cmd.Run(); err != nil {
			return 0, fmt.Errorf("extracting %s: %w", path, err)
		}
		n := 0
		err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || !d.Type().IsRegular() {
				return err
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(dir, p)
			f(filepath.ToSlash(rel), b)
			n++
			return nil
		})
		return n, err
	case strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz") || strings.HasSuffix(lower, ".tar"):
		fh, err := os.Open(path)
		if err != nil {
			return 0, err
		}
		defer fh.Close()
		var r io.Reader = fh
		if !strings.HasSuffix(lower, ".tar") {
			gz, err := gzip.NewReader(fh)
			if err != nil {
				return 0, err
			}
			r = gz
		}
		tr := tar.NewReader(r)
		n := 0
		for {
			h, err := tr.Next()
			if err == io.EOF {
				return n, nil
			}
			if err != nil {
				return n, err
			}
			if h.Typeflag != tar.TypeReg {
				continue
			}
			b, err := io.ReadAll(tr)
			if err != nil {
				return n, err
			}
			f(strings.TrimPrefix(h.Name, "./"), b)
			n++
		}
	default: // a plain file (a raw binary or the .exe)
		b, err := os.ReadFile(path)
		if err != nil {
			return 0, err
		}
		f(filepath.Base(path), b)
		return 1, nil
	}
}

// needsAttribution: icons, images, fonts, fixtures and everything under assets/fonts/.
func needsAttribution(path string) bool {
	if strings.HasPrefix(path, "assets/fonts/") {
		return true
	}
	if i := strings.LastIndexByte(path, '.'); i >= 0 && assetExts[strings.ToLower(path[i+1:])] {
		return true
	}
	segs := strings.Split(path, "/")
	for _, s := range segs[:len(segs)-1] {
		if s == "fixtures" || s == "testdata" {
			return true
		}
	}
	return false
}

// attributionPaths: the first column of every table row in ATTRIBUTION.md.
func attributionPaths(md string) []string {
	var out []string
	for _, l := range strings.Split(md, "\n") {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, "|") {
			continue
		}
		c := strings.Trim(strings.TrimSpace(strings.SplitN(l[1:], "|", 2)[0]), "`")
		if c != "" && c != "Path" && !strings.HasPrefix(c, "--") {
			out = append(out, c)
		}
	}
	return out
}

// globMatch: `*` matches within one segment, `**` any number of segments.
func globMatch(pattern, path string) bool {
	return segs(strings.Split(pattern, "/"), strings.Split(path, "/"))
}

func segs(p, s []string) bool {
	if len(p) == 0 {
		return len(s) == 0
	}
	if p[0] == "**" {
		for i := 0; i <= len(s); i++ {
			if segs(p[1:], s[i:]) {
				return true
			}
		}
		return false
	}
	return len(s) > 0 && wild(p[0], s[0]) && segs(p[1:], s[1:])
}

func wild(p, s string) bool {
	if p == "" {
		return s == ""
	}
	if p[0] == '*' {
		for i := 0; i <= len(s); i++ {
			if wild(p[1:], s[i:]) {
				return true
			}
		}
		return false
	}
	return s != "" && p[0] == s[0] && wild(p[1:], s[1:])
}

func main() {
	ci := false
	var artifacts []string
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--ci":
			ci = true
		case "--artifact":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "guard: --artifact needs a path")
				os.Exit(2)
			}
			i++
			artifacts = append(artifacts, args[i])
		default:
			fmt.Fprintf(os.Stderr, "guard: unknown argument %s\n", args[i])
			os.Exit(2)
		}
	}
	top, err := git(".", "rev-parse", "--show-toplevel")
	if err != nil {
		fmt.Fprintln(os.Stderr, "guard:", err)
		os.Exit(1)
	}
	r, err := Check(strings.TrimSpace(string(top)), ci, artifacts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "guard:", err)
		os.Exit(1)
	}
	for _, n := range r.Notes {
		fmt.Println("guard:", n)
	}
	if len(r.Problems) == 0 {
		fmt.Println("guard: ok")
		return
	}
	var ps []string
	for p := range r.Problems {
		ps = append(ps, p)
	}
	sort.Strings(ps)
	for _, p := range ps {
		fmt.Fprintln(os.Stderr, "guard:", p)
	}
	fmt.Fprintf(os.Stderr, "guard failed with %d problem(s)\n", len(ps))
	os.Exit(1)
}
