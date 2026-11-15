package chameleon

import (
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

//go:embed tools/ble_bridge/version.json
var bridgeVersionManifest []byte

// DistributionRevision is set by the distribution builder. Empty means a source build.
var DistributionRevision string

type BuildInfo struct {
	Release       string `json:"release"`
	Compatibility int    `json:"compatibility"`
	Revision      string `json:"revision"`
}

func CoreBuildInfo() BuildInfo {
	var v BuildInfo
	if err := json.Unmarshal(bridgeVersionManifest, &v); err != nil {
		panic("invalid embedded bridge version manifest")
	}
	v.Revision = DistributionRevision
	return v
}

var ErrBridgeVersionMismatch = errors.New("bridge version mismatch; use the core and bridge from the same distribution")

// CheckBridgeVersion retains legacy peers without build metadata. Diagnostics
// report those peers as unverified. Advertised incompatible builds are rejected.
func CheckBridgeVersion(v *BuildInfo) error {
	return checkBridgeVersion(CoreBuildInfo(), v)
}

func checkBridgeVersion(core BuildInfo, v *BuildInfo) error {
	if v == nil {
		return nil
	}
	if v.Revision != "" {
		if _, err := hex.DecodeString(v.Revision); err != nil || len(v.Revision) != 40 {
			return ErrBridgeVersionMismatch
		}
	}
	if v.Release != core.Release || v.Compatibility != core.Compatibility ||
		(core.Revision != "" && v.Revision != "" && core.Revision != v.Revision) {
		return fmt.Errorf("%w", ErrBridgeVersionMismatch)
	}
	return nil
}
