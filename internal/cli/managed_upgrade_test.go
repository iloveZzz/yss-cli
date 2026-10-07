package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/project"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func managedCLI(t *testing.T, args ...string) (int, map[string]any) {
	t.Helper()
	var out, stderr bytes.Buffer
	args = append(args, "--json")
	code := Run(context.Background(), args, &out, &stderr)
	var result map[string]any
	if e := json.Unmarshal(out.Bytes(), &result); e != nil {
		t.Fatalf("JSON lost: %s %s", out.String(), stderr.String())
	}
	return code, result
}

func TestManagedUpgradeFourProfileTakeoverAndIdempotence(t *testing.T) {
	for _, profile := range []string{"spec", "design", "backend", "frontend"} {
		t.Run(profile, func(t *testing.T) {
			parent, _ := filepath.EvalSymlinks(t.TempDir())
			root := filepath.Join(parent, "project")
			if e := os.Mkdir(root, 0755); e != nil {
				t.Fatal(e)
			}
			business := filepath.Join(root, "business")
			if e := os.WriteFile(business, []byte("existing business"), 0751); e != nil {
				t.Fatal(e)
			}
			before, _ := safefs.Describe(root, "business")
			plan := filepath.Join(parent, "attach.json")
			preview := managedOK(t, "attach", "--root", root, "--profile", profile, "--plan", "--out", plan)
			if preview["readyToApply"] != true || preview["coverage"].(map[string]any)["percent"] != float64(100) {
				t.Fatal("incomplete first takeover")
			}
			managedOK(t, "attach", "--root", root, "--apply", "--plan-file", plan)
			code, env := managedCLI(t, "attach", "--root", root, "--full", "--plan")
			if code == 0 || env["code"] != "SYNC_REQUIRED" {
				t.Fatal("native attach routing failed")
			}
			p := managedOK(t, "sync", "--root", root, "--plan")
			if len(p["changes"].([]any)) != 0 || p["readyToApply"] != true {
				t.Fatal("repeat synchronization changed assets")
			}
			after, _ := safefs.Describe(root, "business")
			if before != after {
				t.Fatal("business bytes or mode changed")
			}
		})
	}
}

