---
name: create-release
description: Create a rustXterm version tag and GitHub Release. Use when the user asks to release, tag, publish or cut a version. Releases are allowed only when CI on main is green for the commit being tagged.
---

# Releasing rustXterm

Releases are the **Go + Wails app** (the repository root; the Rust app is archived in `archive/rust/`). Binaries are built only by
`.github/workflows/release-go.yml` on `v*.*.*` tags; the Go versions continue the Rust numbering
(Rust ended at v0.8.0, Go starts at v0.9.0). The Rust release workflow (`release.yml`) is disabled
is archived with it (`archive/rust/release.yml`). Checks run in `ci.yml` on `main` (Go checks and guard, lab,
cross-builds).

## Release rule

**Only release a commit whose `ci` run on `main` is green.** `release-go.yml` refuses to publish when
the tagged commit has no successful `ci` run, but check before tagging.

## Before you start

```bash
gh auth status                      # repo scope
git status                          # must be clean
git fetch origin --tags && git checkout main && git pull --ff-only
LAST=$(git describe --tags --abbrev=0 2>/dev/null || echo none)
echo "Last tag: $LAST"
```

## Versioning

Semantic versions with a `v` prefix: `v0.9.0`, `v0.9.1` (fixes), `v0.10.0` (features), pre-releases
`v0.10.0-rc.1` (published as prerelease). The tag is the version: `make` reads it with
`git describe --exact-match --match 'v*'` and the workflow passes it to `make dist-all`; nothing to
bump in a file.

```bash
NEXT=v0.9.1   # choose per the changes
```

## Process

### 1. Review changes since the last release

```bash
git log $LAST..HEAD --oneline
```

### 2. Require green CI on the commit

```bash
SHA=$(git rev-parse HEAD)
gh run list --workflow ci.yml --commit "$SHA" --json status,conclusion -q '.[0]'
# wait until {"conclusion":"success","status":"completed"}; never tag a red or running commit
```

### 3. Tag and push (triggers release-go.yml)

```bash
git tag -a $NEXT -m "Release $NEXT"
git push origin $NEXT
```

`release-go.yml` then verifies the green CI run, runs `make dist-all` in Docker and creates the GitHub
Release with `rustxterm-go-X.Y.Z-linux-amd64`, `rustxterm-go-X.Y.Z-x86_64.AppImage`,
`rustxterm-go-X.Y.Z-windows-amd64.exe`, `rustxterm-go-X.Y.Z_macos_universal.zip`, the per-arch
`darwin-amd64`/`darwin-arm64` binaries and `SHA256SUMS`.

### 4. Monitor

```bash
gh run list --workflow release-go.yml --limit 3
gh release view $NEXT          # wait until the assets appear
```

### 5. Write the release notes

Categorize commits in this exact order; omit empty sections:

| Section | Content |
|---|---|
| **✨ New Features** | new user-visible functionality (protocols, screens, CLI/MCP commands) |
| **🔧 Improvements** | fixes, performance, refactors, CI/build |
| **🔒 Security** | security-relevant changes (trust, crypto, agent boundary) |
| **🧹 Cleanup** | dead code, dependency cleanup |
| **📚 Documentation** | README, docs, diagrams, guides |

```bash
cat > /tmp/release-notes-$NEXT.md << 'NOTES'
# Release vX.Y.Z

## ✨ New Features

- ...

## 📦 Downloads

- `rustxterm-go-X.Y.Z-linux-amd64` (GTK 4 + WebKitGTK 6.0: Ubuntu 24.04+, Fedora 39+, Debian 13+)
- `rustxterm-go-X.Y.Z-x86_64.AppImage`
- `rustxterm-go-X.Y.Z-windows-amd64.exe` (WebView2)
- `rustxterm-go-X.Y.Z_macos_universal.zip` (unsigned: `xattr -dr com.apple.quarantine "rustXterm (Go).app"`)
- `SHA256SUMS`

**Full Changelog**: https://github.com/jniltinho/goXterm/compare/vPREV...vNEXT
NOTES
gh release edit $NEXT --title "$NEXT" --notes-file /tmp/release-notes-$NEXT.md
```

### 6. Verify

```bash
gh release view $NEXT
```

- Title is `$NEXT`, sections present, Full Changelog range correct.
- Assets attached; `SHA256SUMS` matches (`sha256sum -c` after download).
