package main

import (
	"io"
	"log"
	"os"
)

const (
	logName    = "cortex.log"
	maxLogSize = 1 << 20
)

// logToFile sends the standard logger to path as well as to stderr, and
// returns a function that closes the file. A log that has grown past maxLogSize
// is started over, so it never needs rotating.
func logToFile(path string) (func(), error) {
	f, err := openLog(path)
	if err != nil {
		return nil, err
	}
	// The file comes first: a MultiWriter stops at the first error, and without
	// a console stderr is a handle that fails every write.
	log.SetOutput(io.MultiWriter(f, os.Stderr))
	return func() { log.SetOutput(os.Stderr); f.Close() }, nil
}

func openLog(path string) (*os.File, error) {
	flags := os.O_WRONLY | os.O_CREATE | os.O_APPEND
	if st, err := os.Stat(path); err == nil && st.Size() > maxLogSize {
		flags |= os.O_TRUNC
	}
	return os.OpenFile(path, flags, 0o600)
}
