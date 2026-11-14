package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	chameleon "github.com/soulteary/chameleon-desk"
)

func runFeatures(args []string) error {
	f := flag.NewFlagSet("features", flag.ContinueOnError)
	surface := f.String("surface", "cli", "web, cli or sdk")
	port := f.String("port", "", "explicit device port; omitted means no device I/O")
	id := f.String("id", "", "one feature ID; omitted lists the catalog")
	writes := f.Bool("allow-writes", false, "evaluate write workflows as permitted; does not execute writes")
	bridge := f.Bool("bridge-configured", false, "evaluate with a managed bridge configured; does not connect")
	timeout := f.Duration("timeout", 5*time.Second, "device query timeout")
	_ = f.Bool("json", true, "JSON output")
	if err := f.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if f.NArg() != 0 || *timeout <= 0 {
		return fmt.Errorf("features requires no positional arguments and a positive timeout")
	}
	if *surface != "web" && *surface != "cli" && *surface != "sdk" {
		return fmt.Errorf("surface must be web, cli or sdk")
	}
	if *id != "" {
		found := false
		for _, f := range chameleon.Features() {
			if f.ID == *id {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("unknown feature %q", *id)
		}
	}
	facts := chameleon.FeatureContext{WritesAllowed: *writes, BridgeConfigured: *bridge}
	if *port != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 3**timeout)
		defer cancel()
		c, e := chameleon.OpenContext(ctx, *port, *timeout)
		if e != nil {
			return e
		}
		defer c.Close()
		d, e := c.FeatureDevice(ctx)
		if e != nil {
			return e
		}
		facts.Device = &d
	}
	if *id != "" {
		v, e := chameleon.EvaluateFeature(*id, *surface, facts, nil)
		if e != nil {
			return e
		}
		return printJSON(v)
	}
	states, e := chameleon.EvaluateFeatures(*surface, facts)
	if e != nil {
		return e
	}
	return printJSON(map[string]any{"schema_version": chameleon.FeatureSchemaVersion, "features": chameleon.Features(), "states": states, "device": facts.Device})
}
