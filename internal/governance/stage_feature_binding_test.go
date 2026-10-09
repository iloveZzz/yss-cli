package governance

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

func TestStageRegisteredDesignAppliedTrackingNodeParity(t *testing.T) {
	tool := os.Getenv("YSS_LEGACY_ORACLE_ROOT")
	if tool == "" {
		t.Skip("set YSS_LEGACY_ORACLE_ROOT for installed Stage/Node caller parity")
	}
	const cpRef = "intake-checkpoint.json"
	root, _ := stageRegisteredFeatureFixture(t, "design", ".work", cpRef)
	cp := apTestCheckpoint(map[string]any{})
	cp["feature_id"], cp["stage"], cp["next_work_unit"] = "feature.supplier", "stage.plan", "work-unit.plan-requirements"
	apTestPut(t, root, cpRef, cp)
	stageRegisteredFeatureSeeds(t, root)
	result, err := stageRun(context.Background(), "register", root, map[string]string{"checkpoint": cpRef, "items": "stage-seeds.json"})
	if err != nil {
		t.Fatal(err)
	}
	apTestPut(t, root, "stage-plan.json", result.(WritePlan))
	if _, err = stageRun(context.Background(), "apply", root, map[string]string{"plan-file": "stage-plan.json"}); err != nil {
		t.Fatal(err)
	}
	before := progressionInventory(t, root)
	script := `import {pathToFileURL} from 'node:url';import fs from 'node:fs';import path from 'node:path';const [tool,root,ref]=process.argv.slice(1);const m=await import(pathToFileURL(path.join(tool,'scripts/lib/stage-tracking.mjs')));const cp=JSON.parse(fs.readFileSync(path.join(root,ref)));const result=m.assertStageTracking(cp,{root,checkpointRef:ref});if(result.status!=='valid')throw Error(JSON.stringify(result));`
	cmd := exec.Command("node", "--input-type=module", "-e", script, tool, root, cpRef)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Node rejected native applied current root Design tracking: %v: %s", err, out)
	}
	if !contractSame(before, progressionInventory(t, root)) {
		t.Fatal("Node Stage inspection wrote files")
	}
}

// Use the installed profile policy bytes rather than a test-only capability.
func stageRegisteredFeatureFixture(t *testing.T, profile, trackerRoot, cpRef string) (string, string) {
	t.Helper()
	root := apTestProfileRoot(t, profile)
	b, err := bundle.Load(profile)
	if err != nil {
		t.Fatal(err)
	}
	contractRef := guidanceContractRef(profile)
	f, ok := b.Files[contractRef]
	if !ok {
		t.Fatalf("installed profile lacks %s", contractRef)
	}
	raw, err := f.Render(map[string]string{"projectName": "registered-feature", "businessDomain": "test-only", "teamSize": "2"})
	if err != nil {
		t.Fatal(err)
	}
	apTestPut(t, root, contractRef, raw)
	if semMap(mustParseContract(raw))["progression_target"] == nil {
		t.Fatal("this new-capability test requires the fixed current Bundle; do not patch its policy")
	}
	apTestPut(t, root, trackerRef, "---\ntracker:\n  platform: local-markdown\n  root: "+trackerRoot+"\n---\n# Tracker\n")
	featureRoot := path.Join(trackerRoot, "supplier")
	apTestPut(t, root, featureRoot+"/map.md", "---\ncheckpoint_ref: "+cpRef+"\n---\n# 登记\n")
	apTestPut(t, root, cpRef, map[string]any{"schema_version": 1, "repository_mode": "project-instance", "feature_id": "feature.supplier", "stage": "stage.technical-analysis", "next_work_unit": "work-unit.technical-analysis", "gates": map[string]any{}, "blockers": []any{}})
	return root, featureRoot
}

func stageRegisteredFeatureSeeds(t *testing.T, root string) {
	t.Helper()
	apTestPut(t, root, "stage-seeds.json", []any{
		map[string]any{"id": "requirements", "title": "需求", "stage": "stage.plan", "work_unit": "work-unit.plan-requirements", "owner": "需求经理", "scope": "需求范围", "acceptance": []any{"明确范围"}, "source_refs": []any{"CONTEXT.md"}},
		map[string]any{"id": "decision", "title": "决定", "stage": "stage.plan", "work_unit": "work-unit.stage-decision", "owner": "架构师", "scope": "方案决定", "acceptance": []any{"明确决定"}, "source_refs": []any{"CONTEXT.md"}, "dependencies": []any{"requirements"}},
	})
}

