package chameleon

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
)

// OperationSchemaVersion versions the server-owned task contract, independently
// of firmware commands and the broader feature inventory.
const OperationSchemaVersion = 1
const WriteRetryPolicy = "never"

//go:embed internal/operations/catalog.json
var operationCatalogJSON []byte

// OperationDefinition binds a task entry to the existing feature requirements.
// Scope defaults to session; offline scope is limited to non-device reads.
// A plan may refer to a write feature while Write remains false: generating a
// plan does not grant permission to execute it. Handlers retain typed validation
// and device-side guards. The registry is not arbitrary route execution.
type OperationDefinition struct {
	Scope           string            `json:"scope"`
	ID              string            `json:"id"`
	Path            string            `json:"path"`
	Method          string            `json:"method"`
	FeatureID       string            `json:"feature_id"`
	Write           bool              `json:"write"`
	SessionRequired bool              `json:"session_required"`
	RetryPolicy     string            `json:"retry_policy"`
	Feature         FeatureDefinition `json:"feature"`
}

var operationDefinitions = loadOperations()

func loadOperations() []OperationDefinition {
	var doc struct {
		Version    int                   `json:"schema_version"`
		Operations []OperationDefinition `json:"operations"`
	}
	if err := json.Unmarshal(operationCatalogJSON, &doc); err != nil {
		panic(err)
	}
	if doc.Version != OperationSchemaVersion {
		panic("unsupported operation catalog")
	}
	features := map[string]FeatureDefinition{}
	for _, f := range Features() {
		features[f.ID] = f
	}
	ids, paths := map[string]bool{}, map[string]bool{}
	for i := range doc.Operations {
		o := &doc.Operations[i]
		f, ok := features[o.FeatureID]
		if !ok || ids[o.ID] || paths[o.Path] {
			panic("invalid or duplicate operation: " + o.ID)
		}
		ids[o.ID] = true
		paths[o.Path] = true
		o.Feature = f
		if o.Scope == "" {
			o.Scope = "session"
		}
		if o.Scope != "session" && o.Scope != "offline" || o.Scope == "offline" && (f.Device || o.Write || f.Write) {
			panic("invalid operation scope: " + o.ID)
		}
		o.SessionRequired = o.Scope != "offline"
		o.RetryPolicy = WriteRetryPolicy
	}
	return doc.Operations
}
func cloneOperation(o OperationDefinition) OperationDefinition {
	o.Feature.Models = slices.Clone(o.Feature.Models)
	o.Feature.Commands = slices.Clone(o.Feature.Commands)
	o.Feature.Notes = slices.Clone(o.Feature.Notes)
	o.Feature.Sources = slices.Clone(o.Feature.Sources)
	o.Feature.Conditional = slices.Clone(o.Feature.Conditional)
	for i := range o.Feature.Conditional {
		o.Feature.Conditional[i].Commands = slices.Clone(o.Feature.Conditional[i].Commands)
	}
	cloneEntry := func(e FeatureEntry) FeatureEntry {
		e.Entries = slices.Clone(e.Entries)
		e.Controls = slices.Clone(e.Controls)
		return e
	}
	o.Feature.Web = cloneEntry(o.Feature.Web)
	o.Feature.CLI = cloneEntry(o.Feature.CLI)
	o.Feature.SDK = cloneEntry(o.Feature.SDK)
	return o
}

// Operations returns independent copies, including firmware/model/transport
// requirements from Features. CLI and SDK task clients use the same entries as
// the Web UI. This list does not register every low-level SDK method as a task.
func Operations() []OperationDefinition {
	out := make([]OperationDefinition, len(operationDefinitions))
	for i, o := range operationDefinitions {
		out[i] = cloneOperation(o)
	}
	return out
}
func LookupOperation(id string) (OperationDefinition, error) {
	for _, o := range operationDefinitions {
		if o.ID == id {
			return cloneOperation(o), nil
		}
	}
	return OperationDefinition{}, fmt.Errorf("unknown operation %q", id)
}
func OperationForPath(path string) (OperationDefinition, bool) {
	for _, o := range operationDefinitions {
		if o.Path == path {
			return cloneOperation(o), true
		}
	}
	return OperationDefinition{}, false
}
