# rustXterm (Go + Wails v3). ONE binary = desktop window + CLI + MCP server (Cobra): no subcommand
# opens the window, subcommands are the CLI/MCP. Everything builds in Docker (only docker + make).
# The binary is CGO: GTK 4 + WebKitGTK 6.0 (the Wails window) and SQLCipher (reads the Rust store).
# Builds always refresh dist/. Migration: openspec/changes/migrate-gui-to-go-wails.
NODE_IMAGE ?= node:24
IMAGE      ?= rustxterm-go-linux
VERSION    ?= $(or $(shell git describe --tags --match 'go-v*' 2>/dev/null | sed 's/^go-v//'),dev)
LDFLAGS    := -s -w -X rustxterm-go/cmd.version=$(VERSION)
BIN        := rustxterm-go
DOCKER     := docker run --rm --user $(shell id -u):$(shell id -g) -e HOME=/tmp
GO_ENV     := -e GOFLAGS=-buildvcs=false -e GOCACHE=/src/.cache/go-build -e GOMODCACHE=/src/.cache/mod -e CGO_ENABLED=1
RUN         = $(DOCKER) -v $(CURDIR):/src -w /src $(GO_ENV) $(IMAGE)

.PHONY: prints image tidy check layers test frontend build appimage windows macos dist-all clean

image:            ## build toolchain image (Go + GTK/WebKitGTK + SQLCipher)
	docker build -q -t $(IMAGE) -f build/linux.Dockerfile build

tidy: image       ## resolve modules and write go.sum
	$(RUN) go mod tidy

check: image      ## gofmt + go vet
	$(RUN) sh -c 'test -z "$$(gofmt -l cmd internal main.go)" || { gofmt -l cmd internal main.go; exit 1; }; go vet ./...'

layers: image     ## module layering (core never imports the GUI/Wails; protocol packages stay independent)
	$(RUN) go test ./internal/archtest

test: image       ## unit tests
	$(RUN) go test ./...

icons:            ## copy the bundled icon catalog (assets/icons/app) into frontend/public/icons
	mkdir -p frontend/public/icons && cp ../assets/icons/app/32/*.png frontend/public/icons/
	cp ../assets/icons/rustxterm-256.png frontend/public/icons/logo.png
	mkdir -p frontend/public/fonts && cp ../assets/fonts/*/*.ttf frontend/public/fonts/

frontend: icons   ## the Vue/Tailwind SPA (Vite Plus) into internal/gui/dist (embedded by the binary)
	$(DOCKER) -v $(CURDIR):/src -w /src/frontend $(NODE_IMAGE) sh -c 'npm ci --no-audit --no-fund && npm run build:desktop'

build: image frontend ## the single binary for Linux amd64 → dist/
	$(RUN) go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BIN)-$(VERSION)-linux-amd64 .

# AppImage holds the binary, its .desktop and icon; GTK 4 + WebKitGTK 6.0 and libsqlcipher come from
# the system (Ubuntu 24.04+, Fedora 39+, Debian 13+).
# Built as .new then renamed, so a running copy of the AppImage ("Text file busy") is replaced too.
# shortcut: GTK/WebKit/SQLCipher are not bundled; bundle them (linuxdeploy) for older distributions.
appimage: build   ## the Linux app as an x86_64 AppImage → dist/
	$(RUN) sh -c '\
	  app=.cache/AppDir; rm -rf $$app; mkdir -p $$app/usr/bin $$app/usr/share/icons/hicolor/256x256/apps; \
	  cp dist/$(BIN)-$(VERSION)-linux-amd64 $$app/usr/bin/$(BIN); \
	  cp build/linux/AppRun build/linux/$(BIN).desktop $$app/; \
	  cp build/linux/$(BIN).png $$app/$(BIN).png; cp build/linux/$(BIN).png $$app/usr/share/icons/hicolor/256x256/apps/$(BIN).png; \
	  desktop-file-validate $$app/$(BIN).desktop && \
	  ARCH=x86_64 appimagetool --no-appstream --runtime-file /usr/local/share/appimage-runtime-x86_64 \
	    $$app dist/$(BIN)-$(VERSION)-x86_64.AppImage.new && \
	  mv -f dist/$(BIN)-$(VERSION)-x86_64.AppImage.new dist/$(BIN)-$(VERSION)-x86_64.AppImage'

# With the pure-Go store, Windows and macOS cross-build CGO-free from Linux — no mingw/osxcross:
#   windows: WebView2 (CGO_ENABLED=0), -H windowsgui.
#   macos:   Wails purego backend (CGO_ENABLED=0, -tags purego); universal joins amd64+arm64.
windows: image frontend ## Windows amd64 (WebView2, CGO-free) → dist/
	$(RUN) sh -c 'CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -tags production -trimpath \
	  -ldflags "$(LDFLAGS) -H windowsgui" -o dist/$(BIN)-$(VERSION)-windows-amd64.exe .'

# shortcut: the universal app carries only the Go linker ad-hoc slice signatures; Developer ID
# signing + notarization is task 13.3.
macos: image frontend ## macOS amd64+arm64 + universal "rustXterm (Go).app" zip (purego, CGO-free) → dist/
	$(RUN) sh -c 'for a in amd64 arm64; do CGO_ENABLED=0 GOOS=darwin GOARCH=$$a go build -tags "production purego" \
	  -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BIN)-$(VERSION)-darwin-$$a . || exit 1; done'
	$(RUN) go run ./build/macapp -version $(VERSION) -icon frontend/public/icons/logo.png \
	  -out dist/$(BIN)-$(VERSION)_macos_universal.zip dist/$(BIN)-$(VERSION)-darwin-amd64 dist/$(BIN)-$(VERSION)-darwin-arm64
	@echo 'Unsigned universal app: xattr -dr com.apple.quarantine "rustXterm (Go).app"' 

dist-all: appimage windows macos ## Linux binary + AppImage, Windows .exe, macOS per-arch — all in dist/

# Screenshot sweep (13.1) on Linux into ../docs/prints/go-linux/<skin>/ (git-ignored). Needs the QA
# container `rxt-qa` (toolchain image on the lab network, with the QA store) and RXT_QA_PASS set.
prints: build    ## screenshots of every screen in Light and Dark → ../docs/prints/go-linux/
	@test -n "$$RXT_QA_PASS" || { echo "set RXT_QA_PASS (the QA store's throwaway master password)"; exit 1; }
	docker start rxt-qa >/dev/null
	for s in light dark; do docker exec -e RXT_QA_PASS rxt-qa sh /w/build/qa/prints.sh $$s /w/.cache/prints/$$s || exit 1; \
	  mkdir -p ../docs/prints/go-linux/$$s && cp .cache/prints/$$s/*.png ../docs/prints/go-linux/$$s/; done

clean:
	rm -rf dist .cache
