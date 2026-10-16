package main

import (
	"bytes"
	"encoding/binary"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEncodeJournalEntryPlainFields(t *testing.T) {
	got := string(encodeJournalEntry([]string{"MESSAGE=hello", "PRIORITY=6"}))
	if want := "MESSAGE=hello\nPRIORITY=6\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A value with a newline must use the length-prefixed form, or journald would
// read its second line as a new field.
func TestEncodeJournalEntryMultilineValue(t *testing.T) {
	val := "nix build failed:\nerror: boom"
	got := encodeJournalEntry([]string{"MESSAGE=" + val})

	var want bytes.Buffer
	want.WriteString("MESSAGE\n")
	binary.Write(&want, binary.LittleEndian, uint64(len(val)))
	want.WriteString(val + "\n")
	if !bytes.Equal(got, want.Bytes()) {
		t.Errorf("got %q, want %q", got, want.Bytes())
	}
}

// slowReader returns its chunks one Read at a time, as a pipe would.
type slowReader struct{ chunks []string }

func (s *slowReader) Read(p []byte) (int, error) {
	if len(s.chunks) == 0 {
		return 0, net.ErrClosed
	}
	n := copy(p, s.chunks[0])
	s.chunks = s.chunks[1:]
	return n, nil
}

func TestTeeLinesForwardsEverythingAndSplitsLines(t *testing.T) {
	r := &slowReader{chunks: []string{"one\ntw", "o\n", "Reboot x? [y/N]: "}}
	var term bytes.Buffer
	var lines []string
	teeLines(r, &term, func(l string) { lines = append(lines, l) })

	if got, want := term.String(), "one\ntwo\nReboot x? [y/N]: "; got != want {
		t.Errorf("terminal got %q, want %q", got, want)
	}
	// The prompt has no newline: it still reaches the terminal at once, and is
	// journaled as a line at EOF.
	want := []string{"one", "two", "Reboot x? [y/N]: "}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Errorf("lines got %q, want %q", lines, want)
	}
}

func TestJournalSendReachesSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.sock")
	srv, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	j := openJournal(path, "RUN1")
	if j == nil {
		t.Fatal("openJournal returned nil")
	}
	j.hostResult("ro", "deploy-failed", "")

	srv.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4096)
	n, err := srv.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	got := string(buf[:n])
	for _, f := range []string{
		"MESSAGE=ro: deploy-failed\n",
		"PRIORITY=3\n",
		"SYSLOG_IDENTIFIER=deploy\n",
		"DEPLOY_RUN_ID=RUN1\n",
		"DEPLOY_HOST=ro\n",
		"DEPLOY_RESULT=deploy-failed\n",
	} {
		if !strings.Contains(got, f) {
			t.Errorf("entry missing %q:\n%s", f, got)
		}
	}
}

func TestOpenJournalWithoutSocketIsNilAndSafe(t *testing.T) {
	j := openJournal(filepath.Join(t.TempDir(), "absent.sock"), "RUN1")
	if j != nil {
		t.Fatal("expected nil journal for a missing socket")
	}
	j.send(prioInfo, "dropped")
	j.hostResult("ro", "deployed", "")
}

func TestHostStatusStrings(t *testing.T) {
	for s, want := range map[HostStatus]string{
		StatusDeployed:     "deployed",
		StatusBuildFailed:  "build-failed",
		StatusCopyFailed:   "copy-failed",
		StatusDeployFailed: "deploy-failed",
		StatusCancelled:    "cancelled",
	} {
		if s.String() != want {
			t.Errorf("%d: got %q, want %q", s, s.String(), want)
		}
	}
}
