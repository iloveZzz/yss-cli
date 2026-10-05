package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestProgramRecoveryCLIRejectsProjectAndInstallArgumentsWithoutWrites(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "absent-tools")
	for _, action := range []string{"status", "recover", "rollback"} {
		for _, extra := range [][]string{{"--root", base}, {"--profile", "spec"}, {"--apply"}, {"--artifact", "package.tar.gz"}, {"--plan-file", "plan.json"}} {
			args := append([]string{"update", action, "--tool-root", root, "--json"}, extra...)
			var out, stderr bytes.Buffer
			if code := Run(context.Background(), args, &out, &stderr); code == 0 {
				t.Fatalf("ambiguous recovery action succeeded: %v", args)
			}
			var env map[string]any
			if err := json.Unmarshal(out.Bytes(), &env); err != nil || env["code"] != "ARGUMENT" {
				t.Fatalf("unexpected recovery refusal: %s / %v", out.Bytes(), err)
			}
			if _, err := os.Lstat(root); !os.IsNotExist(err) {
				t.Fatal("rejected recovery created tool root")
			}
		}
	}
}

func TestProgramRecoveryCLIRequiresToolRootAndKeepsAbsentRootReadOnly(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"recover", "rollback"} {
		var out, stderr bytes.Buffer
		if code := Run(context.Background(), []string{"update", action, "--json"}, &out, &stderr); code == 0 {
			t.Fatal("tool root must be explicit")
		}
		root := filepath.Join(base, action)
		out.Reset()
		stderr.Reset()
		code := Run(context.Background(), []string{"update", action, "--tool-root", root, "--json"}, &out, &stderr)
		var env map[string]any
		if err := json.Unmarshal(out.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		if action == "recover" && (code != 0 || env["status"] != "ok") {
			t.Fatalf("empty recovery should be unchanged: %s", out.Bytes())
		}
		if action == "rollback" && code == 0 {
			t.Fatal("empty rollback must not manufacture a success")
		}
		if _, err := os.Lstat(root); !os.IsNotExist(err) {
			t.Fatal("absent tool root was created by recovery")
		}
	}
}
