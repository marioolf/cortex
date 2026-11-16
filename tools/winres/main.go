// Command winres writes the Windows resources of the Cortex executable (icon,
// version information and application manifest) as COFF object files that the Go
// linker picks up by name: cmd/cortex/rsrc_windows_amd64.syso and
// rsrc_windows_arm64.syso. It uses only the standard library, so no resource
// compiler or third-party tool is needed.
//
//	go run ./tools/winres -ico winres/cortex.ico -version 1.2.3 -out-dir cmd/cortex
//
// The output is deterministic (no timestamps): the same inputs give the same
// bytes. The committed files are the ones for version 0.0.0 (development
// builds); scripts/release.sh regenerates them with the release version, and CI
// checks that the committed ones match `-version 0.0.0`.
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

const (
	rtIcon      = 3
	rtGroupIcon = 14
	rtVersion   = 16
	rtManifest  = 24

	langEnUS = 0x0409

	machineAMD64 = 0x8664
	machineARM64 = 0xAA64
	relAMD64     = 3 // IMAGE_REL_AMD64_ADDR32NB
	relARM64     = 2 // IMAGE_REL_ARM64_ADDR32NB
)

type target struct {
	file    string
	machine uint16
	reloc   uint16
}

var targets = []target{
	{"rsrc_windows_amd64.syso", machineAMD64, relAMD64},
	{"rsrc_windows_arm64.syso", machineARM64, relARM64},
}

func main() {
	ico := flag.String("ico", "winres/cortex.ico", "source .ico file")
	version := flag.String("version", "0.0.0", "version, such as 1.2.3 or v1.2.3-rc1")
	outDir := flag.String("out-dir", "cmd/cortex", "directory that receives the .syso files")
	flag.Parse()
	if err := run(*ico, *version, *outDir); err != nil {
		fmt.Fprintln(os.Stderr, "winres:", err)
		os.Exit(1)
	}
}

