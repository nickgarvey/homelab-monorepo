package main

import (
	"bytes"
	"fmt"
	"io"
	"sync"
)

// stdoutMu serialises the pipeline stages' writes to stdout, which print
// concurrently.
var stdoutMu sync.Mutex

// say prints one line to stdout under the shared lock. Used by the pipeline
// stages that print alongside the streaming build log.
func say(format string, a ...any) {
	stdoutMu.Lock()
	defer stdoutMu.Unlock()
	fmt.Printf(format, a...)
}

// prefixWriter tags each complete line with a prefix before writing it to the
// terminal, holding a partial line until its newline arrives. Only the build
// log needs this: everything else in the pipeline emits whole lines of its own.
// It writes to termOut, not os.Stdout, so the build log stays out of the
// journal.
type prefixWriter struct {
	prefix string
	buf    []byte
}

func (w *prefixWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		w.emit(w.buf[:i])
		w.buf = w.buf[i+1:]
	}
	return len(p), nil
}

// Flush emits a trailing partial line, if any.
func (w *prefixWriter) Flush() {
	if len(w.buf) == 0 {
		return
	}
	w.emit(w.buf)
	w.buf = nil
}

// emit writes one prefixed line in a single Write, so it can't tear against
// other terminal output.
func (w *prefixWriter) emit(line []byte) {
	out := make([]byte, 0, len(w.prefix)+len(line)+1)
	out = append(append(append(out, w.prefix...), line...), '\n')
	termOut.Write(out)
}

// buildOut returns the writer for the build log, or nil to leave it to the
// runner's default.
func buildOut(opts BuildOpts) io.Writer {
	if !opts.Stream {
		return nil
	}
	return &prefixWriter{prefix: "[build] "}
}
