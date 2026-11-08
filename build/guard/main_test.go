package main

import (
	"archive/zip"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// repo is a disposable git repository with base/ ignored, like the real one (cases ported from the
// archived Rust guard tests).
type repo struct {
	t   *testing.T
	dir string
}

func newRepo(t *testing.T) *repo {
	r := &repo{t, t.TempDir()}
	r.git("init", "-q", "-b", "main")
	r.write(".gitignore", "base/\ndocs/prints/\n")
	r.write("ATTRIBUTION.md", "| Path | Origin | License |\n|---|---|---|\n| `assets/ok/*.png` | ours | MIT |\n")
	r.commit("init")
	return r
}

func (r *repo) write(path, body string) {
	p := filepath.Join(r.dir, path)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *repo) git(args ...string) {
	cmd := exec.Command("git", append([]string{"-C", r.dir, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		r.t.Fatalf("git %v: %v %s", args, err, out)
	}
}

func (r *repo) commit(msg string) {
	r.git("add", "-A")
	r.git("commit", "-q", "--allow-empty", "-m", msg)
}

func (r *repo) check(ci bool, artifacts ...string) []string {
	rep, err := Check(r.dir, ci, artifacts)
	if err != nil {
		r.t.Fatal(err)
	}
	var out []string
	for p := range rep.Problems {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func has(ps []string, needle string) bool {
	for _, p := range ps {
		if strings.Contains(p, needle) {
			return true
		}
	}
	return false
}

func homePath() string { return "/" + "home" + "/mallory/.ssh/id_ed25519" }

func TestCleanRepoPasses(t *testing.T) {
	r := newRepo(t)
	r.write("assets/ok/a.png", "png")
	r.write("README", "see /"+"home/user/ and /work/x")
	r.commit("ok")
	if p := r.check(false); len(p) != 0 {
		t.Fatal(p)
	}
}

func TestCommittedBasePathFailsEvenAfterRemoval(t *testing.T) {
	r := newRepo(t)
	r.write("base/notes/x.txt", "note")
	r.git("add", "-f", "base/notes/x.txt")
	r.commit("oops")
	r.git("rm", "-q", "--cached", "base/notes/x.txt")
	r.commit("remove")
	p := r.check(true)
	if !has(p, "history: a commit touches local-only path base/notes/x.txt") || has(p, "under a local-only directory") {
		t.Fatal(p)
	}
}

func TestHomePathOnlyInHistoryFails(t *testing.T) {
	r := newRepo(t)
	r.write("doc.txt", homePath())
	r.commit("leak")
	r.write("doc.txt", "clean")
	r.commit("fix")
	p := r.check(true)
	if !has(p, "(doc.txt): absolute home path") || has(p, "file doc.txt") {
		t.Fatal(p)
	}
}

func TestRenamedForbiddenCopyFailsLocallyNotInCI(t *testing.T) {
	r := newRepo(t)
	r.write("base/bin/secret.dll", "synthetic forbidden bytes")
	r.write("base/icons/i.png", "icon bytes")
	r.write("assets/ok/renamed.png", "synthetic forbidden bytes")
	r.write("assets/ok/icon.png", "icon bytes")
	r.commit("copy")
	p := r.check(false)
	if !has(p, "file assets/ok/renamed.png: identical to forbidden file base/bin/secret.dll") || has(p, "icon.png") {
		t.Fatal(p)
	}
	if p := r.check(true); len(p) != 0 {
		t.Fatal(p)
	}
}

func TestArtifactMembersAreScanned(t *testing.T) {
	r := newRepo(t)
	r.write("base/x.bin", "forbidden member")
	r.write(".gitignore", "base/\n*.zip\n")
	r.commit("ignore")
	zp := filepath.Join(r.dir, "dist.zip")
	f, _ := os.Create(zp)
	z := zip.NewWriter(f)
	w, _ := z.Create("app/renamed.exe")
	_, _ = w.Write([]byte("forbidden member"))
	w, _ = z.Create("app/README.txt")
	_, _ = w.Write([]byte(homePath()))
	_ = z.Close()
	_ = f.Close()
	p := r.check(false, zp)
	if !has(p, "member app/renamed.exe: identical to forbidden file base/x.bin") || !has(p, "member app/README.txt: absolute home path") || len(p) != 2 {
		t.Fatal(p)
	}
}

func TestMissingAttributionRowFails(t *testing.T) {
	r := newRepo(t)
	r.write("assets/new/logo.svg", "<svg/>")
	r.write("internal/x/testdata/sample.txt", "data")
	p := r.check(true)
	if !has(p, "file assets/new/logo.svg: no ATTRIBUTION.md row") || !has(p, "file internal/x/testdata/sample.txt: no ATTRIBUTION.md row") {
		t.Fatal(p)
	}
	r.write("ATTRIBUTION.md", "| Path | Origin | License |\n|---|---|---|\n| `assets/**/*.svg` | ours | MIT |\n| `internal/*/testdata/**` | ours | MIT |\n")
	if p := r.check(true); len(p) != 0 {
		t.Fatal(p)
	}
}

func TestUnlistedFontFileFails(t *testing.T) {
	r := newRepo(t)
	r.write("assets/fonts/demo/Demo-Regular.ttf", "font")
	r.write("assets/fonts/demo/OFL.txt", "license")
	r.write("ATTRIBUTION.md", "| Path | Origin | License |\n|---|---|---|\n| `assets/fonts/demo/Demo-Regular.ttf` | demo | OFL-1.1 |\n")
	if p := r.check(true); len(p) != 1 || p[0] != "file assets/fonts/demo/OFL.txt: no ATTRIBUTION.md row" {
		t.Fatal(p)
	}
}

func TestGlobs(t *testing.T) {
	for _, c := range []struct {
		p, s string
		ok   bool
	}{
		{"crates/*/tests/snapshots/**", "crates/ui/tests/snapshots/a/b.png", true},
		{"a/**/c.png", "a/c.png", true},
		{"a/*.png", "a/x.png", true},
		{"a/*.png", "a/b/x.png", false},
		{"a/x.png", "a/x.pngx", false},
	} {
		if globMatch(c.p, c.s) != c.ok {
			t.Errorf("%s ~ %s", c.p, c.s)
		}
	}
}
