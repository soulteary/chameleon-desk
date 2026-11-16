package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	chameleon "github.com/soulteary/chameleon-desk"
)

// These commands use the Desk server's existing device session. They must not
// open a second TCP client to the single-owner Python bridge.
func runTasks(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: chameleon tasks operations|session|list|submit|get|find|cancel|wait [options]")
	}
	action := args[0]
	f := flag.NewFlagSet("tasks "+action, flag.ContinueOnError)
	server := f.String("server", "http://127.0.0.1:8088", "Desk HTTP loopback origin")
	tokenFile := f.String("token-file", "", "private session-token file written by Desk serve")
	id := f.String("id", "", "task ID for get, cancel or wait")
	requestID := f.String("request-id", "", "caller-owned 16–128-byte submission ID, or ID to find")
	sessionID := f.String("session-id", "", "observed Desk device session ID; never inferred for a write")
	operation := f.String("operation", "", "registered operation ID")
	bodyFile := f.String("body-file", "", "JSON object body file; omit for GET operations")
	interval := f.Duration("poll-interval", time.Second, "read-only polling interval")
	timeout := f.Duration("timeout", time.Minute, "HTTP operation or wait deadline; does not cancel server tasks")
	if err := f.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if f.NArg() != 0 || *timeout <= 0 || *interval <= 0 {
		return fmt.Errorf("no positional arguments and positive timeouts are required")
	}
	switch action {
	case "operations", "session", "list", "submit", "get", "find", "cancel", "wait":
	default:
		return fmt.Errorf("unknown task action %q", action)
	}
	if action == "operations" {
		return printJSON(map[string]any{"schema_version": chameleon.OperationSchemaVersion, "operations": chameleon.Operations()})
	}
	if *tokenFile == "" {
		return fmt.Errorf("--token-file is required; use the private file written by Desk serve")
	}
	info, err := os.Lstat(*tokenFile)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 1024 || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
		return fmt.Errorf("token file must be a small regular file")
	}
	token, err := os.ReadFile(*tokenFile)
	if err != nil {
		return err
	}
	client, err := chameleon.NewTaskClient(*server, strings.TrimSpace(string(token)))
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	var value any
	switch action {
	case "session":
		value, err = client.Session(ctx)
	case "list":
		value, err = client.List(ctx)
	case "find":
		if *requestID == "" {
			return fmt.Errorf("--request-id is required")
		}
		value, err = client.Find(ctx, *requestID)
	case "get":
		value, err = client.Get(ctx, *id)
	case "cancel":
		value, err = client.Cancel(ctx, *id)
	case "wait":
		value, err = client.Wait(ctx, *id, *interval)
	case "submit":
		if _, err := chameleon.LookupOperation(*operation); err != nil {
			return err
		}
		var body []byte
		if *bodyFile != "" {
			body, err = readLimitedFile(*bodyFile)
			if err != nil {
				return err
			}
			var object map[string]json.RawMessage
			if err = json.Unmarshal(body, &object); err != nil || object == nil {
				return fmt.Errorf("--body-file must contain a JSON object")
			}
		}
		value, err = client.Submit(ctx, chameleon.TaskSubmission{RequestID: *requestID, SessionID: *sessionID, OperationID: *operation, Body: body})
	}
	if err != nil {
		return err
	}
	if err = printJSON(value); err != nil {
		return fmt.Errorf("task request already ran; query its request/task ID before further action: %w", err)
	}
	return nil
}
