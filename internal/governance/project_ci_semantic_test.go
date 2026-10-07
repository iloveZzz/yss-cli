package governance

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
)

func ciProfileTestRoot(t *testing.T, profile string) string {
	t.Helper()
	root := semanticTestRoot(t)
	b, e := bundle.Load(profile)
	if e != nil {
		t.Fatal(e)
	}
	for ref, f := range b.Files {
		if strings.HasPrefix(ref, ".template-spec/process/schemas/") || semHas([]string{approvalRegistryRef, approvalRolesRef, approvalSkillsRef, "CONTEXT.md", "AGENTS.md", ".template-spec/agents/issue-tracker.md", ".template-spec/process/checkpoint-boundary.yaml", ".agents/skills/yss-product-lifecycle/references/orchestration-contract.yaml"}, ref) {
			raw, e := f.Render(map[string]string{"projectName": "synthetic-ci", "businessDomain": "test-only", "teamSize": "2"})
			if e != nil {
				t.Fatal(e)
			}
			apTestPut(t, root, ref, raw)
		}
	}
	apTestPut(t, root, "yss-project.yaml", map[string]any{"schema_version": 1, "repository_mode": "project-instance"})
	apTestPut(t, root, ".template-spec/process/harness-profile.yaml", map[string]any{"schema_version": 2, "profile_id": domain.Profiles[profile].ID})
	return root
}

func ciTestGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = root
	c.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=", "GIT_AUTHOR_NAME=Synthetic", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=Synthetic", "GIT_COMMITTER_EMAIL=test@example.invalid")
	out, e := c.CombinedOutput()
	if e != nil {
		t.Fatalf("git %v: %v %s", args, e, out)
	}
	return strings.TrimSpace(string(out))
}

