package main

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestParseVersion(t *testing.T) {
	for in, want := range map[string]struct {
		text string
		n    [4]uint16
	}{
		"0.0.0":       {"0.0.0", [4]uint16{0, 0, 0, 0}},
		"v1.2.3-rc1":  {"1.2.3-rc1", [4]uint16{1, 2, 3, 0}},
		"1.2.3.4+abc": {"1.2.3.4+abc", [4]uint16{1, 2, 3, 4}},
	} {
		text, n, err := parseVersion(in)
		if err != nil || text != want.text || n != want.n {
			t.Errorf("parseVersion(%q) = %q %v %v", in, text, n, err)
		}
	}
	for _, in := range []string{"", "dev", "1.2.3.4.5", "1.x", "70000.0.0", "-rc1"} {
		if _, _, err := parseVersion(in); err == nil {
			t.Errorf("parseVersion(%q) should fail", in)
		}
	}
}

// walk reads one resource directory and returns its entry ids and offsets
// (subdirectory flag removed).
func walk(t *testing.T, sec []byte, off int) (ids, offs []uint32, sub []bool) {
	t.Helper()
	if n := binary.LittleEndian.Uint16(sec[off+12:]); n != 0 {
		t.Fatalf("unexpected named entries")
	}
	nID := int(binary.LittleEndian.Uint16(sec[off+14:]))
	for i := 0; i < nID; i++ {
		e := off + 16 + 8*i
		o := binary.LittleEndian.Uint32(sec[e+4:])
		ids = append(ids, binary.LittleEndian.Uint32(sec[e:]))
		offs = append(offs, o&^0x80000000)
		sub = append(sub, o&0x80000000 != 0)
	}
	return
}

func TestBuild(t *testing.T) {
	ico, err := os.ReadFile("../../winres/cortex.ico")
	if err != nil {
		t.Fatal(err)
	}
	imgs, err := parseICO(ico)
	if err != nil {
		t.Fatal(err)
	}
	if len(imgs) != 6 {
		t.Fatalf("the icon has %d images, want 6", len(imgs))
	}
	for _, tg := range targets {
		obj, err := build(tg, ico, "v1.2.3-rc1")
		if err != nil {
			t.Fatal(err)
		}
		again, _ := build(tg, ico, "v1.2.3-rc1")
		if !bytes.Equal(obj, again) {
			t.Errorf("%s: output is not deterministic", tg.file)
		}
		f, err := pe.NewFile(bytes.NewReader(obj))
		if err != nil {
			t.Fatalf("%s: %v", tg.file, err)
		}
		if f.Machine != tg.machine || len(f.Sections) != 1 || f.Sections[0].Name != ".rsrc" {
			t.Fatalf("%s: machine %#x, sections %v", tg.file, f.Machine, f.Sections)
		}
		s := f.Sections[0]
		want := len(imgs) + 3 // icons, group, version, manifest
		if int(s.NumberOfRelocations) != want {
			t.Fatalf("%s: %d relocations, want %d", tg.file, s.NumberOfRelocations, want)
		}
		sec, err := s.Data()
		if err != nil {
			t.Fatal(err)
		}
		// Each relocation must have the right type and point at a data entry whose
		// RVA field holds an in-section offset.
		for i := 0; i < want; i++ {
			r := obj[s.PointerToRelocations+uint32(10*i):]
			at := binary.LittleEndian.Uint32(r)
			if typ := binary.LittleEndian.Uint16(r[8:]); typ != tg.reloc {
				t.Errorf("%s: relocation type %d, want %d", tg.file, typ, tg.reloc)
			}
			if rva := binary.LittleEndian.Uint32(sec[at:]); int(rva) >= len(sec) {
				t.Errorf("%s: relocation target %d outside the section", tg.file, rva)
			}
		}
		if len(f.Symbols) == 0 || f.Symbols[0].Name != ".rsrc" {
			t.Errorf("%s: missing the .rsrc symbol", tg.file)
		}

		types, typeOffs, sub := walk(t, sec, 0)
		wantTypes := []uint32{rtIcon, rtGroupIcon, rtVersion, rtManifest}
		if len(types) != 4 {
			t.Fatalf("%s: types %v", tg.file, types)
		}
		for i, ty := range types {
			if ty != wantTypes[i] || !sub[i] {
				t.Fatalf("%s: types %v", tg.file, types)
			}
			ids, nameOffs, _ := walk(t, sec, int(typeOffs[i]))
			wantIDs := 1
			if ty == rtIcon {
				wantIDs = len(imgs)
			}
			if len(ids) != wantIDs || ids[0] != 1 {
				t.Errorf("%s: type %d has ids %v", tg.file, ty, ids)
			}
			for _, no := range nameOffs {
				langs, entries, lsub := walk(t, sec, int(no))
				if len(langs) != 1 || langs[0] != langEnUS || lsub[0] {
					t.Fatalf("%s: type %d languages %v", tg.file, ty, langs)
				}
				if size := binary.LittleEndian.Uint32(sec[entries[0]+4:]); size == 0 {
					t.Errorf("%s: empty resource of type %d", tg.file, ty)
				}
			}
		}
		if !bytes.Contains(sec, utf16z("Cortex contributors")) || !bytes.Contains(sec, utf16z("1.2.3-rc1")) {
			t.Errorf("%s: version strings missing", tg.file)
		}
		if !bytes.Contains(sec, []byte(`level="asInvoker"`)) {
			t.Errorf("%s: manifest missing", tg.file)
		}
	}
}

func TestRunWritesBothFiles(t *testing.T) {
	dir := t.TempDir()
	if err := run("../../winres/cortex.ico", "0.0.0", dir); err != nil {
		t.Fatal(err)
	}
	for _, tg := range targets {
		if _, err := os.Stat(filepath.Join(dir, tg.file)); err != nil {
			t.Error(err)
		}
	}
}
