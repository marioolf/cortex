package importer

import (
	"bytes"
	"errors"
	"fmt"
	"io"
)

// scanner reads the top-level JSON array of an export one element at a time
// without ever holding more than one element, and never more than the caps
// allow: encoding/json's Decoder buffers a whole value and any whitespace in
// front of it, which an attacker can make as large as the decompressed entry.
// It only finds where each element ends (tracking nesting and strings); the
// element itself is validated by json.Unmarshal afterwards.
type scanner struct {
	r        io.Reader
	buf      []byte
	pos, end int
	err      error // sticky read error
}

func newScanner(r io.Reader) *scanner { return &scanner{r: r, buf: make([]byte, 64<<10)} }

// fill makes at least one unread byte available, or returns the read error
// (io.EOF at the end).
func (s *scanner) fill() error {
	for s.pos >= s.end {
		if s.err != nil {
			return s.err
		}
		n, err := s.r.Read(s.buf)
		s.pos, s.end = 0, n
		if err != nil {
			s.err = err
		}
	}
	return nil
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }

// errNotArray means the file does not start with a JSON array.
var errNotArray = errors.New("not a JSON array")

var errTooMuchSpace = errors.New("too much whitespace between elements")

// skipSpace consumes whitespace and returns the next byte without consuming
// it. More than limit bytes of whitespace is an error: nothing is kept, but
// the stream is not worth following.
func (s *scanner) skipSpace(limit int64) (byte, error) {
	var n int64
	for {
		if err := s.fill(); err != nil {
			return 0, err
		}
		i := s.pos
		for i < s.end && isSpace(s.buf[i]) {
			i++
		}
		n += int64(i - s.pos)
		s.pos = i
		if n > limit {
			return 0, errTooMuchSpace
		}
		if i < s.end {
			return s.buf[i], nil
		}
	}
}

// start skips an optional UTF-8 BOM and whitespace and consumes the opening
// "[" of the array.
func (s *scanner) start(limit int64) error {
	c, err := s.skipSpace(limit)
	if err == nil && c == 0xEF {
		for _, want := range []byte{0xEF, 0xBB, 0xBF} {
			if err = s.fill(); err != nil {
				return err
			}
			if s.buf[s.pos] != want {
				return errNotArray
			}
			s.pos++
		}
		c, err = s.skipSpace(limit)
	}
	if err != nil {
		return err
	}
	if c != '[' {
		return errNotArray
	}
	s.pos++
	return nil
}

// finish consumes the closing "]" that next reported as done and checks that
// nothing but whitespace follows it. It returns errTrailing otherwise.
func (s *scanner) finish(limit int64) error {
	s.pos++
	_, err := s.skipSpace(limit)
	switch {
	case err == io.EOF:
		return nil
	case err == nil:
		return errTrailing
	}
	return err
}

var errTrailing = errors.New("unexpected data after the end of the export")

// next returns the following element of the array. At the closing "]" it
// returns done. An element longer than limit is not kept: tooBig is true, raw is
// nil, and the scanner has moved past it, so the next call goes on with the
// element after it. size is the element's length in bytes.
func (s *scanner) next(first bool, limit int64) (raw []byte, size int64, tooBig, done bool, err error) {
	c, err := s.skipSpace(limit)
	if err != nil {
		return nil, 0, false, false, err
	}
	if !first {
		switch c {
		case ']':
			return nil, 0, false, true, nil
		case ',':
			s.pos++
			if c, err = s.skipSpace(limit); err != nil {
				return nil, 0, false, false, err
			}
		default:
			return nil, 0, false, false, fmt.Errorf("unexpected %q between elements", c)
		}
	}
	if c == ']' && first {
		return nil, 0, false, true, nil
	}
	if c == ',' || c == ']' || c == '}' || c == ':' {
		return nil, 0, false, false, fmt.Errorf("unexpected %q", c)
	}

	var st scanState
	var out []byte
	for {
		if err := s.fill(); err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return nil, size, false, false, err
		}
		chunk := s.buf[s.pos:s.end]
		n, fin := st.scan(chunk)
		size += int64(n)
		if !tooBig {
			if size > limit {
				tooBig, out = true, nil
			} else {
				if len(out)+n > cap(out) { // double, so that growing to the cap allocates about twice the cap
					out = append(make([]byte, 0, min(max(2*cap(out), len(out)+n, 16<<10), int(limit))), out...)
				}
				out = append(out, chunk[:n]...)
			}
		}
		s.pos += n
		if fin {
			return out, size, tooBig, false, nil
		}
	}
}

// scanState finds the end of one JSON value across chunks.
type scanState struct {
	depth int
	inStr bool
	esc   bool
}

// scan looks at chunk and returns how many bytes belong to the element and
// whether its end was reached. A scalar ends before the delimiter that
// follows it.
func (st *scanState) scan(chunk []byte) (n int, done bool) {
	for i := 0; i < len(chunk); {
		if st.inStr {
			if st.esc {
				st.esc = false
				i++
				continue
			}
			j := bytes.IndexAny(chunk[i:], `"\`)
			if j < 0 {
				return len(chunk), false
			}
			i += j
			if chunk[i] == '\\' {
				st.esc = true
				i++
				continue
			}
			st.inStr = false
			i++
			if st.depth == 0 {
				return i, true
			}
			continue
		}
		switch c := chunk[i]; c {
		case '"':
			st.inStr = true
		case '{', '[':
			st.depth++
		case '}', ']':
			if st.depth == 0 {
				return i, true
			}
			st.depth--
			if st.depth == 0 {
				return i + 1, true
			}
		case ',', ' ', '\t', '\r', '\n':
			if st.depth == 0 {
				return i, true
			}
		}
		i++
	}
	return len(chunk), false
}
