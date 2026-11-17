package main

import (
	"context"
	"flag"
	"fmt"
	chameleon "github.com/soulteary/chameleon-desk"
	"time"
)

func runEnvironmentDoctor(args []string) error {
	f := flag.NewFlagSet("environment doctor", flag.ContinueOnError)
	tools := f.String("recovery-tools-dir", chameleon.DefaultRecoveryToolsDir(), "trusted local worker directory")
	usb := f.String("usb-bridge", "", "managed USB HTTP loopback origin")
	ble := f.String("ble-bridge", "", "managed BLE HTTP loopback origin")
	key := f.String("bridge-token-file", "", "existing private bridge token file")
	timeout := f.Duration("timeout", 3*time.Second, "management status deadline")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || *timeout <= 0 || (*usb != "" && *ble != "") || ((*usb != "" || *ble != "") != (*key != "")) {
		return fmt.Errorf("use optional one bridge URL with its token file, trusted worker directory and positive timeout")
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	report := chameleon.DiagnoseEnvironment(*tools)
	if *key != "" {
		b := bridgeDoctorReport(ctx, *usb, *ble, *key)
		report.Bridge = &b
	}
	if err := printJSON(report); err != nil {
		return err
	}
	for _, w := range report.Recovery.Workers {
		if w.State != "available" {
			return fmt.Errorf("recovery environment is incomplete; no helper or device was opened")
		}
	}
	if report.Bridge != nil {
		for _, c := range report.Bridge.Checks {
			if c.Status == "fail" {
				return fmt.Errorf("bridge diagnosis failed; no connection was changed")
			}
		}
	}
	return nil
}
