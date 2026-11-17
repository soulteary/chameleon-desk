package main

import (
	"encoding/json"
	"flag"
	"fmt"
	chameleon "github.com/soulteary/chameleon-desk"
	"os"
)

// Bridge controls go through the running Desk task service, never a second
// bridge data connection or a competing management connection.
func runBridgeCLI(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("use bridge doctor|status|scan|select|connect|disconnect")
	}
	if args[0] == "doctor" {
		return runBridgeDoctor(args[1:])
	}
	action := args[0]
	switch action {
	case "status", "scan", "select", "connect", "disconnect":
	default:
		return fmt.Errorf("unknown bridge action")
	}
	f := flag.NewFlagSet("bridge "+action, flag.ContinueOnError)
	server := f.String("server", "http://127.0.0.1:8088", "existing Desk HTTP loopback origin")
	token := f.String("token-file", "", "private Desk task-client token file")
	session := f.String("session-id", "", "observed Desk device session ID")
	request := f.String("request-id", "", "caller-owned unique 16–128-byte request ID")
	device := f.String("device-id", "", "exact ID from bridge scan; select only")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if f.NArg() != 0 || *token == "" || *session == "" || len(*request) < 16 || len(*request) > 128 || ((action == "select") != (*device != "")) {
		return fmt.Errorf("require token-file, observed session-id, unique request-id, and device-id only for select")
	}
	// Use the existing CLI client for token permissions, submission receipts and
	// ambiguous-acceptance handling. There is one submission and no automatic wait.
	taskArgs := []string{"submit", "--server", *server, "--token-file", *token, "--session-id", *session, "--request-id", *request, "--operation", "bridge." + action}
	if action != "status" {
		body := map[string]string{}
		if action == "select" {
			body["device_id"] = *device
		}
		file, err := os.CreateTemp("", "desk-bridge-body-*.json")
		if err != nil {
			return err
		}
		defer os.Remove(file.Name())
		err = json.NewEncoder(file).Encode(body)
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		taskArgs = append(taskArgs, "--body-file", file.Name())
	}
	if _, err := chameleon.LookupOperation("bridge." + action); err != nil {
		return err
	}
	return runTasks(taskArgs)
}
