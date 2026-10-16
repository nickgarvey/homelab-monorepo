package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// journalSocket is journald's native-protocol socket.
const journalSocket = "/run/systemd/journal/socket"

// journalMaxMessage caps one entry's MESSAGE. A datagram must fit the socket's
// send buffer, and nothing the tool prints in one line comes close.
const journalMaxMessage = 48 * 1024

// Syslog priorities used for PRIORITY=.
const (
	prioErr  = 3
	prioInfo = 6
)

// journal sends entries to the local systemd journal, tagged with
// SYSLOG_IDENTIFIER=deploy and the run's DEPLOY_RUN_ID, so a run can be read
// back after the terminal that showed it is gone:
//
//	journalctl -t deploy
//	journalctl DEPLOY_RUN_ID=<id>
//
// A nil *journal is valid and drops everything: a run without journald still
// deploys.
type journal struct {
	conn  net.Conn
	runID string
}

// runJournal is this process's journal, set by main before run starts.
var runJournal *journal

// openJournal connects to the journal at path, or returns nil if it can't.
func openJournal(path, runID string) *journal {
	conn, err := net.Dial("unixgram", path)
	if err != nil {
		return nil
	}
	return &journal{conn: conn, runID: runID}
}

// newRunID names one run: start time plus pid, unique per machine.
func newRunID(now time.Time) string {
	return fmt.Sprintf("%s-%d", now.Format("20060102T150405"), os.Getpid())
}

// send writes one entry. fields are extra KEY=VALUE pairs; keys must be
// uppercase journal field names. Errors are dropped: logging never fails a
// deploy.
func (j *journal) send(priority int, msg string, fields ...string) {
	if j == nil {
		return
	}
	if len(msg) > journalMaxMessage {
		msg = msg[:journalMaxMessage] + " [truncated]"
	}
	all := append([]string{
		"MESSAGE=" + msg,
		"PRIORITY=" + strconv.Itoa(priority),
		"SYSLOG_IDENTIFIER=deploy",
		"DEPLOY_RUN_ID=" + j.runID,
	}, fields...)
	j.conn.Write(encodeJournalEntry(all))
}

// hostResult records how one host ended this run. result is a short token
// (deployed, built, skipped, failed, build-failed, …); reason, if any, says why.
func (j *journal) hostResult(host, result, reason string) {
	prio := prioInfo
	if strings.Contains(result, "failed") {
		prio = prioErr
	}
	msg := host + ": " + result
	if reason != "" {
		msg += " (" + reason + ")"
	}
	j.send(prio, msg, "DEPLOY_HOST="+host, "DEPLOY_RESULT="+result)
}

// encodeJournalEntry renders KEY=VALUE fields in journald's native protocol. A
// value containing a newline can't use the plain form, so it is sent as the
// key, a newline, a little-endian 64-bit length and the raw value.
func encodeJournalEntry(fields []string) []byte {
	var b bytes.Buffer
	for _, f := range fields {
		key, val, _ := strings.Cut(f, "=")
		if !strings.Contains(val, "\n") {
			b.WriteString(f)
			b.WriteByte('\n')
			continue
		}
		b.WriteString(key)
		b.WriteByte('\n')
		binary.Write(&b, binary.LittleEndian, uint64(len(val)))
		b.WriteString(val)
		b.WriteByte('\n')
	}
	return b.Bytes()
}

// termMu serialises writes to the real terminal. Both the forwarded output of
// teeOutput and the build log (which bypasses it) write there concurrently.
var termMu sync.Mutex

// lockedWriter writes to the terminal under termMu, one Write at a time.
type lockedWriter struct{ w io.Writer }

func (l lockedWriter) Write(p []byte) (int, error) {
	termMu.Lock()
	defer termMu.Unlock()
	return l.w.Write(p)
}

// termOut and termErr reach the terminal directly, bypassing the journal. They
// are captured at startup, before teeOutput replaces os.Stdout and os.Stderr.
// The nix build log streams here: it is too long for the journal, and nix keeps
// each derivation's log in /nix/var/log/nix/drvs.
var (
	termOut io.Writer = lockedWriter{os.Stdout}
	termErr io.Writer = lockedWriter{os.Stderr}
)