func TestStageRegisteredFeaturePlansUseMapDirectory(t *testing.T) {
	for _, scenario := range []struct{ profile, tracker, checkpoint string }{
		{"spec", ".work", ".work/supplier/checkpoint.json"},
		{"spec", "docs/current-work", "docs/current-work/supplier/checkpoint.json"},
		{"design", ".work", "intake-checkpoint.json"},
	} {
		t.Run(scenario.profile+"/"+scenario.tracker, func(t *testing.T) {
			root, base := stageRegisteredFeatureFixture(t, scenario.profile, scenario.tracker, scenario.checkpoint)
			cp := apTestCheckpoint(map[string]any{})
			cp["feature_id"], cp["stage"], cp["next_work_unit"] = "feature.supplier", "stage.plan", "work-unit.plan-requirements"
			cp["gates"] = map[string]any{"retained.historical": map[string]any{"status": "pending", "ref": "unchanged.json"}}
			apTestPut(t, root, scenario.checkpoint, cp)
			stageRegisteredFeatureSeeds(t, root)
			before := progressionInventory(t, root)
			result, err := stageRun(context.Background(), "register", root, map[string]string{"checkpoint": scenario.checkpoint, "items": "stage-seeds.json"})
			if err != nil {
				t.Fatal(err)
			}
			plan := result.(WritePlan)
			if !contractSame(before, progressionInventory(t, root)) {
				t.Fatal("registration plan wrote business assets")
			}
			if plan.Options["stage_scan_inputs"] == "" {
				t.Fatal("current registration set was not bound to the plan")
			}
			definitions := 0
			for _, op := range plan.Operations {
				if strings.Contains(op.Path, "/work-items/") {
					definitions++
					if !strings.HasPrefix(op.Path, base+"/work-items/") {
						t.Fatalf("definition was inferred from ID: %s", op.Path)
					}
				}
			}
			if definitions != 2 {
				t.Fatalf("expected two actual work-item definitions, got %d", definitions)
			}
			apTestPut(t, root, "stage-plan.json", plan)
			if _, err = stageRun(context.Background(), "apply", root, map[string]string{"plan-file": "stage-plan.json"}); err != nil {
				t.Fatal(err)
			}
			actual, err := load(root, scenario.checkpoint)
			if err != nil || !contractSame(cp["gates"], actual["gates"]) {
				t.Fatalf("stage registration changed existing gates: %v", err)
			}
			tracking, err := asTracking(actual)
			if err != nil || tracking.FeatureID != "feature.supplier" || tracking.CheckpointRef != scenario.checkpoint {
				t.Fatalf("registered tracking identity changed: %#v %v", tracking, err)
			}
			if scenario.profile == "design" {
				if tracking.Entry.Kind != "checkpoint" || tracking.Entry.Ref != scenario.checkpoint {
					t.Fatalf("root-level Design entry changed: %#v", tracking.Entry)
				}
			} else if tracking.Entry.Ref != base+"/parent-ticket.md" {
				t.Fatalf("parent ticket was inferred from canonical ID: %#v", tracking.Entry)
			}
			after := progressionInventory(t, root)
			if _, err = stageRun(context.Background(), "status", root, map[string]string{"checkpoint": scenario.checkpoint}); err != nil {
				t.Fatal(err)
			}
			if !contractSame(after, progressionInventory(t, root)) {
				t.Fatal("current stage status wrote files")
			}
		})
	}
}

func TestStageRegisteredFeatureReadSeparatesIDAndDirectory(t *testing.T) {
	root, _ := stageRegisteredFeatureFixture(t, "spec", ".work", ".work/supplier/checkpoint.json")
	cpRef := ".work/supplier/checkpoint.json"
	before, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(cpRef)))
	if err != nil {
		t.Fatal(err)
	}
	result, err := stageRun(context.Background(), "status", root, map[string]string{"checkpoint": cpRef})
	if err != nil {
		t.Fatalf("unique registered canonical feature rejected: %v", err)
	}
	if result.(map[string]any)["status"] != "not-applicable" {
		t.Fatalf("unexpected stage result: %#v", result)
	}
	after, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(cpRef)))
	if err != nil || string(before) != string(after) {
		t.Fatalf("read changed checkpoint: %v", err)
	}
}

