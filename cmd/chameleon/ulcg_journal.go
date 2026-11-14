package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	chameleon "github.com/soulteary/chameleon-desk"
	"io"
	"os"
)

// An append-only, command-owned journal preserves every pre-write checkpoint.
// Never reopen by name: an external replacement must not become our output.
type ulcgJournal struct {
	file    *os.File
	path    string
	records int
}

func newULCGJournal(path string) (*ulcgJournal, error) {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return nil, e
	}
	return &ulcgJournal{file: f, path: path}, nil
}
func (j *ulcgJournal) owned() bool {
	a, e := j.file.Stat()
	if e != nil {
		return false
	}
	b, e := os.Lstat(j.path)
	return e == nil && b.Mode().IsRegular() && os.SameFile(a, b)
}
func (j *ulcgJournal) Write(v chameleon.ULCGChallenges) error {
	if !j.owned() {
		return fmt.Errorf("ULCG journal was closed or replaced; collection stopped")
	}
	data, e := json.Marshal(v)
	if e != nil {
		return e
	}
	data = append(data, '\n')
	if n, e := j.file.Write(data); e != nil {
		return fmt.Errorf("write ULCG checkpoint: %w", e)
	} else if n != len(data) {
		return io.ErrShortWrite
	}
	if e = j.file.Sync(); e != nil {
		return fmt.Errorf("sync ULCG checkpoint: %w", e)
	}
	if j.records == 0 {
		if e := syncOutputDirectory(j.path); e != nil {
			return fmt.Errorf("sync ULCG journal directory: %w", e)
		}
	}
	if !j.owned() {
		return fmt.Errorf("ULCG journal replaced during checkpoint; collection stopped")
	}
	j.records++
	return nil
}
func (j *ulcgJournal) Close() error {
	remove := j.records == 0 && j.owned()
	e := j.file.Close()
	if remove {
		e = errors.Join(e, os.Remove(j.path))
	}
	return e
}

// Accept legacy single-object JSON and new JSONL journals. A crash may leave
// an incomplete final record; only the last complete checkpoint is usable.
func readULCGCheckpoint(path string) (out chameleon.ULCGChallenges, err error) {
	b, e := readLimitedFile(path)
	if e != nil {
		return out, e
	}
	d := json.NewDecoder(bytes.NewReader(b))
	records := 0
	for {
		var v chameleon.ULCGChallenges
		e = d.Decode(&v)
		if e == io.EOF || (e == io.ErrUnexpectedEOF && records > 0) {
			break
		}
		if e != nil {
			return out, e
		}
		if v.Schema != 1 || (len(v.UID) != 4 && len(v.UID) != 7 && len(v.UID) != 10) || (records > 0 && !bytes.Equal(v.UID, out.UID)) {
			return out, fmt.Errorf("invalid or mixed-UID ULCG checkpoint")
		}
		out = v
		records++
	}
	if records == 0 {
		return out, fmt.Errorf("empty ULCG journal")
	}
	return out, out.Validate()
}
