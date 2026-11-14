package wsl

import (
	"reflect"
	"testing"
	"time"
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

func TestRootOf(t *testing.T) {
	if got, want := rootOf("Ubuntu"), `\\wsl.localhost\Ubuntu`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCacheReusesResultForTTL(t *testing.T) {
	now := time.Unix(1000, 0)
	calls := 0
	c := &distroCache{
		ttl:   20 * time.Second,
		now:   func() time.Time { return now },
		fetch: func() []string { calls++; return []string{"Ubuntu"} },
	}
	for i := 0; i < 8; i++ {
		c.get()
	}
	if calls != 1 {
		t.Fatalf("fetched %d times within the ttl, want 1", calls)
	}
	now = now.Add(21 * time.Second)
	c.get()
	if calls != 2 {
		t.Errorf("fetched %d times after the ttl, want 2", calls)
	}
}

func TestCacheKeepsAnEmptyResult(t *testing.T) {
	calls := 0
	c := &distroCache{ttl: time.Minute, now: time.Now, fetch: func() []string { calls++; return nil }}
	c.get()
	c.get()
	if calls != 1 {
		t.Errorf("an empty result was fetched %d times, want 1", calls)
	}
}

func TestSetLookupReplacesAndRestores(t *testing.T) {
	restore := SetLookup(func() []string { return []string{"Fake"} })
	if got := Roots(); !reflect.DeepEqual(got, []string{`\\wsl.localhost\Fake`}) {
		t.Errorf("replaced: %q", got)
	}
	restore()
	if got := running.fetch; got == nil {
		t.Error("not restored")
	}
	restore2 := SetLookup(func() []string { return nil })
	defer restore2()
	if got := Homes(); got != nil {
		t.Errorf("empty lookup: %q", got)
	}
}