func TestCompleteProjectCIGitBaselineProtectsFourProfiles(t *testing.T) {
	for _, profile := range []string{"spec", "design", "backend", "frontend"} {
		t.Run(profile, func(t *testing.T) {
			for _, change := range []string{"unchanged", "deleted-approval", "deleted-evidence", "reduced-scope"} {
				t.Run(change, func(t *testing.T) {
					root := ciProfileTestRoot(t, profile)
					registeredTracker, e := tracker(newView(root))
					if e != nil {
						t.Fatal(e)
					}
					ticketRoot := strings.TrimSuffix(text(registeredTracker["root"]), "/")
					apTestPut(t, root, ciConfigRef, map[string]any{"schema_version": 1, "provider": "github", "branch": "main", "additional_paths": []any{"audit"}})
					apTestPut(t, root, "audit/keep.txt", "observed-scope")
					if change == "deleted-approval" || change == "deleted-evidence" {
						apTestPut(t, root, ticketRoot+"/historical-approval.json", map[string]any{"decision": "approved", "evidence_ref": "audit/evidence.txt"})
						apTestPut(t, root, "audit/evidence.txt", "historical-only")
					}
					ciTestGit(t, root, "init", "-q")
					ciTestGit(t, root, "add", ".")
					ciTestGit(t, root, "commit", "-qm", "synthetic baseline")
					base := ciTestGit(t, root, "rev-parse", "HEAD")
					// Test the base guard independently from validation of the synthetic
					// historical claim: old evidence cannot authorize current execution.
					switch change {
					case "deleted-approval":
						if e = os.Remove(filepath.Join(root, ticketRoot, "historical-approval.json")); e != nil {
							t.Fatal(e)
						}
					case "deleted-evidence":
						if e = os.Remove(filepath.Join(root, "audit/evidence.txt")); e != nil {
							t.Fatal(e)
						}
					case "reduced-scope":
						apTestPut(t, root, ciConfigRef, map[string]any{"schema_version": 1, "provider": "github", "branch": "main", "additional_paths": []any{}})
					}
					before := ciTestGit(t, root, "status", "--porcelain=v1")
					indexBefore := ciTestGit(t, root, "ls-files", "--stage")
					s := newSemanticSession(context.Background(), root, nil)
					roots, _, e := governanceScope(s.v)
					if e != nil {
						t.Fatal(e)
					}
					e = s.ciBaseProtection(base, roots, map[string]map[string]any{}, nil)
					switch change {
					case "unchanged":
						if e != nil {
							t.Fatal(e)
						}
					case "reduced-scope":
						apTestCode(t, e, "CI_SCOPE_REDUCED")
					default:
						apTestCode(t, e, "APPROVED_EVIDENCE_DELETED")
					}
					if e = s.finish(); e != nil {
						t.Fatal(e)
					}
					for _, action := range []string{"check", "verify"} {
						result, err := RunContext(context.Background(), "project-ci", action, root, map[string]string{"base": base, "runtime-store": "off"})
						report, ok := result.(*SemanticReport)
						if !ok || report.ApprovalCreated || !report.ReadOnly {
							t.Fatalf("missing readonly report: %#v", result)
						}
						if change == "unchanged" {
							if err != nil {
								t.Fatal(err)
							}
						} else {
							apTestCode(t, err, "PROJECT_CI_REJECTED")
							wanted := "APPROVED_EVIDENCE_DELETED"
							if change == "reduced-scope" {
								wanted = "CI_SCOPE_REDUCED"
							}
							found := false
							for _, d := range report.Diagnostics {
								if d.Code == wanted {
									found = true
								}
							}
							if !found {
								t.Fatalf("missing %s: %#v", wanted, report.Diagnostics)
							}
						}
						wantExit := 0
						if change != "unchanged" {
							wantExit = 1
						}
						if change == "deleted-evidence" {
							// Full CI also reads the now unavailable referenced input.
							// The base guard remains a gate refusal; aggregate errors
							// retain the stronger input-exception classification.
							wantExit = 2
						}
						actualExit := 0
						if err != nil {
							actualExit = semanticExit(err)
						}
						if actualExit != wantExit {
							t.Fatalf("full CI exit=%d want=%d: %v", actualExit, wantExit, err)
						}
						apTestExportNativeFixture(t, root, profile, "project-ci-base-"+action, change,
							[]string{"project-ci", action, "--base", base, "--runtime-store", "off"}, wantExit)
					}
					if before != ciTestGit(t, root, "status", "--porcelain=v1") || indexBefore != ciTestGit(t, root, "ls-files", "--stage") {
						t.Fatal("base check changed Git state")
					}
				})
			}
		})
	}
}

func TestProjectCICapturedCheckpointsKeepSourceOwnership(t *testing.T) {
	root := ciProfileTestRoot(t, "design")
	checkpoint := map[string]any{"gates": map[string]any{"gate.spec-approved": map[string]any{"approval_ref": "spec-approval.json"}}}
	apTestPut(t, root, "receiving-checkpoint.json", checkpoint)
	for _, ref := range []string{
		"docs/spec-baselines/spec-baseline.demo/v1/package/payload/files/checkpoint.json",
		"docs/handoffs/handoff.demo/v5/package/payload/files/checkpoint.json",
		"docs/backend-deliveries/delivery.demo/v1/package/payload/files/checkpoint.json",
	} {
		apTestPut(t, root, ref, checkpoint)
	}
	before := verificationTree(t, root)
	s := newSemanticSession(context.Background(), root, nil)
	owners, err := s.ciApprovalOwners()
	if err != nil {
		t.Fatal(err)
	}
	if !contractSame(owners["spec-approval.json"], []string{"receiving-checkpoint.json"}) {
		t.Fatalf("captured source checkpoint became a receiver approval owner: %#v", owners)
	}
	if err = s.finish(); err != nil {
		t.Fatal(err)
	}
	if !contractSame(before, verificationTree(t, root)) {
		t.Fatal("approval-owner discovery changed assets")
	}
}