func TestStageRegisteredFeatureRejectsUntrustedMapsAndCapability(t *testing.T) {
	for _, variant := range []string{"missing-map", "duplicate-map", "other-checkpoint", "other-feature", "changed-tracker-root", "unknown-policy-version", "unknown-capability", "cancelled"} {
		t.Run(variant, func(t *testing.T) {
			ref := ".work/supplier/checkpoint.json"
			root, base := stageRegisteredFeatureFixture(t, "spec", ".work", ref)
			ctx := context.Background()
			code := "PROGRESSION_BINDING"
			switch variant {
			case "missing-map":
				if err := os.Remove(filepath.Join(root, base, "map.md")); err != nil {
					t.Fatal(err)
				}
			case "duplicate-map":
				apTestPut(t, root, ".work/duplicate/map.md", "---\ncheckpoint_ref: "+ref+"\n---\n")
				code = "PROFILE_INPUT_AMBIGUOUS"
			case "other-checkpoint", "other-feature":
				cp, err := load(root, ref)
				if err != nil {
					t.Fatal(err)
				}
				if variant == "other-feature" {
					cp["feature_id"] = "feature.other"
				} else {
					code = "PROFILE_INPUT_AMBIGUOUS"
				}
				apTestPut(t, root, "another-checkpoint.json", cp)
				apTestPut(t, root, base+"/map.md", "---\ncheckpoint_ref: another-checkpoint.json\n---\n")
			case "changed-tracker-root":
				apTestPut(t, root, trackerRef, "---\ntracker:\n  platform: local-markdown\n  root: docs/current-work\n---\n")
			case "unknown-policy-version", "unknown-capability":
				policy, err := load(root, guidanceContractRef("spec"))
				if err != nil {
					t.Fatal(err)
				}
				if variant == "unknown-policy-version" {
					semMap(policy["progression_target"])["schema_version"] = 2
				} else {
					semMap(policy["progression_target"])["required_capabilities"] = []any{"lifecycle-target-v1", "unknown-feature-registration"}
				}
				apTestPut(t, root, guidanceContractRef("spec"), policy)
				code = "CAPABILITY"
			case "cancelled":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
				code = "CANCELLED"
			}
			before := progressionInventory(t, root)
			_, err := stageRun(ctx, "status", root, map[string]string{"checkpoint": ref})
			apTestCode(t, err, code)
			if !contractSame(before, progressionInventory(t, root)) {
				t.Fatal("rejected binding query wrote files")
			}
		})
	}
}

func TestStageRegisteredFeatureMultipleFeaturesAndLegacyReadStayStrict(t *testing.T) {
	root, _ := stageRegisteredFeatureFixture(t, "spec", ".work", ".work/supplier/checkpoint.json")
	apTestPut(t, root, ".work/other/map.md", "---\ncheckpoint_ref: .work/other/checkpoint.json\n---\n")
	apTestPut(t, root, ".work/other/checkpoint.json", map[string]any{"feature_id": "feature.other"})
	if _, err := stageRun(context.Background(), "status", root, map[string]string{"checkpoint": ".work/supplier/checkpoint.json"}); err != nil {
		t.Fatalf("unrelated registered feature blocked explicit feature: %v", err)
	}
	for _, oldPolicy := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy-slug-with-current-policy", true: "legacy-no-policy"}[oldPolicy], func(t *testing.T) {
			root, _ := stageRegisteredFeatureFixture(t, "spec", ".work", ".work/supplier/checkpoint.json")
			cp, err := load(root, ".work/supplier/checkpoint.json")
			if err != nil {
				t.Fatal(err)
			}
			cp["feature_id"] = "supplier"
			apTestPut(t, root, ".work/supplier/checkpoint.json", cp)
			if oldPolicy {
				if err = os.Remove(filepath.Join(root, filepath.FromSlash(guidanceContractRef("spec")))); err != nil {
					t.Fatal(err)
				}
			}
			before := progressionInventory(t, root)
			if _, err = stageRun(context.Background(), "status", root, map[string]string{"checkpoint": ".work/supplier/checkpoint.json"}); err != nil {
				t.Fatal(err)
			}
			if !contractSame(before, progressionInventory(t, root)) {
				t.Fatal("legacy read enabled or rewrote current policy")
			}
		})
	}
}

