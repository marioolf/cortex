package main

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"unicode/utf16"
)

// TestWindowsResources cross-builds the Windows executables and checks that the
// linker embedded the icon, version information and manifest from the
// rsrc_windows_*.syso files. It runs on any host that has the Go toolchain.
func TestWindowsResources(t *testing.T) {
	if testing.Short() {
		t.Skip("cross-builds the Windows executables")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go tool in PATH")
	}
	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			exe := filepath.Join(t.TempDir(), "cortex.exe")
			cmd := exec.Command(goBin, "build", "-trimpath", "-ldflags", "-H windowsgui", "-o", exe, ".")
			cmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH="+arch, "CGO_ENABLED=0")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("build: %v\n%s", err, out)
			}
			f, err := pe.Open(exe)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			var rsrc *pe.Section
			for _, s := range f.Sections {
				if s.Name == ".rsrc" {
					rsrc = s
				}
			}
			if rsrc == nil {
				t.Fatal("no .rsrc section")
			}
			sec, err := rsrc.Data()
			if err != nil {
				t.Fatal(err)
			}
			rva := rsrc.VirtualAddress
			types := map[uint32][]byte{}
			count := map[uint32]int{}
			for _, e := range dirEntries(sec, 0) {
				for _, n := range dirEntries(sec, e.off) {
					for _, l := range dirEntries(sec, n.off) {
						dataRVA := binary.LittleEndian.Uint32(sec[l.off:])
						size := binary.LittleEndian.Uint32(sec[l.off+4:])
						start := dataRVA - rva
						count[e.id]++
						if _, ok := types[e.id]; !ok {
							types[e.id] = sec[start : start+size]
						}
					}
				}
			}
			for _, ty := range []uint32{3, 14, 16, 24} { // RT_ICON, RT_GROUP_ICON, RT_VERSION, RT_MANIFEST
				if len(types[ty]) == 0 {
					t.Errorf("resource type %d missing", ty)
				}
			}
			if count[3] != 6 || count[14] != 1 {
				t.Errorf("icons %d, groups %d; want 6 and 1", count[3], count[14])
			}
			ver := types[16]
			for _, s := range []string{"Cortex contributors", "Cortex - local memory for your AI tools", "cortex.exe", "0.0.0"} {
				if !bytes.Contains(ver, utf16z(s)) {
					t.Errorf("version info lacks %q", s)
				}
			}
			if !bytes.Contains(types[24], []byte("PerMonitorV2")) {
				t.Error("manifest lacks PerMonitorV2")
			}
		})
	}
}

type dirEntry struct{ id, off uint32 }

// dirEntries lists the entries of the resource directory at off; the offsets
// have the subdirectory flag removed.
func dirEntries(sec []byte, off uint32) []dirEntry {
	n := int(binary.LittleEndian.Uint16(sec[off+12:])) + int(binary.LittleEndian.Uint16(sec[off+14:]))
	var out []dirEntry
	for i := 0; i < n; i++ {
		e := off + 16 + 8*uint32(i)
		out = append(out, dirEntry{binary.LittleEndian.Uint32(sec[e:]), binary.LittleEndian.Uint32(sec[e+4:]) &^ 0x80000000})
	}
	return out
}

func utf16z(s string) []byte {
	var b []byte
	for _, c := range utf16.Encode([]rune(s)) {
		b = binary.LittleEndian.AppendUint16(b, c)
	}
	return b
}
