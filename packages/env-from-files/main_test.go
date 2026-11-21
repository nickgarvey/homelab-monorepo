package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestReadDir(t *testing.T) {
	dir := t.TempDir()
	for name, value := range map[string]string{"A_KEY": "one\n", "B": "two", ".hidden": "x"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o700); err != nil {
		t.Fatal(err)
	}
	env, err := readDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(env)
	if want := []string{"A_KEY=one\n", "B=two"}; !slices.Equal(env, want) {
		t.Fatalf("got %q, want %q", env, want)
	}
}

func TestReadDirRejectsBadName(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bad-name"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readDir(dir); err == nil {
		t.Fatal("want an error for a file name that is not a variable name")
	}
}