func TestStageRegisteredFeaturePlanRejectsMapDriftAndCallbackMembership(t *testing.T) {
	ref := ".work/supplier/checkpoint.json"
	root, _ := stageRegisteredFeatureFixture(t, "spec", ".work", ref)
	cp, err := load(root, ref)
	if err != nil {
		t.Fatal(err)
	}
	cp["stage"], cp["next_work_unit"] = "stage.plan", "work-unit.plan-requirements"
	apTestPut(t, root, ref, cp)
	stageRegisteredFeatureSeeds(t, root)
	plan, err := buildStagePlan(root, "register", ref, "stage-seeds.json")
	if err != nil {
		t.Fatal(err)
	}
	apTestPut(t, root, "stage-plan.json", plan)
	apTestPut(t, root, ".work/second/map.md", "---\ncheckpoint_ref: "+ref+"\n---\n")
	before := progressionInventory(t, root)
	if _, err = stageRun(context.Background(), "apply", root, map[string]string{"plan-file": "stage-plan.json"}); err == nil {
		t.Fatal("duplicate registration added after plan accepted")
	}
	if !contractSame(before, progressionInventory(t, root)) {
		t.Fatal("rejected stale plan changed business files")
	}
	if err = os.RemoveAll(filepath.Join(root, ".work/second")); err != nil {
		t.Fatal(err)
	}
	// Also prove the transaction callback covers a late registration addition
	// which an ordinary per-file guard cannot observe.
	beforeCP := mustReadSpecBaselineTestFile(t, filepath.Join(root, ref))
	_, err = transaction.ApplyContextWithValidation(context.Background(), root, "stage-register", plan.Operations, plan.Observed, nil, func() error {
		apTestPut(t, root, ".work/unexpected/map.md", "---\ncheckpoint_ref: "+ref+"\n---\n")
		return validateStagePlanScans(context.Background(), root, plan)
	})
	apTestCode(t, err, "INPUT_DRIFT")
	if string(beforeCP) != string(mustReadSpecBaselineTestFile(t, filepath.Join(root, ref))) {
		t.Fatal("failed final membership check did not restore checkpoint")
	}
}

func TestStageRegisteredFeatureAliasViewKeepsSourceAndInputWatch(t *testing.T) {
	ref := ".work/supplier/checkpoint.json"
	root, _ := stageRegisteredFeatureFixture(t, "spec", ".work", ref)
	cp := apTestCheckpoint(map[string]any{})
	cp["feature_id"], cp["stage"], cp["next_work_unit"] = "feature.supplier", "stage.plan", "work-unit.plan-requirements"
	apTestPut(t, root, ref, cp)
	stageRegisteredFeatureSeeds(t, root)
	plan, err := buildStagePlan(root, "register", ref, "stage-seeds.json")
	if err != nil {
		t.Fatal(err)
	}
	apTestPut(t, root, "stage-plan.json", plan)
	if _, err = stageRun(context.Background(), "apply", root, map[string]string{"plan-file": "stage-plan.json"}); err != nil {
		t.Fatal(err)
	}
	s := newSemanticSession(context.Background(), root, nil)
	s.v.virtual = map[string]semanticArchiveFile{}
	err = filepath.WalkDir(root, func(file string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		local, err := filepath.Rel(root, file)
		if err != nil {
			return err
		}
		local = filepath.ToSlash(local)
		if local == ".yss/transactions" {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		stored := "frozen/" + local
		raw, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		s.v.virtual[stored] = semanticArchiveFile{Data: raw, Mode: uint32(info.Mode().Perm())}
		s.aliases[local] = stored
		for dir := path.Dir(local); dir != "."; dir = path.Dir(dir) {
			s.aliases[dir] = "frozen/" + dir
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	cp, err = s.doc(ref)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = checkStage(s.v, cp, ref, s); err != nil {
		t.Fatalf("bound virtual source aliases were replaced by physical paths: %v", err)
	}
	if err = s.finish(); err != nil {
		t.Fatal(err)
	}
	mapFile := s.v.virtual["frozen/.work/supplier/map.md"]
	mapFile.Data = append(append([]byte(nil), mapFile.Data...), []byte("\nchanged\n")...)
	s.v.virtual["frozen/.work/supplier/map.md"] = mapFile
	apTestCode(t, s.finish(), "INPUT_DRIFT")
}

func TestStageRegisteredFeatureCallbackRejectsForeignScanRoot(t *testing.T) {
	root := t.TempDir()
	data, err := json.Marshal([]progressionScanInput{{Root: t.TempDir(), Ref: ".work", Files: []string{}}})
	if err != nil {
		t.Fatal(err)
	}
	plan := WritePlan{Options: map[string]string{"stage_scan_inputs": string(data)}, Operations: []transaction.Operation{}}
	apTestCode(t, validateStagePlanScans(context.Background(), root, plan), "PLAN")
}
