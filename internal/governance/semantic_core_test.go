package governance

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
)

func TestSemanticSameBytesReplacementIsInputDrift(t *testing.T) {
	root := semanticTestRoot(t)
	apTestPut(t, root, "evidence.json", []byte(`{"status":"passed"}`))
	s := newSemanticSession(context.Background(), root, nil)
	if _, err := s.bytes("evidence.json"); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "evidence.json")
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(root, "replacement.json")
	if err = os.WriteFile(other, []byte(`{"status":"passed"}`), info.Mode()); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(other, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(other, file); err != nil {
		t.Fatal(err)
	}
	apTestCode(t, s.finish(), "INPUT_DRIFT")
}

func semanticTestRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestSemanticOnlineRecheckPrecedesAllFileRechecks(t *testing.T) {
	root := semanticTestRoot(t)
	apTestPut(t, root, "parent.json", "old")
	s := newSemanticSession(context.Background(), root, nil)
	if _, err := s.bytes("parent.json"); err != nil {
		t.Fatal(err)
	}
	child := newSemanticSession(context.Background(), root, nil)
	s.children = append(s.children, child)
	calls := 0
	if err := child.observeOnline("synthetic-fact", func() error {
		calls++
		if calls > 1 {
			apTestPut(t, root, "parent.json", "new")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	apTestCode(t, s.finish(), "INPUT_DRIFT")
	if calls != 2 {
		t.Fatalf("fact not rechecked: %d", calls)
	}
}

func TestSemanticOnlineDuplicateIdentityCannotHideEarlierFacts(t *testing.T) {
	s := newSemanticSession(context.Background(), semanticTestRoot(t), nil)
	changed := false
	if err := s.observeOnline("same-fact", func() error {
		if changed {
			return s.reject("FACT_DRIFT", "changed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.observeOnline("same-fact", func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	changed = true
	apTestCode(t, s.finish(), "FACT_DRIFT")
}

func TestSemanticCoincidentViewsCannotHideEarlierObservation(t *testing.T) {
	root := semanticTestRoot(t)
	apTestPut(t, root, "input.json", "old")
	s := newSemanticSession(context.Background(), root, nil)
	if _, err := s.bytes("input.json"); err != nil {
		t.Fatal(err)
	}
	apTestPut(t, root, "input.json", "new")
	v := newView(root)
	if _, err := v.read("input.json"); err != nil {
		t.Fatal(err)
	}
	s.externalViews[root] = v
	apTestCode(t, s.finish(), "INPUT_DRIFT")
}

func TestSemanticRejectsUnknownLegacyMetadataInObservedView(t *testing.T) {
	for _, name := range []string{"spec", "design", "backend", "frontend"} {
		t.Run(name, func(t *testing.T) {
			for _, change := range []string{"known", "unknown-version", "wrong-source", "missing-commit", "missing-baseline"} {
				t.Run(change, func(t *testing.T) {
					root := ciProfileTestRoot(t, name)
					profile := domain.Profiles[name]
					version := 2
					if name == "spec" {
						version = 3
					}
					meta := map[string]any{"metadataSchemaVersion": version, "templateSource": profile.TemplateSource, "profileId": profile.ID, "templateCommit": strings.Repeat("a", 40), "managedFiles": map[string]any{}, "baselineDigest": "44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a"}
					switch change {
					case "unknown-version":
						meta["metadataSchemaVersion"] = 99
					case "wrong-source":
						meta["templateSource"] = "unknown"
					case "missing-commit":
						delete(meta, "templateCommit")
					case "missing-baseline":
						delete(meta, "managedFiles")
					}
					apTestPut(t, root, profile.Metadata, meta)
					s := newSemanticSession(context.Background(), root, nil)
					err := s.authorities()
					if change == "known" {
						if err != nil {
							t.Fatal(err)
						}
					} else {
						apTestCode(t, err, "IDENTITY")
					}
					if _, ok := s.v.observed[profile.Metadata]; !ok {
						t.Fatal("metadata not bound")
					}
				})
			}
		})
	}
}

func TestSemanticRegisteredExternalAbsenceIsObserved(t *testing.T) {
	for _, change := range []string{"stable", "target", "parent", "symlink"} {
		t.Run(change, func(t *testing.T) {
			root := semanticTestRoot(t)
			apTestPut(t, root, "registration.json", map[string]any{"target_parent": "test-only"})
			target := filepath.Join(root, "future", "service")
			s := newSemanticSession(context.Background(), root, nil)
			if err := s.observedExternalAbsence(target, "registration.json"); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "target":
				if err := os.MkdirAll(target, 0755); err != nil {
					t.Fatal(err)
				}
			case "parent":
				if err := os.Mkdir(filepath.Dir(target), 0755); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(root, filepath.Dir(target)); err != nil {
					t.Fatal(err)
				}
			}
			if change == "stable" {
				if err := s.finish(); err != nil {
					t.Fatal(err)
				}
			} else {
				apTestCode(t, s.finish(), "INPUT_DRIFT")
			}
		})
	}
}

func TestCompleteProjectCIInitialFourProfilesWithoutInterpreters(t *testing.T) {
	t.Setenv("PATH", "")
	for _, profile := range []string{"spec", "design", "backend", "frontend"} {
		t.Run(profile, func(t *testing.T) {
			root := semanticTestRoot(t)
			b, err := bundle.Load(profile)
			if err != nil {
				t.Fatal(err)
			}
			for ref, f := range b.Files {
				if strings.HasPrefix(ref, ".template-spec/process/schemas/") || semHas([]string{".template-spec/process/harness-profile.yaml", approvalRegistryRef, approvalRolesRef, approvalSkillsRef, "CONTEXT.md", "AGENTS.md", ".template-spec/agents/issue-tracker.md"}, ref) {
					raw, e := f.Render(map[string]string{"projectName": "synthetic-governance", "businessDomain": "test-only", "teamSize": "2"})
					if e != nil {
						t.Fatal(e)
					}
					apTestPut(t, root, ref, raw)
				}
			}
			apTestPut(t, root, "yss-project.yaml", map[string]any{"schema_version": 1, "repository_mode": "project-instance"})
			apTestPut(t, root, ".template-spec/process/harness-profile.yaml", map[string]any{"schema_version": 2, "profile_id": domain.Profiles[profile].ID})
			result, err := fullProjectCIRun(context.Background(), "check", root, map[string]string{"profile": profile})
			if err != nil {
				t.Fatal(err)
			}
			report := result.(*SemanticReport)
			if report.ApprovalCreated || !report.ReadOnly || report.Scope != "complete-governance" || report.Status != "passed" {
				t.Fatalf("scope/report: %+v", report)
			}
			roles, err := load(root, approvalRolesRef)
			if err != nil {
				t.Fatal(err)
			}
			delete(roles, "user_decision_policy")
			apTestPut(t, root, approvalRolesRef, roles)
			result, err = fullProjectCIRun(context.Background(), "check", root, nil)
			apTestCode(t, err, "PROJECT_CI_REJECTED")
			report = result.(*SemanticReport)
			if semanticExit(err) != 2 || report.Status != "error" || len(report.Diagnostics) == 0 || report.Diagnostics[0].Code != "CAPABILITY" {
				t.Fatalf("missing policy must remain capability error: %v %+v", err, report)
			}
		})
	}
}

func TestSemanticObservedMissingAndMembershipDrift(t *testing.T) {
	for _, change := range []string{"stable-missing", "added", "deleted", "replaced", "bytes", "mode"} {
		t.Run(change, func(t *testing.T) {
			root := semanticTestRoot(t)
			if change != "stable-missing" && change != "added" {
				apTestPut(t, root, "assets/a.yaml", "value: old\n")
			}
			s := newSemanticSession(context.Background(), root, nil)
			if _, err := s.scan("assets"); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "added":
				apTestPut(t, root, "assets/a.yaml", "value: new\n")
			case "deleted":
				if err := os.Remove(filepath.Join(root, "assets/a.yaml")); err != nil {
					t.Fatal(err)
				}
			case "replaced":
				if err := os.Remove(filepath.Join(root, "assets/a.yaml")); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filepath.Join(root, "assets/a.yaml"), 0755); err != nil {
					t.Fatal(err)
				}
			case "bytes":
				apTestPut(t, root, "assets/a.yaml", "value: new\n")
			case "mode":
				if err := os.Chmod(filepath.Join(root, "assets/a.yaml"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			err := s.finish()
			if change == "stable-missing" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var d *domain.Error
			if !errors.As(err, &d) || d.Code != "INPUT_DRIFT" || d.Exit != 2 {
				t.Fatalf("unobserved %s drift: %v", change, err)
			}
		})
	}
}

func TestSemanticSchemaReferenceClosureBoundToReadView(t *testing.T) {
	root := semanticTestRoot(t)
	apTestPut(t, root, "schemas/main.json", `{"$schema":"https://json-schema.org/draft/2020-12/schema","$ref":"value.json"}`)
	apTestPut(t, root, "schemas/value.json", `{"type":"string"}`)
	s := newSemanticSession(context.Background(), root, nil)
	if err := s.validateSchema("schemas/main.json", "ok"); err != nil {
		t.Fatal(err)
	}
	apTestPut(t, root, "schemas/value.json", `{"type":"number"}`)
	var d *domain.Error
	if err := s.finish(); !errors.As(err, &d) || d.Code != "INPUT_DRIFT" || d.Exit != 2 {
		t.Fatalf("Schema dependency drift missed: %v", err)
	}
}

func TestSemanticSourceAliasesRemainObservedAndManifestBound(t *testing.T) {
	root := semanticTestRoot(t)
	apTestPut(t, root, "package/payload/rules/a.yaml", "value: source\n")
	s := newSemanticSession(context.Background(), root, nil)
	child, err := s.sourceSnapshotSession("package", map[string]string{"rules": "payload/rules", "rules/a.yaml": "payload/rules/a.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := child.scan("rules")
	if err != nil || len(rows) != 1 || rows[0] != "rules/a.yaml" {
		t.Fatalf("logical scan: %v %v", rows, err)
	}
	value, err := child.doc("rules/a.yaml")
	if err != nil || value["value"] != "source" {
		t.Fatalf("source alias: %v %v", value, err)
	}
	if err = s.finish(); err != nil {
		t.Fatal(err)
	}
	apTestPut(t, root, "package/payload/rules/extra.yaml", "value: undeclared\n")
	if err = s.finish(); err == nil {
		t.Fatal("source manifest scan accepted added file")
	}
	other := newSemanticSession(context.Background(), root, nil)
	child, err = other.sourceSnapshotSession("package", map[string]string{"rules": "payload/rules", "rules/a.yaml": "payload/rules/a.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = child.scan("rules"); err == nil {
		t.Fatal("unregistered physical file accepted")
	}
}

func TestSemanticCancelledFinalReadRefusesPass(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := newSemanticSession(ctx, semanticTestRoot(t), nil)
	if _, err := s.scan("missing"); err != nil {
		t.Fatal(err)
	}
	cancel()
	var d *domain.Error
	if err := s.finish(); !errors.As(err, &d) || d.Code != "CANCELLED" || d.Exit != 2 {
		t.Fatalf("cancel accepted: %v", err)
	}
}

func TestSemanticPublicKindAndSchemaCannotBypassDomainRules(t *testing.T) {
	for _, args := range []map[string]string{{"kind": "checkpoint-boundary", "file": "input.yaml"}, {"kind": "approval", "file": "input.yaml"}, {"kind": "slice", "file": "input.yaml", "schema": "loose.json"}, {"kind": "slice", "file": "input.yaml", "continuation": "true"}, {"kind": "slice", "file": "input.yaml", "phase": "contract"}, {"kind": "task", "file": "input.yaml", "slice": "slice.demo"}} {
		result, err := semanticRun(context.Background(), "contract", "verify", semanticTestRoot(t), args)
		var d *domain.Error
		if !errors.As(err, &d) || d.Exit != 2 || d.Code != "ARGUMENT" {
			t.Fatalf("invalid public type/override accepted: %v", err)
		}
		report := result.(*SemanticReport)
		if report.ApprovalCreated || !report.ReadOnly || len(report.Diagnostics) == 0 {
			t.Fatalf("failure report contract: %+v", report)
		}
	}
}

func TestSemanticPublicVerificationCannotSelectInternalExpectations(t *testing.T) {
	root := apTestRoot(t)
	for _, flag := range []string{"subject-digest", "bundle-digest", "inputs-only", "completed", "unknown-flag"} {
		result, err := RunContext(context.Background(), "evidence", "verify", root, map[string]string{"kind": "verification", "file": "candidate.json", flag: "synthetic"})
		apTestCode(t, err, "ARGUMENT")
		report := result.(*SemanticReport)
		if report.ApprovalCreated || !report.ReadOnly || semanticExit(err) != 2 {
			t.Fatalf("unsafe public selector: %+v %v", report, err)
		}
	}
}
