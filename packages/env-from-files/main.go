// env-from-files runs a program with environment variables read from files:
// each regular file in a directory becomes a variable named after the file,
// whose value is the file's content. It is the bridge from pod secrets that the
// Secrets Store CSI driver mounts as files (docs/openbao.md, "Pod secrets") to
// apps that read their secrets from the environment.
//
//	env-from-files install <dir>            copy this binary to <dir>/env-from-files
//	env-from-files <dir> -- <program> [args...]
//
// The second form replaces itself with the program (exec), so the program keeps
// the PID and receives signals directly. Names starting with "." (the CSI
// driver's bookkeeping) are skipped; a name that is not a valid variable name is
// an error. Values are taken byte for byte.
package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

var validName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "env-from-files:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 2 && args[0] == "install" {
		return install(args[1])
	}
	if len(args) < 3 || args[1] != "--" {
		return fmt.Errorf("usage: env-from-files install <dir> | env-from-files <dir> -- <program> [args...]")
	}
	env, err := readDir(args[0])
	if err != nil {
		return err
	}
	prog, err := exec.LookPath(args[2])
	if err != nil {
		return err
	}
	return syscall.Exec(prog, args[2:], append(os.Environ(), env...))
}

// readDir returns NAME=value for every regular file in dir, following symlinks.
func readDir(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var env []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if !validName.MatchString(name) {
			return nil, fmt.Errorf("%s: not a valid environment variable name", path)
		}
		value, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		env = append(env, name+"="+string(value))
	}
	return env, nil
}

func install(dir string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	src, err := os.Open(self)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(filepath.Join(dir, "env-from-files"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return err
	}
	return dst.Close()
}
