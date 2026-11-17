package main

import (
	"bufio"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	chameleon "github.com/soulteary/chameleon-desk"
)

const taskJournalLimit = 64 << 20

type taskJournal struct {
	file *os.File
	path string
}
type taskJournalHeader struct {
	Version int    `json:"version"`
	StoreID string `json:"store_id"`
	Key     string `json:"receipt_key"`
}
type taskJournalEntry struct {
	Task chameleon.TaskSnapshot `json:"task"`
	Hash string                 `json:"receipt_hmac"`
}

func defaultTaskJournalPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "chameleon-desk", "tasks.jsonl"), nil
}

// One process owns the journal. OS locks are released after a crash; a stale
// lockfile cannot prevent restart or silently enable a second writer.
func (ts *webTasks) openJournal(path string) (err error) {
	ts.init()
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if info, e := os.Lstat(path); e == nil && (!info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0)) {
		return fmt.Errorf("task journal must be a private regular file")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = f.Close()
		}
	}()
	if err = lockTaskJournal(f); err != nil {
		return fmt.Errorf("task journal is already owned or cannot be locked: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		return err
	}
	actual, e := os.Lstat(path)
	if e != nil || !actual.Mode().IsRegular() || !os.SameFile(actual, info) || (runtime.GOOS != "windows" && actual.Mode().Perm()&0077 != 0) {
		return fmt.Errorf("task journal changed while opening")
	}
	if info.Size() > taskJournalLimit {
		return fmt.Errorf("task journal exceeds %d bytes; archive it explicitly before starting a new history", taskJournalLimit)
	}
	ts.journal = &taskJournal{f, path}
	if info.Size() == 0 {
		ts.storeID = newTaskID()
		header := taskJournalHeader{1, ts.storeID, hex.EncodeToString(ts.receiptKey[:])}
		if err = ts.journal.append(header); err != nil {
			return err
		}
		if err = syncOutputDirectory(path); err != nil {
			return err
		}
		return nil
	}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 64<<10)
	if !scanner.Scan() {
		return fmt.Errorf("missing task journal header")
	}
	var header taskJournalHeader
	if err = json.Unmarshal(scanner.Bytes(), &header); err != nil {
		return fmt.Errorf("invalid task journal header: %w", err)
	}
	key, e := hex.DecodeString(header.Key)
	if header.Version != 1 || len(header.StoreID) != 32 || e != nil || len(key) != 32 {
		return fmt.Errorf("unsupported task journal header")
	}
	copy(ts.receiptKey[:], key)
	ts.storeID = header.StoreID
	for scanner.Scan() {
		var entry taskJournalEntry
		if err = json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			return fmt.Errorf("corrupt task journal; restore the journal from backup rather than discarding receipts: %w", err)
		}
		v := entry.Task
		hash, e := hex.DecodeString(entry.Hash)
		operation, ok := chameleon.OperationForPath(v.Path)
		if e != nil || len(hash) != 32 || len(v.ID) != 32 || len(v.RequestID) > 128 || v.SessionID == "" || !validJournalState(v.State) || !validJournalPhase(v.Phase) || len(v.RequestID) < 16 || !ok || operation.ID != v.OperationID || operation.Write != v.Write || len(v.Result) != 0 {
			return fmt.Errorf("invalid task journal record")
		}
		if prior, ok := ts.receipts[v.RequestID]; ok && (prior.id != v.ID || hex.EncodeToString(prior.hash[:]) != entry.Hash) {
			return fmt.Errorf("conflicting task journal receipt")
		}
		if previous := ts.records[v.ID]; previous != nil && (previous.RequestID != v.RequestID || previous.SessionID != v.SessionID || previous.Path != v.Path) {
			return fmt.Errorf("conflicting task journal identity")
		}
		if _, ok := ts.records[v.ID]; !ok {
			ts.order = append(ts.order, v.ID)
		}
		var digest [32]byte
		copy(digest[:], hash)
		ts.receipts[v.RequestID] = taskReceipt{digest, v.ID}
		ts.records[v.ID] = &webTask{TaskSnapshot: v}
	}
	if err = scanner.Err(); err != nil {
		return fmt.Errorf("read task journal: %w", err)
	}
	// A truncated final line can be syntactically valid JSON without its newline.
	// Require the write delimiter so append never repairs/merges unknown bytes.
	last := make([]byte, 1)
	if _, err = f.ReadAt(last, info.Size()-1); err != nil || last[0] != '\n' {
		return fmt.Errorf("incomplete task journal; restore or inspect it before starting")
	}
	if len(ts.receipts) > 4096 {
		return fmt.Errorf("too many task receipts")
	}
	for _, id := range ts.order {
		t := ts.records[id]
		t.Recovered = true
		t.Result = nil
		t.ResultRetained = false
		if !taskTerminal(t) {
			started := entryStarted(t.TaskSnapshot)
			t.State = "interrupted"
			t.Phase = "finished"
			t.Status = 0
			t.Updated = time.Now().UTC()
			t.Outcome = "not_started"
			// started is intentionally not serialized. The last durable phase is the
			// pre-callback boundary; it conservatively includes untransmitted writes.
			if started {
				t.Outcome = "read_interrupted"
				if t.Write {
					t.Outcome = "write_outcome_unknown"
				}
			}
			if err = ts.persistLocked(t); err != nil {
				return err
			}
		}
	}
	ts.evictLocked()
	return nil
}
func entryStarted(t chameleon.TaskSnapshot) bool {
	return t.Commands > 0 || t.Phase == "device" || t.Phase == "environment" || t.Phase == "bridge-management"
}
func (j *taskJournal) append(v any) error {
	current, e := os.Lstat(j.path)
	if e != nil {
		return e
	}
	owned, e := j.file.Stat()
	if e != nil {
		return e
	}
	if !current.Mode().IsRegular() || !os.SameFile(current, owned) || (runtime.GOOS != "windows" && current.Mode().Perm()&0077 != 0) {
		return fmt.Errorf("task journal was replaced")
	}
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	b = append(b, '\n')
	if owned.Size()+int64(len(b)) > taskJournalLimit {
		return fmt.Errorf("task journal capacity reached")
	}
	n, e := j.file.Write(b)
	if e != nil {
		return e
	}
	if n != len(b) {
		return io.ErrShortWrite
	}
	if e = j.file.Sync(); e != nil {
		return e
	}
	current, e = os.Lstat(j.path)
	if e != nil || !os.SameFile(current, owned) {
		return fmt.Errorf("task journal was replaced while syncing")
	}
	return nil
}
func (ts *webTasks) persistLocked(t *webTask) error {
	if ts.journalError != nil {
		return ts.journalError
	}
	if ts.journal == nil {
		return nil
	} // In-memory fixtures only; serve always opens a journal.
	snapshot := t.TaskSnapshot
	snapshot.Result = nil
	snapshot.ResultRetained = false
	hash := ts.receipts[t.RequestID].hash
	err := ts.journal.append(taskJournalEntry{snapshot, hex.EncodeToString(hash[:])})
	if err != nil {
		ts.journalError = err
	}
	return err
}
func (ts *webTasks) digest(input any) [32]byte {
	b, _ := json.Marshal(input)
	mac := hmac.New(sha256.New, ts.receiptKey[:])
	_, _ = mac.Write(b)
	var out [32]byte
	copy(out[:], mac.Sum(nil))
	return out
}
func (ts *webTasks) evictLocked() {
	for len(ts.records) > 128 {
		removed := false
		for _, id := range ts.order {
			if t := ts.records[id]; t != nil && taskTerminal(t) {
				delete(ts.records, id)
				removed = true
				break
			}
		}
		if !removed {
			return
		}
	}
}
func (ts *webTasks) newReceiptKey() {
	if _, err := rand.Read(ts.receiptKey[:]); err != nil {
		panic(err)
	}
}

// Publish a private token for CLI/SDK clients without placing it in argv/logs.
// The token changes with each server process; it is not part of task history.
func writeTaskTokenFile(path, token string) error {
	if info, e := os.Lstat(path); e == nil {
		if !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) || info.Size() > 128 {
			return fmt.Errorf("refusing to replace a non-private token file")
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		old, e := hex.DecodeString(strings.TrimSpace(string(b)))
		if e != nil || len(old) != 32 {
			return fmt.Errorf("refusing to overwrite a file that is not a Desk session token")
		}
	} else if !os.IsNotExist(e) {
		return e
	}

	f, err := os.CreateTemp(filepath.Dir(path), ".desk-token-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.WriteString(token + "\n"); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	return syncOutputDirectory(path)
}

func validJournalState(state string) bool {
	switch state {
	case "queued", "running", "succeeded", "failed", "canceled", "interrupted":
		return true
	}
	return false
}
func validJournalPhase(phase string) bool {
	return phase == "queued" || phase == "device" || phase == "environment" || phase == "bridge-management" || phase == "finished"
}