// teeLines copies r to term as it arrives, so prompts without a trailing
// newline still show, and hands each complete line to line. A final partial
// line is handed over at EOF.
func teeLines(r io.Reader, term io.Writer, line func(string)) {
	buf := make([]byte, 32*1024)
	var pending []byte
	for {
		n, err := r.Read(buf)
		if n > 0 {
			term.Write(buf[:n])
			pending = append(pending, buf[:n]...)
			for {
				i := bytes.IndexByte(pending, '\n')
				if i < 0 {
					break
				}
				line(string(pending[:i]))
				pending = pending[i+1:]
			}
		}
		if err != nil {
			break
		}
	}
	if len(pending) > 0 {
		line(string(pending))
	}
}

// teeOutput routes os.Stdout and os.Stderr through pipes that copy to the
// terminal and journal every line: stdout at info, stderr at err. Every
// fmt.Print in the tool is captured without changing its call site. The
// returned function restores both and waits until everything written so far
// is delivered.
func teeOutput(j *journal) (stop func()) {
	var wg sync.WaitGroup
	var restores []func()
	for _, s := range []struct {
		f    **os.File
		term io.Writer
		prio int
	}{{&os.Stdout, termOut, prioInfo}, {&os.Stderr, termErr, prioErr}} {
		r, w, err := os.Pipe()
		if err != nil {
			continue
		}
		orig := *s.f
		*s.f = w
		prio := s.prio
		wg.Add(1)
		go func() {
			defer wg.Done()
			teeLines(r, s.term, func(l string) { j.send(prio, l) })
			r.Close()
		}()
		f := s.f
		restores = append(restores, func() {
			*f = orig
			w.Close()
		})
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			for _, r := range restores {
				r()
			}
			wg.Wait()
		})
	}
}

// Run lifecycle. main starts the run with startRun and ends it with endRun; a
// signal ends it from handleSignals. Whichever comes first records the outcome.
var (
	stopTee    = func() {}
	endOnce    sync.Once
	cleanupsMu sync.Mutex
	cleanups   []func()
)

// startRun opens the journal, starts the tee and records how the run was
// invoked.
func startRun(argv []string) {
	runJournal = openJournal(journalSocket, newRunID(time.Now()))
	stopTee = teeOutput(runJournal)
	runJournal.send(prioInfo, "deploy started: "+strings.Join(argv, " "),
		"DEPLOY_ARGV="+strings.Join(argv, " "), "DEPLOY_EVENT=start")
	handleSignals()
}

// endRun drains the tee and records the run's outcome. Only the first call
// counts.
func endRun(code int, outcome string) {
	endOnce.Do(func() {
		stopTee()
		prio := prioInfo
		if code != 0 {
			prio = prioErr
		}
		runJournal.send(prio, fmt.Sprintf("deploy %s (exit %d)", outcome, code),
			"DEPLOY_EVENT=end", "DEPLOY_EXIT="+strconv.Itoa(code), "DEPLOY_OUTCOME="+outcome)
	})
}

// onInterrupt registers cleanup to run if a signal ends the run. The returned
// function unregisters it.
func onInterrupt(f func()) (remove func()) {
	cleanupsMu.Lock()
	defer cleanupsMu.Unlock()
	cleanups = append(cleanups, f)
	i := len(cleanups) - 1
	return func() {
		cleanupsMu.Lock()
		defer cleanupsMu.Unlock()
		cleanups[i] = nil
	}
}

// handleSignals ends the run on SIGINT, SIGTERM or SIGHUP (the terminal went
// away): run the registered cleanups, journal which signal it was, exit 130.
func handleSignals() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		sig := <-ch
		fmt.Printf("\nInterrupted (%s)\n", sig)
		cleanupsMu.Lock()
		fs := append([]func(){}, cleanups...)
		cleanupsMu.Unlock()
		for i := len(fs) - 1; i >= 0; i-- {
			if fs[i] != nil {
				fs[i]()
			}
		}
		endRun(130, "interrupted by "+sig.String())
		os.Exit(130)
	}()
}
