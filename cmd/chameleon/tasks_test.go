package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func taskCLIOutput(t *testing.T, args ...string) ([]byte, error) {
	t.Helper()
	f, e := os.CreateTemp(t.TempDir(), "stdout")
	if e != nil {
		t.Fatal(e)
	}
	old := os.Stdout
	os.Stdout = f
	defer func() { os.Stdout = old; f.Close() }()
	err := runTasks(args)
	b, e := os.ReadFile(f.Name())
	if e != nil {
		t.Fatal(e)
	}
	return b, err
}
func TestTaskCLIQueriesTheExistingServerWithoutOpeningDevice(t *testing.T) {
	ws, opens := sessionFixture(t)
	ws.rotateSessionID()
	defer ws.tasks.stop()
	srv := httptest.NewServer(offlineWeb(t, ws))
	defer srv.Close()
	tokenFile := filepath.Join(t.TempDir(), "token")
	if e := os.WriteFile(tokenFile, []byte(ws.token), 0600); e != nil {
		t.Fatal(e)
	}
	common := []string{"--server", srv.URL, "--token-file", tokenFile}
	b, e := taskCLIOutput(t, append([]string{"session"}, common...)...)
	if e != nil || !strings.Contains(string(b), ws.currentSessionID()) {
		t.Fatal(string(b), e)
	}
	bodyFile := filepath.Join(t.TempDir(), "body.json")
	os.WriteFile(bodyFile, []byte(`{"slot":0}`), 0600)
	args := append([]string{"submit"}, common...)
	args = append(args, "--request-id", "cli-request-0001", "--session-id", ws.currentSessionID(), "--operation", "slot.activate", "--body-file", bodyFile)
	ws.allowWrites = true
	b, e = taskCLIOutput(t, args...)
	if e != nil {
		t.Fatal(e)
	}
	var accepted webTask
	if e = json.Unmarshal(b, &accepted); e != nil {
		t.Fatal(e)
	}
	waitTask(t, ws, accepted.ID, func(v webTask) bool { return taskTerminal(&v) })
	for _, action := range []string{"get", "wait", "cancel"} {
		b, e = taskCLIOutput(t, append(append([]string{action}, common...), "--id", accepted.ID)...)
		if e != nil || !strings.Contains(string(b), `"outcome": "not_started"`) {
			t.Fatal(action, string(b), e)
		}
	}
	b, e = taskCLIOutput(t, append(append([]string{"find"}, common...), "--request-id", "cli-request-0001")...)
	if e != nil || !strings.Contains(string(b), accepted.ID) {
		t.Fatal(string(b), e)
	}
	if opens.Load() != 0 {
		t.Fatal("task CLI opened a competing device connection")
	}
}
func TestTaskCLIRejectsInvalidInputAndTokenFiles(t *testing.T) {
	if _, e := taskCLIOutput(t, "list"); e == nil {
		t.Fatal("missing token accepted")
	}
	tokenFile := filepath.Join(t.TempDir(), "token")
	os.WriteFile(tokenFile, []byte("private"), 0600)
	for _, args := range [][]string{{"get", "--id", "../capabilities"}, {"submit", "--operation", "raw.any"}, {"wait", "--poll-interval", "0s"}} {
		if _, e := taskCLIOutput(t, append(args, "--token-file", tokenFile)...); e == nil {
			t.Fatal(args)
		}
	}
	target := filepath.Join(t.TempDir(), "valuable-file")
	os.WriteFile(target, []byte("preserve"), 0600)
	if e := writeTaskTokenFile(target, strings.Repeat("a", 64)); e == nil {
		t.Fatal("unrelated file overwritten")
	}
	b, _ := os.ReadFile(target)
	if string(b) != "preserve" {
		t.Fatal(string(b))
	}
}
