package codex

import (
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"
)

// Limits against hostile or corrupt .zst files. Codex writes a single plain
// zstd frame at level 3 (window of a few MB), so these are far above any real
// rollout. They are variables so tests can lower them.
var (
	maxDecompressed int64  = 512 << 20 // bytes read from one file
	maxWindow       uint64 = 32 << 20  // zstd window a frame may demand
	maxDecoderMem   uint64 = 128 << 20 // decoder memory
)

// newDecoder returns a streaming decoder with bounded memory. One goroutine
// only: collection is sequential and a pool of workers per file is a waste.
func newDecoder(r io.Reader) (*zstd.Decoder, error) {
	return zstd.NewReader(r,
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderMaxWindow(maxWindow),
		zstd.WithDecoderMaxMemory(maxDecoderMem),
	)
}

// cappedReader fails instead of returning more than left bytes, so a small
// file that expands to gigabytes is skipped with a warning rather than read.
type cappedReader struct {
	r    io.Reader
	left int64
}

func (c *cappedReader) Read(p []byte) (int, error) {
	if c.left <= 0 {
		// Only an error if there really is more data.
		var one [1]byte
		if n, _ := c.r.Read(one[:]); n > 0 {
			return 0, fmt.Errorf("decompressed size exceeds the limit")
		}
		return 0, io.EOF
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	return n, err
}
