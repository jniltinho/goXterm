// Command macapp packs the two macOS slices into a universal "goXterm.app" and zips it, on
// Linux and without Apple tools (lipo, iconutil): the fat Mach-O, Info.plist, PkgInfo and a PNG
// .icns are written in Go. The bundle is unsigned beyond the Go linker's ad-hoc slice signatures, so
// a downloaded copy needs: xattr -dr com.apple.quarantine "goXterm.app".
//
// Usage: go run ./build/macapp -version V -icon logo.png -out app.zip darwin-amd64 darwin-arm64
package main

import (
	"archive/zip"
	"bytes"
	"debug/macho"
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
)

const (
	appDir   = "goXterm.app/Contents/"
	execName = "goxterm"
	align    = 14 // 2^14: the page alignment lipo uses for arm64 and x86_64 slices
)

func main() {
	version := flag.String("version", "dev", "CFBundleShortVersionString")
	icon := flag.String("icon", "", "square PNG (256 px) for the app icon")
	out := flag.String("out", "", "output .zip")
	flag.Parse()
	if *out == "" || flag.NArg() < 2 {
		log.Fatal("usage: macapp -version V -icon logo.png -out app.zip <thin> <thin>...")
	}
	var slices [][]byte
	for _, p := range flag.Args() {
		b, err := os.ReadFile(p)
		if err != nil {
			log.Fatal(err)
		}
		slices = append(slices, b)
	}
	fat, err := universal(slices)
	if err != nil {
		log.Fatal(err)
	}
	var png []byte
	if *icon != "" {
		if png, err = os.ReadFile(*icon); err != nil {
			log.Fatal(err)
		}
	}
	if err := writeZip(*out, *version, fat, png); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s: universal %s (%d bytes)\n", *out, appDir+"MacOS/"+execName, len(fat))
}

// universal joins thin Mach-O executables into a fat binary (lipo -create) and checks the result
// reads back with every CPU, so a bad slice never ships.
func universal(slices [][]byte) ([]byte, error) {
	type arch struct{ cpu, sub, off, size uint32 }
	var archs []arch
	off := uint32(1 << align) // the first slice starts on its own page after the fat header
	for _, b := range slices {
		f, err := macho.NewFile(bytes.NewReader(b))
		if err != nil {
			return nil, fmt.Errorf("not a thin Mach-O: %w", err)
		}
		archs = append(archs, arch{uint32(f.Cpu), f.SubCpu, off, uint32(len(b))})
		off += (uint32(len(b)) + 1<<align - 1) &^ (1<<align - 1)
	}
	var buf bytes.Buffer
	w := func(v ...uint32) { _ = binary.Write(&buf, binary.BigEndian, v) }
	w(macho.MagicFat, uint32(len(archs)))
	for _, a := range archs {
		w(a.cpu, a.sub, a.off, a.size, align)
	}
	for i, b := range slices {
		buf.Write(make([]byte, int(archs[i].off)-buf.Len()))
		buf.Write(b)
	}
	fat := buf.Bytes()
	ff, err := macho.NewFatFile(bytes.NewReader(fat))
	if err != nil {
		return nil, fmt.Errorf("fat self-check: %w", err)
	}
	if len(ff.Arches) != len(slices) {
		return nil, fmt.Errorf("fat self-check: %d arches, want %d", len(ff.Arches), len(slices))
	}
	return fat, nil
}

// icns wraps a PNG as an Apple icon file: 'ic08' is the 256×256 PNG entry.
func icns(png []byte) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.BigEndian, []any{[4]byte{'i', 'c', 'n', 's'}, uint32(16 + len(png)),
		[4]byte{'i', 'c', '0', '8'}, uint32(8 + len(png))})
	b.Write(png)
	return b.Bytes()
}

func plist(version string, icon bool) string {
	iconKey := ""
	if icon {
		iconKey = "\t<key>CFBundleIconFile</key>\n\t<string>rustxterm</string>\n"
	}
	return strings.ReplaceAll(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key>
	<string>goXterm</string>
	<key>CFBundleDisplayName</key>
	<string>goXterm</string>
	<key>CFBundleIdentifier</key>
	<string>io.github.jniltinho.rustxterm.go</string>
	<key>CFBundleVersion</key>
	<string>__V__</string>
	<key>CFBundleShortVersionString</key>
	<string>__V__</string>
	<key>CFBundleExecutable</key>
	<string>`+execName+`</string>
`+iconKey+`	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleInfoDictionaryVersion</key>
	<string>6.0</string>
	<key>LSMinimumSystemVersion</key>
	<string>12.0</string>
	<key>NSLocalNetworkUsageDescription</key>
	<string>rustXterm connects to the SSH, SFTP, FTP, Telnet, RDP and VNC servers you open on your local network.</string>
	<key>NSHighResolutionCapable</key>
	<true/>
	<key>LSApplicationCategoryType</key>
	<string>public.app-category.developer-tools</string>
	<key>NSHumanReadableCopyright</key>
	<string>rustXterm is distributed under the MIT License.</string>
</dict>
</plist>
`, "__V__", version)
}

func writeZip(path, version string, fat, png []byte) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	z := zip.NewWriter(f)
	now := time.Now()
	add := func(name string, mode os.FileMode, data []byte) error {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: now}
		h.SetMode(mode)
		w, err := z.CreateHeader(h)
		if err == nil {
			_, err = w.Write(data)
		}
		return err
	}
	files := []struct {
		name string
		mode os.FileMode
		data []byte
	}{
		{appDir + "Info.plist", 0o644, []byte(plist(version, png != nil))},
		{appDir + "PkgInfo", 0o644, []byte("APPL????")},
		{appDir + "MacOS/" + execName, 0o755, fat},
	}
	if png != nil {
		files = append(files, struct {
			name string
			mode os.FileMode
			data []byte
		}{appDir + "Resources/rustxterm.icns", 0o644, icns(png)})
	}
	for _, e := range files {
		if err := add(e.name, e.mode, e.data); err != nil {
			f.Close()
			return err
		}
	}
	if err := z.Close(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
