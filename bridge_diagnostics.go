package chameleon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"syscall"
)

type BridgeHTTPError struct{ Status int }

func (e *BridgeHTTPError) Error() string {
	return fmt.Sprintf("device bridge returned HTTP %d", e.Status)
}

type BridgeDiagnosticCheck struct {
	Code    string `json:"code"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type BridgeDiagnostic struct {
	Core   BuildInfo               `json:"core_version"`
	Bridge *BuildInfo              `json:"bridge_version,omitempty"`
	Checks []BridgeDiagnosticCheck `json:"checks"`
}

// Diagnose reads management status once. It never scans, connects, disconnects,
// claims the data socket or sends firmware commands. It cannot certify hardware.
func (b *BLEBridge) Diagnose(ctx context.Context) BridgeDiagnostic {
	out := BridgeDiagnostic{Core: CoreBuildInfo()}
	add := func(code, status, message string) {
		out.Checks = append(out.Checks, BridgeDiagnosticCheck{code, status, message})
	}
	var status BridgeStatus
	err := b.request(ctx, http.MethodGet, "status", nil, &status)
	if err == nil {
		err = b.validateStatus(status)
		if err == nil {
			out.Bridge = status.Build
		}
	}
	if err != nil {
		var h *BridgeHTTPError
		var n net.Error
		switch {
		case errors.Is(err, ErrBridgeVersionMismatch):
			add("version_mismatch", "fail", ErrBridgeVersionMismatch.Error())
		case errors.As(err, &h) && (h.Status == 401 || h.Status == 403):
			add("authentication_failed", "fail", "Check that Desk and the bridge use the same private token file")
		case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &n) && n.Timeout()):
			add("management_timeout", "fail", "Management status timed out; check bridge process and listener")
		case errors.Is(err, syscall.ECONNREFUSED):
			add("management_unreachable", "fail", "No listener accepted the configured address; start the bridge or check its port")
		default:
			add("management_invalid_or_unreachable", "fail", "Cannot validate bridge status; check URL, process, medium and protocol")
		}
		return out
	}
	if status.Build == nil {
		add("unversioned_bridge", "unknown", "Legacy bridge has no build metadata; install a matching distribution to verify pairing")
	} else if out.Core.Revision == "" || status.Build.Revision == "" {
		add("source_build", "unknown", "Release and compatibility match; exact revision pairing is unverified for a source build")
	} else {
		add("version_match", "pass", "Core and bridge release, compatibility and revision match")
	}
	if status.State == "blocked" {
		add("cleanup_blocked", "fail", "Bridge cleanup is blocked; check the device and restart the bridge explicitly")
	}
	if status.SessionActive {
		add("data_port_in_use", "unknown", "A data client owns the bridge; close that client before a separate CLI or maintenance session")
	}
	backendHints := map[string]string{
		"serial_port_busy":             "Close the other serial owner, then connect explicitly",
		"serial_port_missing":          "Enumerate and select the application port again",
		"permission_denied":            "Check OS device/Bluetooth permissions for the bridge process",
		"serial_access_denied_or_busy": "Check Windows serial ownership and permissions; access denied does not distinguish them",
		"adapter_not_ready":            "Check Bluetooth adapter power and readiness",
		"adapter_service_unavailable":  "Check the OS Bluetooth service and adapter",
	}
	if hint, ok := backendHints[status.ErrorCode]; ok {
		add(status.ErrorCode, "fail", hint)
	} else if status.Error != "" {
		// Error text is backend-controlled and may contain identifiers; do not echo it.
		add("backend_error", "unknown", "Bridge reports a backend failure; run the local Python doctor and inspect its local log")
	}
	add("management_ready", "pass", "Authenticated status and transport policy validated")
	add("hardware_unverified", "unknown", "No device was opened; driver, adapter, card and firmware behavior remain unverified")
	return out
}

func (b *USBBridge) Diagnose(ctx context.Context) BridgeDiagnostic { return b.control.Diagnose(ctx) }
