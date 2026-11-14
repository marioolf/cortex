package wsl

import (
	"reflect"
	"testing"
	"unicode/utf16"
)

func utf16le(s string, bom bool) []byte {
	u := utf16.Encode([]rune(s))
	if bom {
		u = append([]uint16{0xFEFF}, u...)
	}
	var b []byte
	for _, c := range u {
		b = append(b, byte(c), byte(c>>8))
	}
	return b
}

func TestParseDistros(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want []string
	}{
		{"empty", nil, nil},
		{"bom and crlf", utf16le("Ubuntu\r\nDebian-12\r\n\r\n", true), []string{"Ubuntu", "Debian-12"}},
		{"no bom", utf16le("Ubuntu\r\n", false), []string{"Ubuntu"}},
		{"non ascii", utf16le("Ubuntu-ñ\r\n", false), []string{"Ubuntu-ñ"}},
		{"odd trailing byte", append(utf16le("Ubuntu\r\n", false), 0x0a), []string{"Ubuntu"}},
	}
	for _, c := range cases {
		if got := parseDistros(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestHomeGlob(t *testing.T) {
	if got, want := homeGlob("Ubuntu"), `\\wsl.localhost\Ubuntu\home\*`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