func run(icoPath, version, outDir string) error {
	icoData, err := os.ReadFile(icoPath)
	if err != nil {
		return err
	}
	for _, t := range targets {
		obj, err := build(t, icoData, version)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(outDir, t.file), obj, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// parseVersion returns the version text without a leading "v" and its four
// numeric parts: "v1.2.3-rc1" gives "1.2.3-rc1" and [1 2 3 0].
func parseVersion(v string) (string, [4]uint16, error) {
	var nums [4]uint16
	v = strings.TrimPrefix(v, "v")
	core := v
	if i := strings.IndexAny(core, "-+"); i >= 0 {
		core = core[:i]
	}
	parts := strings.Split(core, ".")
	if core == "" || len(parts) > 4 {
		return "", nums, fmt.Errorf("invalid version %q", v)
	}
	for i, p := range parts {
		n, err := strconv.ParseUint(p, 10, 16)
		if err != nil {
			return "", nums, fmt.Errorf("invalid version %q: %v", v, err)
		}
		nums[i] = uint16(n)
	}
	return v, nums, nil
}

type icoImage struct {
	width, height, colors byte
	planes, bits          uint16
	data                  []byte
}

func parseICO(b []byte) ([]icoImage, error) {
	if len(b) < 6 || binary.LittleEndian.Uint16(b[0:]) != 0 || binary.LittleEndian.Uint16(b[2:]) != 1 {
		return nil, fmt.Errorf("not an .ico file")
	}
	n := int(binary.LittleEndian.Uint16(b[4:]))
	if n == 0 || len(b) < 6+16*n {
		return nil, fmt.Errorf("bad .ico directory")
	}
	var imgs []icoImage
	for i := 0; i < n; i++ {
		e := b[6+16*i:]
		size := binary.LittleEndian.Uint32(e[8:])
		off := binary.LittleEndian.Uint32(e[12:])
		if uint64(off)+uint64(size) > uint64(len(b)) {
			return nil, fmt.Errorf("bad .ico entry %d", i)
		}
		imgs = append(imgs, icoImage{
			width: e[0], height: e[1], colors: e[2],
			planes: binary.LittleEndian.Uint16(e[4:]), bits: binary.LittleEndian.Uint16(e[6:]),
			data: b[off : off+size],
		})
	}
	return imgs, nil
}

func u16(b *bytes.Buffer, vs ...uint16) {
	for _, v := range vs {
		binary.Write(b, binary.LittleEndian, v)
	}
}

func u32(b *bytes.Buffer, vs ...uint32) {
	for _, v := range vs {
		binary.Write(b, binary.LittleEndian, v)
	}
}

func groupIcon(imgs []icoImage) []byte {
	var b bytes.Buffer
	u16(&b, 0, 1, uint16(len(imgs)))
	for i, im := range imgs {
		b.Write([]byte{im.width, im.height, im.colors, 0})
		u16(&b, im.planes, im.bits)
		u32(&b, uint32(len(im.data)))
		u16(&b, uint16(i+1))
	}
	return b.Bytes()
}

// versionNode writes one VS_VERSIONINFO-style node: length, value length, type,
// UTF-16 key, padding, value and children. The length covers the node only,
// padding between siblings is added by the caller through align.
func versionNode(key string, valueLen uint16, text bool, value []byte, children ...[]byte) []byte {
	var b bytes.Buffer
	u16(&b, 0, valueLen)
	if text {
		u16(&b, 1)
	} else {
		u16(&b, 0)
	}
	for _, c := range utf16.Encode([]rune(key)) {
		u16(&b, c)
	}
	u16(&b, 0)
	align(&b)
	b.Write(value)
	for _, c := range children {
		align(&b)
		b.Write(c)
	}
	out := b.Bytes()
	binary.LittleEndian.PutUint16(out, uint16(len(out)))
	return out
}

func align(b *bytes.Buffer) {
	for b.Len()%4 != 0 {
		b.WriteByte(0)
	}
}

func utf16z(s string) []byte {
	var b bytes.Buffer
	for _, c := range utf16.Encode([]rune(s)) {
		u16(&b, c)
	}
	u16(&b, 0)
	return b.Bytes()
}

func versionInfo(text string, n [4]uint16) []byte {
	ms := uint32(n[0])<<16 | uint32(n[1])
	ls := uint32(n[2])<<16 | uint32(n[3])
	var ffi bytes.Buffer
	u32(&ffi, 0xFEEF04BD, 0x00010000, ms, ls, ms, ls, 0x3F, 0, 0x00040004, 1, 0, 0, 0)

	// Order as Explorer shows them; keys must stay sorted for determinism only.
	strs := [][2]string{
		{"CompanyName", "Cortex contributors"},
		{"FileDescription", "Cortex - local memory for your AI tools"},
		{"FileVersion", text},
		{"LegalCopyright", "MIT License"},
		{"OriginalFilename", "cortex.exe"},
		{"ProductName", "Cortex"},
		{"ProductVersion", text},
	}
	var kids [][]byte
	for _, kv := range strs {
		val := utf16z(kv[1])
		kids = append(kids, versionNode(kv[0], uint16(len(val)/2), true, val))
	}
	table := versionNode("040904B0", 0, true, nil, kids...)
	sfi := versionNode("StringFileInfo", 0, true, nil, table)
	var tr bytes.Buffer
	u16(&tr, langEnUS, 0x04B0)
	vfi := versionNode("VarFileInfo", 0, true, nil, versionNode("Translation", 4, false, tr.Bytes()))
	return versionNode("VS_VERSION_INFO", 52, false, ffi.Bytes(), sfi, vfi)
}

func manifest(n [4]uint16) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">
  <assemblyIdentity type="win32" name="Cortex.Cortex" version="` + fmt.Sprintf("%d.%d.%d.%d", n[0], n[1], n[2], n[3]) + `" processorArchitecture="*"/>
  <description>Cortex - local memory for your AI tools</description>
  <dependency>
    <dependentAssembly>
      <assemblyIdentity type="win32" name="Microsoft.Windows.Common-Controls" version="6.0.0.0" processorArchitecture="*" publicKeyToken="6595b64144ccf1df" language="*"/>
    </dependentAssembly>
  </dependency>
  <trustInfo xmlns="urn:schemas-microsoft-com:asm.v3">
    <security>
      <requestedPrivileges>
        <requestedExecutionLevel level="asInvoker" uiAccess="false"/>
      </requestedPrivileges>
    </security>
  </trustInfo>
  <compatibility xmlns="urn:schemas-microsoft-com:compatibility.v1">
    <application>
      <supportedOS Id="{8e0f7a12-bfb3-4fe8-b9a5-48fd50a15a9a}"/>
    </application>
  </compatibility>
  <application xmlns="urn:schemas-microsoft-com:asm.v3">
    <windowsSettings>
      <dpiAware xmlns="http://schemas.microsoft.com/SMI/2005/WindowsSettings">true/pm</dpiAware>
      <dpiAwareness xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">PerMonitorV2,PerMonitor</dpiAwareness>
      <activeCodePage xmlns="http://schemas.microsoft.com/SMI/2019/WindowsSettings">UTF-8</activeCodePage>
    </windowsSettings>
  </application>
</assembly>
`)
}

type resource struct {
	typ, id uint32
	data    []byte
}

// build returns the COFF object for one target.
func build(t target, icoData []byte, version string) ([]byte, error) {
	text, nums, err := parseVersion(version)
	if err != nil {
		return nil, err
	}
	imgs, err := parseICO(icoData)
	if err != nil {
		return nil, err
	}
	var res []resource
	for i, im := range imgs {
		res = append(res, resource{rtIcon, uint32(i + 1), im.data})
	}
	res = append(res,
		resource{rtGroupIcon, 1, groupIcon(imgs)},
		resource{rtVersion, 1, versionInfo(text, nums)},
		resource{rtManifest, 1, manifest(nums)})
	sort.SliceStable(res, func(i, j int) bool {
		if res[i].typ != res[j].typ {
			return res[i].typ < res[j].typ
		}
		return res[i].id < res[j].id
	})

	// Tree layout: root, one directory per type, one per (type, id) with a single
	// language entry, then the data entries, then the data.
	var types []uint32
	byType := map[uint32][]int{}
	for i, r := range res {
		if len(byType[r.typ]) == 0 {
			types = append(types, r.typ)
		}
		byType[r.typ] = append(byType[r.typ], i)
	}
	dirSize := func(n int) int { return 16 + 8*n }
	off := dirSize(len(types))
	typeOff := map[uint32]int{}
	for _, ty := range types {
		typeOff[ty] = off
		off += dirSize(len(byType[ty]))
	}
	nameOff := make([]int, len(res))
	for i := range res {
		nameOff[i] = off
		off += dirSize(1)
	}
	entryOff := make([]int, len(res))
	for i := range res {
		entryOff[i] = off
		off += 16
	}
	dataOff := make([]int, len(res))
	for i, r := range res {
		dataOff[i] = off
		off += (len(r.data) + 3) &^ 3
	}
	secSize := off

	var sec bytes.Buffer
	dir := func(n int) { u32(&sec, 0, 0); u16(&sec, 0, 0, 0, uint16(n)) }
	const sub = 0x80000000
	dir(len(types))
	for _, ty := range types {
		u32(&sec, ty, uint32(typeOff[ty])|sub)
	}
	for _, ty := range types {
		dir(len(byType[ty]))
		for _, i := range byType[ty] {
			u32(&sec, res[i].id, uint32(nameOff[i])|sub)
		}
	}
	for i := range res {
		dir(1)
		u32(&sec, langEnUS, uint32(entryOff[i]))
	}
	for i, r := range res {
		u32(&sec, uint32(dataOff[i]), uint32(len(r.data)), 0, 0)
	}
	for _, r := range res {
		sec.Write(r.data)
		align(&sec)
	}
	if sec.Len() != secSize {
		return nil, fmt.Errorf("internal error: section size %d, want %d", sec.Len(), secSize)
	}
	if len(res) > 0xFFFF {
		return nil, fmt.Errorf("too many resources")
	}

	const hdr, shdr = 20, 40
	rawOff := hdr + shdr
	relOff := rawOff + secSize
	symOff := relOff + 10*len(res)

	var b bytes.Buffer
	u16(&b, t.machine, 1)
	u32(&b, 0, uint32(symOff), 2)
	u16(&b, 0, 0)
	b.WriteString(".rsrc\x00\x00\x00")
	u32(&b, 0, 0, uint32(secSize), uint32(rawOff), uint32(relOff), 0)
	u16(&b, uint16(len(res)), 0)
	u32(&b, 0x40000040) // initialized data, readable
	b.Write(sec.Bytes())
	for i := range res {
		u32(&b, uint32(entryOff[i]), 0)
		u16(&b, t.reloc)
	}
	// Symbol table: the section symbol and its auxiliary record, then an empty
	// string table.
	b.WriteString(".rsrc\x00\x00\x00")
	u32(&b, 0)
	u16(&b, 1, 0)
	b.Write([]byte{3, 1}) // IMAGE_SYM_CLASS_STATIC, one aux record
	u32(&b, uint32(secSize))
	u16(&b, uint16(len(res)), 0)
	u32(&b, 0)
	u16(&b, 0)
	b.Write([]byte{0, 0, 0, 0})
	u32(&b, 4)
	return b.Bytes(), nil
}
