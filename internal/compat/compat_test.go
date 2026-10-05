package compat

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func invoke(t *testing.T, alias string, args ...string) (int, []byte, string) {
	t.Helper()
	var out, err bytes.Buffer
	code := Run(context.Background(), alias, args, &out, &err)
	return code, out.Bytes(), err.String()
}

func TestLegacyRejectionsAndDiscovery(t *testing.T) {
	for _, alias := range []string{"create-yss-spec", "create-yss-harness-design", "create-yss-harness-backend", "create-yss-harness-frontend"} {
		t.Run(alias, func(t *testing.T) {
			code, out, _ := invoke(t, alias, "--version", "--json")
			if code != 0 {
				t.Fatalf("version: %d %s", code, out)
			}
			if alias == "create-yss-spec" {
				if string(out) != "create-yss-spec 3.5.10\n" {
					t.Fatalf("spec legacy version %s", out)
				}
			} else {
				var result map[string]any
				if json.Unmarshal(out, &result) != nil || result["schemaVersion"] != float64(1) || result["packageName"] != alias {
					t.Fatalf("version JSON %s", out)
				}
			}
			code, out, err := invoke(t, alias, "sync", "--json", "--unknown-option")
			if code != 1 {
				t.Fatalf("invalid flag exit %d", code)
			}
			var result map[string]any
			if json.Unmarshal(out, &result) != nil {
				t.Fatalf("error JSON: %s", out)
			}
			if alias == "create-yss-spec" {
				if err != "" || result["ok"] != false || result["error"].(map[string]any)["code"] != "YSS_ARGUMENT_INVALID" {
					t.Fatalf("spec error %s stderr=%q", out, err)
				}
			} else if result["code"] != "INVALID" || result["status"] != "error" || err != "未知参数: --unknown-option\n" {
				t.Fatalf("specialist error %s stderr=%q", out, err)
			}
			code, out, _ = invoke(t, alias, "sync", "--json", "--target-dir", t.TempDir())
			if code != 1 {
				t.Fatalf("unported sync exit %d: %s", code, out)
			}
			if !bytes.Contains(out, []byte("UNPORTED")) {
				t.Fatalf("must explicitly reject uncovered semantics: %s", out)
			}
		})
	}
}

func TestNativePlanAndApplyProtectFamilyAndProject(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "project")
	plan := filepath.Join(base, "plan.json")
	args := []string{"--native", "init", "--target-dir", root, "--project-name", "Compat", "--business-domain", "Data", "--plan", "--out", plan, "--json"}
	code, out, stderr := invoke(t, "create-yss-spec", args...)
	if code != 0 {
		t.Fatalf("plan: %s %s", out, stderr)
	}
	var envelope map[string]any
	if json.Unmarshal(out, &envelope) != nil || envelope["protocolVersion"] != float64(1) || envelope["profile"] != "spec" {
		t.Fatalf("native envelope %s", out)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("init plan wrote target")
	}
	code, out, _ = invoke(t, "create-yss-spec", "--native", "init", "--target-dir", root, "--apply", "--plan-file", plan, "--json")
	if code != 0 {
		t.Fatalf("apply: %s", out)
	}
	if _, err := os.Stat(filepath.Join(root, ".yss.json")); err != nil {
		t.Fatal(err)
	}
	code, out, _ = invoke(t, "create-yss-spec", "--native", "doctor", "--target-dir", root, "--json")
	if code != 0 || !bytes.Contains(out, []byte("go-hybrid")) {
		t.Fatalf("native doctor failed: %s", out)
	}
	code, out, _ = invoke(t, "create-yss-harness-backend", "--native", "diff", "--target-dir", root, "--json")
	if code != 1 || !bytes.Contains(out, []byte("IDENTITY")) {
		t.Fatalf("cross family accepted: %s", out)
	}
	code, out, _ = invoke(t, "create-yss-harness-backend", "--native", "migrate", "rollback", "--apply", "--target-dir", root, "--json")
	if code != 1 || !bytes.Contains(out, []byte("IDENTITY")) {
		t.Fatalf("cross family rollback accepted: %s", out)
	}
	code, out, _ = invoke(t, "create-yss-spec", "--native", "sync", "--target-dir", root, "--plan", "--json")
	if code != 0 {
		t.Fatalf("sync plan: %s", out)
	}
	code, out, _ = invoke(t, "create-yss-spec", "--native", "recover", "--target-dir", root, "--json")
	if code != 0 || !bytes.Contains(out, []byte("committed")) {
		t.Fatalf("recover preview: %s", out)
	}
	if _, err := os.Stat(filepath.Join(root, ".yss.json")); err != nil {
		t.Fatal("recover without apply changed project")
	}
	code, out, _ = invoke(t, "create-yss-spec", "--native", "sync", "--target-dir", root, "--force", "--json")
	if code != 1 || !bytes.Contains(out, []byte("UNPORTED")) {
		t.Fatalf("force silently dropped: %s", out)
	}
}

func TestFourNativeReadOnlyProfilePlans(t *testing.T) {
	for alias, profile := range map[string]string{"create-yss-spec": "spec", "create-yss-harness-design": "design", "create-yss-harness-backend": "backend", "create-yss-harness-frontend": "frontend"} {
		t.Run(alias, func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(base, "target")
			code, out, stderr := invoke(t, alias, "--native", "init", "--target-dir", root, "--project-name", "Compat plan", "--business-domain", "Data", "--plan", "--json")
			if code != 0 {
				t.Fatalf("native plan: %s %s", out, stderr)
			}
			var envelope map[string]any
			if json.Unmarshal(out, &envelope) != nil || envelope["profile"] != profile || envelope["protocolVersion"] != float64(1) {
				t.Fatalf("native profile envelope: %s", out)
			}
			result := envelope["result"].(map[string]any)
			changes := result["changes"].([]any)
			if len(changes) < 10 || len(result["conflicts"].([]any)) != 0 {
				t.Fatal("unexpected real snapshot plan")
			}
			if _, err = os.Stat(root); !os.IsNotExist(err) {
				t.Fatal("preview wrote target")
			}
			t.Logf("real %s init plan: %d changes, target absent", profile, len(changes))
		})
	}
}

func TestMain(m *testing.M) {
	// A real executable bridge target without depending on the parent's cmd wiring.
	if os.Getenv("YSS_COMPAT_API_HELPER") == "1" && len(os.Args) > 2 && os.Args[1] == "compat-api" {
		os.Exit(RunAPI(context.Background(), os.Args[2], os.Stdin, os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

func TestSynchronousJSBridge(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node required only for thin JS compatibility bridge verification")
	}
	packageDir := filepath.Join("..", "..", "compat", "spec-api")
	abs, err := filepath.Abs(packageDir)
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(node, "--test", filepath.Join(abs, "index.test.cjs"))
	command.Env = append(os.Environ(), "YSS_BINARY="+binary, "YSS_COMPAT_API_HELPER=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("JS contract tests: %v\n%s", err, output)
	}
}
