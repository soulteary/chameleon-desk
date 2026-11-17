package chameleon

import "runtime"

// EnvironmentDiagnostic describes the core host, not a browser host or remote
// bridge host. Management status is the only optional bridge request.
type EnvironmentDiagnostic struct {
	Schema   int                     `json:"schema_version"`
	Core     BuildInfo               `json:"core_version"`
	OS       string                  `json:"os"`
	Arch     string                  `json:"arch"`
	Recovery RecoveryDiagnostic      `json:"recovery"`
	Bridge   *BridgeDiagnostic       `json:"bridge,omitempty"`
	Checks   []BridgeDiagnosticCheck `json:"checks"`
}

func DiagnoseEnvironment(tools string) EnvironmentDiagnostic {
	return EnvironmentDiagnostic{Schema: 1, Core: CoreBuildInfo(), OS: runtime.GOOS, Arch: runtime.GOARCH, Recovery: DiagnoseRecovery(tools), Checks: []BridgeDiagnosticCheck{
		{Code: "diagnostic_scope", Status: "pass", Message: "No worker execution, hardware open, firmware command or connection change is performed"},
		{Code: "bridge_host_environment", Status: "unknown", Message: "Running bridge host checks are reported only when advertised. Pre-start bind checks and unobserved OS/hardware permissions require its local Python doctor"},
	}}
}
