package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestAtomicOutputConcurrentAndPrivate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if e := atomicFile(path, bytes.Repeat([]byte{byte(i)}, 4096)); e == nil {
				wins.Add(1)
			} else if !os.IsExist(e) {
				t.Error(e)
			}
		}(i)
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("published %d files", wins.Load())
	}
	b, e := os.ReadFile(path)
	if e != nil || len(b) != 4096 || !bytes.Equal(b, bytes.Repeat(b[:1], 4096)) {
		t.Fatalf("incomplete output: %v", e)
	}
	st, e := os.Stat(path)
	if e != nil {
		t.Fatal(e)
	}
	if st.Mode().Perm()&0077 != 0 {
		t.Fatalf("public output: %v", st.Mode())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temporary files leaked: %v", entries)
	}
}
func TestAtomicOutputPreservesLinks(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "dangling"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			original := filepath.Join(dir, "original")
			output := filepath.Join(dir, "output")
			if e := os.WriteFile(original, []byte("original"), 0600); e != nil {
				t.Fatal(e)
			}
			var e error
			switch kind {
			case "hardlink":
				e = os.Link(original, output)
			case "dangling":
				e = os.Symlink(filepath.Join(dir, "missing"), output)
			default:
				e = os.Symlink(original, output)
			}
			if e != nil {
				t.Skip(e)
			}
			if e = atomicFile(output, []byte("replacement")); !os.IsExist(e) {
				t.Fatalf("got %v", e)
			}
			b, _ := os.ReadFile(original)
			if string(b) != "original" {
				t.Fatal("original changed")
			}
			if _, e = os.Lstat(output); e != nil {
				t.Fatal("link removed", e)
			}
		})
	}
}
func TestCardfileRejectsInputAndTemplateAliases(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input")
	template := filepath.Join(dir, "template")
	for _, p := range []string{input, template} {
		if e := os.WriteFile(p, []byte("original"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	for _, target := range []string{input, template} {
		alias := filepath.Join(dir, fmt.Sprintf("alias-%s", filepath.Base(target)))
		if e := os.Link(target, alias); e != nil {
			t.Fatal(e)
		}
		if e := runCardfile([]string{"from-image", "--input", input, "--template", template, "--output", alias}); e == nil {
			t.Fatal("alias accepted")
		}
		b, _ := os.ReadFile(target)
		if string(b) != "original" {
			t.Fatal("input changed")
		}
	}
}
func TestResultPreservesExistingBackup(t *testing.T) {
	p := filepath.Join(t.TempDir(), "backup.json")
	if e := os.WriteFile(p, []byte("original"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := result(map[string]bool{"ok": true}, p, false); !errors.Is(e, os.ErrExist) {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "original" {
		t.Fatal("backup overwritten")
	}
}
