package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

func TestUpgradeRejectsProjectArgumentsAndInvalidTargetsWithoutNetworking(t *testing.T) {
	for _, args := range [][]string{
		{"upgrade", "--root", "/project", "--json"},
		{"upgrade", "apply", "--json"},
		{"upgrade", "--check", "--to", "1.1.0-alpha.1", "--json"},
		{"upgrade", "--check", "--to", "1.1.0", "--to", "1.2.0", "--json"},
		{"upgrade", "--to", "--json"},
		{"upgrade", "--check=invalid", "--json"},
	} {
		var out, stderr bytes.Buffer
		code := Run(context.Background(), args, &out, &stderr)
		if code == 0 {
			t.Fatalf("invalid upgrade accepted: %v", args)
		}
		var e struct{ Command, Status, Code string }
		if err := json.Unmarshal(out.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		if e.Command != "upgrade" || e.Status != "error" || e.Code != "ARGUMENT" && e.Code != "VERSION" {
			t.Fatalf("unexpected rejection: %+v %s", e, stderr.String())
		}
		if e.Code == "ARGUMENT" && code != 2 {
			t.Fatalf("upgrade argument exit = %d, wanted 2", code)
		}
	}
}
