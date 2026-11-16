package chameleon

import (
	"reflect"
	"testing"
)

func TestOperationsHaveUniqueTaskRoutesAndFeatureRequirements(t *testing.T) {
	features := map[string]FeatureDefinition{}
	for _, f := range Features() {
		features[f.ID] = f
	}
	ids, paths := map[string]bool{}, map[string]bool{}
	for _, o := range Operations() {
		if !reflect.DeepEqual(o.Feature, features[o.FeatureID]) {
			t.Fatal("feature requirements drifted", o.ID)
		}
		if ids[o.ID] || paths[o.Path] {
			t.Fatalf("duplicate operation: %+v", o)
		}
		ids[o.ID] = true
		paths[o.Path] = true
		if o.Feature.ID != o.FeatureID || !o.SessionRequired || o.RetryPolicy != "never" || (o.Method != "GET" && o.Method != "POST") || (o.Write && o.Method != "POST") {
			t.Fatalf("invalid operation: %+v", o)
		}
		if o.Write && !o.Feature.Write {
			t.Fatalf("write gate differs from feature: %s", o.ID)
		}
	}
	copy := Operations()
	copy[0].Feature.Models[0] = "changed"
	if Operations()[0].Feature.Models[0] == "changed" {
		t.Fatal("shared mutable metadata")
	}
	if _, e := LookupOperation("arbitrary.raw"); e == nil {
		t.Fatal("unregistered operation")
	}
	if _, ok := OperationForPath("/api/firmware/check"); ok {
		t.Fatal("firmware network check admitted as device task")
	}
}
