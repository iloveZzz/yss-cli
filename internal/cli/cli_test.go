package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStableJSONSuccessErrorAndVersionWithoutProject(t *testing.T) {
	for _, args := range [][]string{{"version", "--json"}, {"--version", "--json"}, {"context", "--root", "/does/not/exist", "--json"}, {"init", "--profile", "unknown", "--json"}} {
		var out, stderr bytes.Buffer
		code := Run(context.Background(), args, &out, &stderr)
		var result map[string]any
		if e := json.Unmarshal(out.Bytes(), &result); e != nil {
			t.Fatalf("non-JSON response %v: %s", args, out.String())
		}
		for _, key := range []string{"outputVersion", "version", "protocolVersion", "command", "profile", "status", "code", "result"} {
			if _, ok := result[key]; !ok {
				t.Fatalf("missing %s", key)
			}
		}
		if code != 0 && result["status"] != "error" {
			t.Fatal("error envelope mismatch")
		}
	}
}

func TestMalformedJSONCommandsCannotCreateProject(t *testing.T) {
	base, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	root := filepath.Join(base, "project")
	for _, args := range [][]string{{"init", "--json", "--root"}, {"init", "unknown", "--root", root, "--profile", "spec", "--json"}, {"migrate", "unknown", "--root", root, "--json"}} {
		var out, stderr bytes.Buffer
		if code := Run(context.Background(), args, &out, &stderr); code == 0 {
			t.Fatal("invalid command succeeded")
		}
		var env map[string]any
		if e := json.Unmarshal(out.Bytes(), &env); e != nil {
			t.Fatalf("lost JSON error: %q %q", out.String(), stderr.String())
		}
		if env["code"] != "ARGUMENT" {
			t.Fatalf("unexpected code: %v", env)
		}
		if _, e := os.Stat(root); !os.IsNotExist(e) {
			t.Fatal("rejected command wrote project")
		}
	}
}
