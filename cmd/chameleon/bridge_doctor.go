package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	chameleon "github.com/soulteary/chameleon-desk"
)

func runBridgeDoctor(args []string) error {
	f := flag.NewFlagSet("bridge doctor", flag.ContinueOnError)
	usb := f.String("usb-bridge", "", "managed USB HTTP loopback origin")
	ble := f.String("ble-bridge", "", "managed BLE HTTP loopback origin")
	key := f.String("bridge-token-file", "", "existing private token file")
	timeout := f.Duration("timeout", 3*time.Second, "management status deadline")
	jsonOut := f.Bool("json", false, "JSON output")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || (*usb == "") == (*ble == "") || *key == "" || *timeout <= 0 {
		return fmt.Errorf("use bridge doctor --usb-bridge URL or --ble-bridge URL, --bridge-token-file FILE and a positive timeout")
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	report := bridgeDoctorReport(ctx, *usb, *ble, *key)
	var err error
	if *jsonOut {
		err = printJSON(report)
	} else {
		for _, c := range report.Checks {
			fmt.Printf("%s %s: %s\n", c.Status, c.Code, c.Message)
		}
	}
	if err != nil {
		return err
	}
	for _, c := range report.Checks {
		if c.Status == "fail" {
			return fmt.Errorf("bridge diagnostics failed; no connection changes or device commands were attempted")
		}
	}
	return nil
}

func bridgeDoctorReport(ctx context.Context, usb, ble, key string) chameleon.BridgeDiagnostic {
	fail := func(code, message string) chameleon.BridgeDiagnostic {
		return chameleon.BridgeDiagnostic{Core: chameleon.CoreBuildInfo(), Checks: []chameleon.BridgeDiagnosticCheck{{Code: code, Status: "fail", Message: message}}}
	}
	token, err := readBridgeToken(key)
	if err != nil {
		return fail("token_file_invalid", err.Error())
	}
	if usb != "" {
		b, err := chameleon.NewUSBBridge(usb, token)
		if err != nil {
			return fail("bridge_configuration_invalid", err.Error())
		}
		return b.Diagnose(ctx)
	}
	b, err := chameleon.NewBLEBridge(ble, token)
	if err != nil {
		return fail("bridge_configuration_invalid", err.Error())
	}
	return b.Diagnose(ctx)
}
