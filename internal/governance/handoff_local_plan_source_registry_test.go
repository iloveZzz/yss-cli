package governance

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Reuse the retained native export's original schema1 Plan approval and policy.
func TestHandoffSchema1LocalPlanCapturedSourceRegistry(t *testing.T) {
	fixture := os.Getenv("YSS_LOCAL_PLAN_HANDOFF_ROOT")
	if fixture == "" {
		t.Skip("actual native local Plan Handoff fixture not configured")
	}
	const packageRef = "native-terminal-package"
	const registryRef = ".template-spec/process/lifecycle-registry.yaml"
	beforeFixture := progressionInventory(t, fixture)
	t.Cleanup(func() {
		if !reflect.DeepEqual(beforeFixture, progressionInventory(t, fixture)) {
			t.Error("verification modified the retained original fixture")
		}
	})
	for _, variant := range []string{"public-current", "sparse-source-current", "missing-source-registry", "changed-source-registry"} {
		t.Run(variant, func(t *testing.T) {
			root := handoffLocalPlanSourceCopy(t, fixture)
			captured := filepath.Join(root, packageRef, "payload/files", filepath.FromSlash(registryRef))
			switch variant {
			case "missing-source-registry":
				if err := os.Remove(captured); err != nil {
					t.Fatal(err)
				}
			case "changed-source-registry":
				raw, err := os.ReadFile(captured)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(captured, append(raw, '\n'), 0644); err != nil {
					t.Fatal(err)
				}
			}
			before := progressionInventory(t, root)
			if variant == "sparse-source-current" {
				parent := apTestSession(t, root)
				manifest, err := parent.doc(packageRef + "/manifest.json")
				if err != nil {
					t.Fatal(err)
				}
				aliases := map[string]string{}
				for _, value := range semList(manifest["files"]) {
					file := semMap(value)
					if original := text(file["original_ref"]); original != "" {
						aliases[original] = strings.TrimPrefix(text(file["path"]), "payload/files/")
					}
				}
				source, err := parent.sourceSnapshotSession(packageRef+"/payload/files", aliases)
				if err != nil {
					t.Fatal(err)
				}
				source.roles, err = source.doc(".template-spec/agents/digital-human-roles.yaml")
				if err != nil {
					t.Fatal(err)
				}
				// The package reader initially publishes only signing boundaries.
				published := []any{}
				for _, bucket := range []string{"dual_digital_human", "digital_human_review", "check_reviews"} {
					for _, value := range semList(semMap(source.roles["gate_policy"])[bucket]) {
						published = append(published, map[string]any{"id": semMap(value)["gate"]})
					}
				}
				source.registry = map[string]any{"gates": published, "id_policy": map[string]any{"deprecated_ids": []any{}}}
				sparse := contractCopy(source.registry)
				record, err := source.doc("local-plan-approval.json")
				if err != nil || contractN(record["schema_version"]) != 1 || record["gate_id"] != "gate.plan-approved" {
					t.Fatalf("fixture does not carry a current schema1 bounded Plan: %v", err)
				}
				bounded := false
				for _, value := range semList(semMap(semMap(source.roles["gate_policy"])["review_execution"])["review_bundles"]) {
					row := semMap(value)
					bounded = bounded || row["aggregate_gate"] == "gate.plan-approved" && row["aggregate_additional_review_task"] == "forbidden"
				}
				if !bounded {
					t.Fatal("original source policy does not require bounded Plan aggregate verification")
				}
				handoff, err := source.doc(text(manifest["handoff_ref"]))
				if err != nil {
					t.Fatal(err)
				}
				binding := semMap(semMap(handoff["source"])["domain_strategy_ref"])
				if err = contractSourceApproval(source, "gate.plan-approved", map[string]any{"ref": binding["persisted_ref"], "approval_context": binding["approval_context"]}, "local-plan-approval.json"); err != nil {
					t.Fatal(err)
				}
				if !contractSame(sparse, source.registry) {
					t.Fatal("source approval replaced the package reader's registry state")
				}
				if err = parent.finish(); err != nil {
					t.Fatal(err)
				}
			} else {
				_, err := RunContext(context.Background(), "handoff", "verify", root, map[string]string{"kind": "package", "package": packageRef})
				if variant == "public-current" && err != nil {
					t.Fatalf("public current local Plan package: %v", err)
				}
				if variant != "public-current" && err == nil {
					t.Fatal("receiver registry substituted for missing or changed captured source registry")
				}
			}
			if !reflect.DeepEqual(before, progressionInventory(t, root)) {
				t.Fatal("source verification modified evidence bytes or modes")
			}
		})
	}
}

func handoffLocalPlanSourceCopy(t *testing.T, source string) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(source, func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		ref, err := filepath.Rel(source, file)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		target := filepath.Join(root, ref)
		if entry.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(file)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
	return root
}
