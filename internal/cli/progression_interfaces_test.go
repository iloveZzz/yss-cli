package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// This public test consumes the embedded fixed Bundle without policy injection.
func TestLifecycleTargetPublicBundlePlanApplyStatus(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "spec")
	run := func(args ...string) map[string]any {
		t.Helper()
		var out, stderr bytes.Buffer
		if code := Run(context.Background(), append(args, "--json"), &out, &stderr); code != 0 {
			t.Fatalf("%v exit=%d out=%s stderr=%s", args, code, out.String(), stderr.String())
		}
		var e map[string]any
		if err := json.Unmarshal(out.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		return e["result"].(map[string]any)
	}
	run("init", "--profile", "spec", "--root", root)
	cp := ".work/unrelated-name/checkpoint.yaml"
	put := func(ref, data string) {
		t.Helper()
		file := filepath.Join(root, ref)
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	put(cp, "schema_version: 1\nrepository_mode: project-instance\nprofile_id: harness.spec-template\nfeature_id: feature.public\nstage: stage.entry-triage\nnext_work_unit: work-unit.plan-opportunity\nblockers: []\ngates: {}\n")
	put(".work/unrelated-name/map.md", "---\ncheckpoint_ref: "+cp+"\n---\n# 已登记功能\n")
	before, err := os.ReadFile(filepath.Join(root, cp))
	if err != nil {
		t.Fatal(err)
	}
	result := run("lifecycle", "target", "--root", root, "--checkpoint", cp)
	p := result["progression"].(map[string]any)
	if p["target"] != "business-accepted" || p["enabled"] != true || p["config_digest"] != nil || result["action"] != "target" {
		t.Fatalf("native default: %#v", result)
	}
	put("target-input.json", "{\"schema_version\":1,\"kind\":\"lifecycle-progression-target\",\"feature_id\":\"feature.public\",\"checkpoint_ref\":\""+cp+"\",\"target\":\"spec-approved\",\"intent_source\":\"已确认本功能终点\",\"consumers\":[]}")
	file := filepath.Join(base, "target-plan.json")
	run("lifecycle", "target", "--root", root, "--checkpoint", cp, "--input", "target-input.json", "--plan", "--out", file)
	if _, err := os.Stat(filepath.Join(root, ".work/unrelated-name/progression-target.json")); !os.IsNotExist(err) {
		t.Fatal("plan wrote intent")
	}
	run("lifecycle", "target", "--root", root, "--apply", "--plan-file", file)
	run("lifecycle", "target", "--root", root, "--apply", "--plan-file", file)
	result = run("lifecycle", "status", "--root", root, "--checkpoint", cp)
	p = result["progression"].(map[string]any)
	if p["target"] != "spec-approved" || p["status"] != "pending" || result["next_work_unit"] != "work-unit.plan-opportunity" || result["read_only"] != true {
		t.Fatalf("status: %#v", result)
	}
	after, _ := os.ReadFile(filepath.Join(root, cp))
	if !bytes.Equal(before, after) {
		t.Fatal("target update changed checkpoint approval closure")
	}
	run("rollback", "--root", root, "--apply")
	if _, err := os.Stat(filepath.Join(root, ".work/unrelated-name/progression-target.json")); !os.IsNotExist(err) {
		t.Fatal("public rollback retained intent")
	}
}
