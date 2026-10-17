// artifact-sizes measures public JSON after loading and validating the embedded
// candidate. Build-time only: JSON encoding here deliberately remains the oracle.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/iloveZzz/yss-cli/internal/bundle"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 1 {
		return fmt.Errorf("artifact-sizes accepts no arguments")
	}
	report := map[string]any{}
	for _, profile := range []string{"spec", "backend"} {
		b, err := bundle.Load(profile)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(b)
		if err != nil {
			return err
		}
		if len(raw)+1 > bundle.MaxSnapshotBytes {
			return fmt.Errorf("%s public snapshot exceeds %d bytes", profile, bundle.MaxSnapshotBytes)
		}
		report[profile] = map[string]any{"snapshotBytes": len(raw) + 1, "bundleHash": b.BundleHash, "templateCommit": b.TemplateCommit}
	}
	return json.NewEncoder(os.Stdout).Encode(report)
}
