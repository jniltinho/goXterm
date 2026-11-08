// Package archtest enforces the module layering (the Go counterpart of `cargo xtask layers`): the
// core — protocols, store, control, importers — never depends on the GUI or Wails, protocol packages
// stay independent of each other, and only the desktop entry point reaches the GUI.
package archtest

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const mod = "rustxterm-go/"

// allowed lists, per package (path under the module), the module packages it may import. Packages
// under internal/rdpmit (the vendored go-rdp) import within rdpmit only. Anything not listed here is
// refused.
var allowed = map[string][]string{
	"main":                {"cmd"},
	"cmd":                 {"internal/control", "internal/gui", "internal/mobaini", "internal/store"},
	"internal/gui":        {"internal/*"}, // the GUI composes the core
	"internal/ftpconn":    {"internal/fsentry"},
	"internal/sftpconn":   {"internal/fsentry", "internal/sshconn"},
	"internal/rdpconn":    {"internal/rdpmit/*"},
	"internal/store":      {"internal/secret"},
	"internal/control":    {},
	"internal/fsentry":    {},
	"internal/localconn":  {},
	"internal/mobaini":    {},
	"internal/secret":     {},
	"internal/serialconn": {},
	"internal/sshconn":    {},
	"internal/telnetconn": {},
	"internal/tunnelconn": {},
	"internal/vncconn":    {},
	"internal/wslconn":    {},
	"internal/tlstrust":   {},
	"internal/archtest":   {},
}

// guiOnly are third-party imports reserved for the GUI layer.
var guiOnly = []string{"github.com/wailsapp/"}

func match(pattern, pkg string) bool {
	if strings.HasSuffix(pattern, "/*") {
		return strings.HasPrefix(pkg, strings.TrimSuffix(pattern, "*"))
	}
	return pattern == pkg
}

func TestLayers(t *testing.T) {
	root, _ := filepath.Abs("../..")
	imports := map[string]map[string]bool{} // package → imports
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() && (name == ".cache" || name == "frontend" || name == "dist" || name == "build" ||
			name == "archive" || name == "target" || name == "assets" || name == "docs" || name == "openspec" || name == "lab" || strings.HasPrefix(name, ".") && path != root) {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, filepath.Dir(path))
		pkg := filepath.ToSlash(rel)
		if pkg == "." {
			pkg = "main"
		}
		if imports[pkg] == nil {
			imports[pkg] = map[string]bool{}
		}
		for _, im := range f.Imports {
			imports[pkg][strings.Trim(im.Path.Value, `"`)] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	pkgs := make([]string, 0, len(imports))
	for p := range imports {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)
	for _, pkg := range pkgs {
		vendored := strings.HasPrefix(pkg, "internal/rdpmit/")
		rules, known := allowed[pkg]
		if !known && !vendored {
			t.Errorf("%s: new package — add its allowed imports to archtest (keep the core independent of the GUI)", pkg)
			continue
		}
		for im := range imports[pkg] {
			for _, g := range guiOnly {
				if strings.HasPrefix(im, g) && pkg != "internal/gui" {
					t.Errorf("%s imports %s: Wails/GUI code belongs only in internal/gui", pkg, im)
				}
			}
			if !strings.HasPrefix(im, mod) {
				continue
			}
			target := strings.TrimPrefix(im, mod)
			ok := vendored && strings.HasPrefix(target, "internal/rdpmit/")
			for _, r := range rules {
				ok = ok || match(r, target)
			}
			if !ok {
				t.Errorf("%s may not import %s (layering; see archtest.allowed)", pkg, target)
			}
		}
	}
}