func TestManagedUpgradeKeepLocalPermission(t *testing.T) {
	parent, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(parent, "project")
	managedOK(t, "init", "--root", root, "--profile", "spec")
	if e := os.Chmod(filepath.Join(root, "AGENTS.md"), 0600); e != nil {
		t.Fatal(e)
	}
	planFile := filepath.Join(parent, "original.json")
	managedOK(t, "sync", "--root", root, "--plan", "--out", planFile)
	p, e := project.ReadPlan(planFile)
	if e != nil {
		t.Fatal(e)
	}
	var a project.AssetResult
	for _, asset := range p.Assets {
		if asset.Path == "AGENTS.md" {
			a = asset
		}
	}
	resolution := project.ResolutionFile{SchemaVersion: 1, PlanDigest: p.Digest, Decisions: []project.Resolution{{Path: a.Path, Choice: "keep-local", Before: a.Before, Target: a.Target}}}
	raw, _ := json.Marshal(resolution)
	decision := filepath.Join(parent, "decisions.json")
	if e := os.WriteFile(decision, raw, 0600); e != nil {
		t.Fatal(e)
	}
	resolved := filepath.Join(parent, "resolved.json")
	managedOK(t, "sync", "--root", root, "--plan", "--plan-file", planFile, "--resolution-file", decision, "--out", resolved)
	managedOK(t, "sync", "--root", root, "--apply", "--plan-file", resolved)
	after, _ := safefs.Describe(root, "AGENTS.md")
	if after != a.Before {
		t.Fatal("keep-local lost permission")
	}
	repeat := managedOK(t, "sync", "--root", root, "--plan")
	if len(repeat["changes"].([]any)) != 0 || repeat["readyToApply"] != true {
		t.Fatal("permission disposition was not stable")
	}
}
func managedOK(t *testing.T, args ...string) map[string]any {
	t.Helper()
	code, result := managedCLI(t, args...)
	if code != 0 {
		t.Fatalf("%v: %+v", args, result)
	}
	return result["result"].(map[string]any)
}
func TestManagedUpgradeReviewResolveApplyRepeatAndTampering(t *testing.T) {
	parent, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	root := filepath.Join(parent, "project")
	managedOK(t, "init", "--root", root, "--profile", "spec")
	code, env := managedCLI(t, "attach", "--root", root, "--full", "--plan")
	if code == 0 || env["code"] != "SYNC_REQUIRED" {
		t.Fatalf("identity must precede full selection: %+v", env)
	}
	local, e := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if e != nil {
		t.Fatal(e)
	}
	local = append(local, []byte("\n本项目入口定制。\n")...)
	if e = os.WriteFile(filepath.Join(root, "AGENTS.md"), local, 0644); e != nil {
		t.Fatal(e)
	}
	planFile := filepath.Join(parent, "plan.json")
	review := filepath.Join(parent, "review")
	preview := managedOK(t, "sync", "--root", root, "--plan", "--out", planFile, "--review-out", review)
	if preview["readyToApply"] != false || preview["schemaVersion"] != float64(2) {
		t.Fatalf("invalid readiness: %+v", preview)
	}
	p, e := project.ReadPlan(planFile)
	if e != nil {
		t.Fatal(e)
	}
	var asset project.AssetResult
	for _, a := range p.Assets {
		if a.Path == "AGENTS.md" {
			asset = a
		}
	}
	if asset.Candidate == nil || !asset.Candidate.Clean || !asset.BaselineAvailable {
		t.Fatalf("canonical base or clean merge absent: %+v", asset)
	}
	candidate := filepath.Join(review, "candidates", "AGENTS.md")
	r := project.Resolution{Path: asset.Path, Choice: "use-merged", Before: asset.Before, Target: asset.Target, CandidateFile: candidate, CandidateDigest: asset.Candidate.Digest}
	decision := project.ResolutionFile{SchemaVersion: 1, PlanDigest: p.Digest, Decisions: []project.Resolution{r}}
	raw, _ := json.Marshal(decision)
	decisionFile := filepath.Join(parent, "decisions.json")
	if e = os.WriteFile(decisionFile, raw, 0600); e != nil {
		t.Fatal(e)
	}
	resolvedFile := filepath.Join(parent, "resolved.json")
	resolved := managedOK(t, "sync", "--root", root, "--plan", "--plan-file", planFile, "--resolution-file", decisionFile, "--out", resolvedFile)
	if resolved["readyToApply"] != true {
		t.Fatalf("resolved plan blocked: %+v", resolved)
	}
	if e = os.WriteFile(candidate, []byte("tampered"), 0600); e != nil {
		t.Fatal(e)
	}
	code, env = managedCLI(t, "sync", "--root", root, "--apply", "--plan-file", resolvedFile)
	if code == 0 || env["code"] != "CANDIDATE_DRIFT" {
		t.Fatalf("candidate tampering accepted: %+v", env)
	}
	if e = os.WriteFile(candidate, local, 0600); e != nil {
		t.Fatal(e)
	}
	receipt := managedOK(t, "sync", "--root", root, "--apply", "--plan-file", resolvedFile)
	if receipt["fileApplication"] != "applied" || receipt["verification"] != "passed" {
		t.Fatalf("missing split receipt: %+v", receipt)
	}
	repeat := managedOK(t, "sync", "--root", root, "--plan")
	if len(repeat["changes"].([]any)) != 0 || repeat["readyToApply"] != true {
		t.Fatalf("accepted exception not stable: %+v", repeat)
	}
	actual, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if !bytes.Equal(actual, local) {
		t.Fatal("reviewed merged entry was lost")
	}
	meta, _ := os.ReadFile(filepath.Join(root, ".yss.json"))
	var identity map[string]any
	_ = json.Unmarshal(meta, &identity)
	if identity["schemaVersion"] != float64(3) {
		t.Fatal("metadata v3 missing")
	}
	// A valid checksum does not authorize a caller-created write in a saved plan.
	rebound, e := project.Build(root, "", "sync", nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	rebound.Changes = append(rebound.Changes, project.Change{Path: "src/business", Before: domain.Descriptor{Type: "missing"}, After: asset.Target, Data: asset.TargetData, Ownership: "managed"})
	rebound.Digest = ""
	raw, _ = json.Marshal(rebound)
	rebound.Digest = safefs.Digest(raw)
	file := filepath.Join(parent, "rebound.json")
	if e = project.SavePlan(rebound, file); e != nil {
		t.Fatal(e)
	}
	code, env = managedCLI(t, "sync", "--root", root, "--apply", "--plan-file", file)
	if code == 0 || env["code"] != "PLAN" {
		t.Fatalf("rehashed caller-written plan accepted: %+v", env)
	}
}
