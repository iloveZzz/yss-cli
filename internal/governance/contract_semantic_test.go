package governance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"go.yaml.in/yaml/v3"
)

func contractTestBusiness(t *testing.T) (string, string, map[string]any, map[string]any, func()) {
	t.Helper()
	root := apTestRoot(t)
	setRef := "docs/.scratch/demo/business-ticket-set.yaml"
	ticketRef := "docs/.scratch/demo/business-tickets/BT-001.md"
	specRef := "docs/.scratch/demo/spec.md"
	bind := func(ref string) map[string]any {
		b, e := os.ReadFile(filepath.Join(root, ref))
		if e != nil {
			t.Fatal(e)
		}
		return map[string]any{"ref": ref, "version": "v1", "digest": "sha256:" + safefs.Digest(b)}
	}
	apTestPut(t, root, specRef, "---\ncontent_profile: plan-spec-v1\n---\n## 功能需求\n| ID | 需求 |\n|---|---|\n| FR-001 | 管理员登记数据源 |\n## 验收标准\n| ID | 需求引用 |\n|---|---|\n| AC-001 | FR-001 |\n")
	ticket := map[string]any{"schema_version": 1, "kind": "business-ticket", "id": "BT-001", "version": "v1", "status": "ready-for-human", "spec": bind(specRef), "requirement_refs": []any{"FR-001"}, "acceptance_refs": []any{"AC-001"}, "dependencies": []any{}, "source_refs": []any{}, "open_questions": []any{}}
	set := map[string]any{"schema_version": 1, "kind": "business-ticket-set", "id": "business-ticket-set.demo", "version": "v1", "status": "ready-for-human", "spec": ticket["spec"], "tickets": []any{}, "coverage_deferred": []any{}, "review_ref": "docs/.scratch/demo/review.yaml"}
	save := func() {
		b, e := yaml.Marshal(ticket)
		if e != nil {
			t.Fatal(e)
		}
		apTestPut(t, root, ticketRef, "---\n"+string(b)+"---\n# 数据源登记\n## 业务结果\n保存后可选择\n## 范围\n登记与校验\n## 非目标\n无调度\n## 验收\n引用 AC-001\n## 风险\n校验失败保留输入\n")
		entry := bind(ticketRef)
		entry["id"] = ticket["id"]
		set["tickets"] = []any{entry}
		apTestPut(t, root, setRef, set)
		apTestPut(t, root, "docs/.scratch/demo/review.md", "独立范围与粒度审查，仅为机制测试。")
		apTestPut(t, root, text(set["review_ref"]), map[string]any{"schema_version": 1, "kind": "business-ticket-review", "result": "passed", "subject_ref": setRef, "subject_digest": bind(setRef)["digest"], "reviewer": "synthetic.reviewer", "drafter": "synthetic.drafter", "evidence": []any{bind("docs/.scratch/demo/review.md")}})
	}
	save()
	return root, setRef, ticket, set, save
}

func TestContractBusinessFormalDraftAndReject(t *testing.T) {
	root, ref, ticket, set, save := contractTestBusiness(t)
	s := apTestSession(t, root)
	report, e := contractBusinessTickets(s, ref)
	if e != nil {
		t.Fatal(e)
	}
	if len(semList(report["tickets"])) != 1 {
		t.Fatalf("missing immutable source ticket rows: %#v", report)
	}
	if e = s.finish(); e != nil {
		t.Fatal(e)
	}
	ticket["status"] = "draft"
	set["status"] = "draft"
	ticket["open_questions"] = []any{map[string]any{"id": "Q-001", "question": "失败如何恢复", "owner": "product", "resolve_by": "Design", "blocking": true}}
	save()
	if _, e = contractBusinessTicketsMode(apTestSession(t, root), ref, "draft"); e != nil {
		t.Fatal(e)
	}
	if _, e = contractBusinessTickets(apTestSession(t, root), ref); e == nil {
		t.Fatal("draft/open blocker passed formal")
	}
	ticket["status"] = "ready-for-human"
	set["status"] = "ready-for-human"
	ticket["open_questions"] = []any{}
	ticket["requirement_refs"] = []any{"AC-001"}
	save()
	if _, e = contractBusinessTickets(apTestSession(t, root), ref); e == nil {
		t.Fatal("AC used as requirement passed")
	}
}

func TestContractBusinessLocatorStableOnly(t *testing.T) {
	root := apTestRoot(t)
	apTestPut(t, root, "rules.md", "# Rules\nBody only mentions rule.one.\n\n| ID | Statement |\n|---|---|\n| rule.two | stable |\n\n> | ID | Statement |\n> |---|---|\n> | rule.one | quoted |\n\n## Example\n| ID | Statement |\n|---|---|\n| rule.one | example |\n")
	if e := contractBusinessLocator(apTestSession(t, root), map[string]any{"ref": "rules.md", "locator": "rule.two", "locator_kind": "id"}); e != nil {
		t.Fatal(e)
	}
	if e := contractBusinessLocator(apTestSession(t, root), map[string]any{"ref": "rules.md", "locator": "rule.one", "locator_kind": "id"}); e == nil {
		t.Fatal("body/quote/example matched stable ID")
	}
	apTestPut(t, root, "rules.json", map[string]any{"rows": []any{map[string]any{"rule_id": "rule.one"}}})
	if e := contractBusinessLocator(apTestSession(t, root), map[string]any{"ref": "rules.json", "locator": "rule.one", "locator_kind": "id"}); e != nil {
		t.Fatal(e)
	}
}

func TestContractReadonlyCancelAndDrift(t *testing.T) {
	root, ref, _, _, _ := contractTestBusiness(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := newSemanticSession(ctx, root, nil)
	if _, e := contractBusinessTickets(s, ref); e == nil {
		t.Fatal("cancelled read passed")
	}
	s = apTestSession(t, root)
	if _, e := contractBusinessTickets(s, ref); e != nil {
		t.Fatal(e)
	}
	apTestPut(t, root, "docs/.scratch/demo/spec.md", "changed")
	if e := s.finish(); e == nil {
		t.Fatal("input drift passed")
	}
}

func TestContractBusinessOldOracleDifferential(t *testing.T) {
	if _, e := exec.LookPath("node"); e != nil {
		t.Skip("development-only old oracle unavailable")
	}
	old := governanceOracleRoot(t)
	if _, e := os.Stat(filepath.Join(old, "scripts/lib/business-tickets.mjs")); e != nil {
		t.Skip("fixed old oracle unavailable")
	}
	root, ref, ticket, _, save := contractTestBusiness(t)
	oracle := func() bool {
		cmd := exec.Command("node", "--input-type=module", "-e", "import {checkBusinessTickets} from './scripts/lib/business-tickets.mjs';const r=checkBusinessTickets({root:process.argv[1],setRef:process.argv[2],mode:'formal'});console.log(r.status);", root, ref)
		cmd.Dir = old
		b, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("old oracle failed: %v %s", e, b)
		}
		return strings.TrimSpace(string(b)) == "passed"
	}
	for _, valid := range []bool{true, false} {
		if !valid {
			ticket["acceptance_refs"] = []any{}
			save()
		}
		_, e := contractBusinessTickets(apTestSession(t, root), ref)
		if got := e == nil; got != oracle() || got != valid {
			t.Fatalf("differential valid=%v native=%v error=%v", valid, got, e)
		}
	}
}
func TestContractSliceV3OldOracleDifferential(t *testing.T) {
	if _, e := exec.LookPath("node"); e != nil {
		t.Skip("development-only old oracle unavailable")
	}
	old := governanceOracleRoot(t)
	cmd := exec.Command("node", "--input-type=module", "-e", "import {pilotFixture} from './scripts/fixtures/slice-contract-v3/pilot-fixture.mjs';import {createApprovedExecutionContext} from './scripts/lib/approved-execution-context.mjs';const f=pilotFixture();const a=f.approve();createApprovedExecutionContext(a.binding,{root:f.root,work_unit_id:'work-unit.slice-backend',readOnly:true});console.log(JSON.stringify({root:f.root,ref:a.binding.ref,approval:a.binding.approval_ref}));", "synthetic-oracle")
	cmd.Dir = old
	oracleTMP := contractTestOracleTMP(t)
	cmd.Env = append(os.Environ(), "TMPDIR="+oracleTMP)
	b, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("old valid Slice oracle: %v %s", e, b)
	}
	meta := map[string]string{}
	if e = json.Unmarshal(b, &meta); e != nil {
		t.Fatalf("%v %s", e, b)
	}
	t.Cleanup(func() {
		if os.Getenv("YSS_NATIVE_FIXTURE_ROOT") == "" {
			_ = os.RemoveAll(meta["root"])
		}
	})
	_ = apTestRoot(t)
	for ref, f := range apFixtureBundle.Files {
		if ref == ".template-spec/agents/issue-tracker.md" {
			continue // The old oracle fixture did not enable the business protocol.
		}
		if !(strings.HasPrefix(ref, ".agents/skills/") || strings.HasPrefix(ref, ".template-spec/") || strings.HasPrefix(ref, "scripts/lib/")) {
			continue
		}
		if _, e := os.Stat(filepath.Join(meta["root"], filepath.FromSlash(ref))); e == nil {
			continue
		}
		raw, e := f.Render(map[string]string{"projectName": "synthetic", "businessDomain": "test-only", "teamSize": "2"})
		if e != nil {
			t.Fatal(e)
		}
		apTestPut(t, meta["root"], ref, raw)
	}
	apTestPut(t, meta["root"], ".template-spec/process/harness-profile.yaml", map[string]any{"schema_version": 2, "profile_id": "harness.spec-template"})
	compilerRef := ".agents/skills/yss-implementation-contract-compiler/references/compiler-contract.yaml"
	compiler, err := os.ReadFile(filepath.Join(old, filepath.FromSlash(compilerRef)))
	if err != nil {
		t.Fatal(err)
	}
	apTestPut(t, meta["root"], compilerRef, compiler)
	s := apTestSession(t, meta["root"])
	s.args["tool-root"] = old
	c, e := loadNativeSlice(s, meta["ref"])
	if e != nil {
		t.Fatal(e)
	}
	for _, policy := range []string{"harness-apps-multi-project", "external-repository-native"} {
		for _, projectRoot := range []string{"app/backend/", "app/frontend/", "app/backend/project1/"} {
			t.Run(policy+"/"+projectRoot, func(t *testing.T) {
				raw := contractCopy(c.Raw)
				scope := semMap(raw["scope"])
				scope["implementation_path_policy"] = policy
				scope["project_roots"] = []any{projectRoot}
				for _, v := range semMap(raw["verification"]) {
					semMap(v)["cwd"] = projectRoot
				}
				for _, unit := range semList(raw["work_units"]) {
					semMap(unit)["project_root"] = projectRoot
				}
				apTestPut(t, meta["root"], "app-path-slice.yaml", map[string]any{"slice_contract": raw})
				if _, err := loadNativeSlice(apTestSession(t, meta["root"]), "app-path-slice.yaml"); err != nil {
					t.Fatalf("registered app root rejected: %v", err)
				}
			})
		}
	}
	if e = contractSliceFresh(s, c, map[string]string{"approval-ref": meta["approval"], "unit": "work-unit.slice-backend"}); e != nil {
		t.Fatal(e)
	}
	if e = s.finish(); e != nil {
		t.Fatal(e)
	}
	if _, e = RunContext(context.Background(), "contract", "verify", meta["root"], map[string]string{"kind": "slice", "file": meta["ref"], "tool-root": old, "approval-ref": meta["approval"], "unit": "work-unit.slice-backend"}); e != nil {
		t.Fatalf("public structural Slice: %v", e)
	}
	if _, err := RunContext(context.Background(), "contract", "verify", meta["root"], map[string]string{"kind": "slice", "file": meta["ref"], "tool-root": old}); err == nil {
		t.Fatal("Slice verify accepted without current approval")
	}
	contractTestRetainFixture(t, meta["root"], "slice-v3-missing-approval", map[string]any{"group": "contract", "action": "verify", "kind": "slice", "file": meta["ref"], "tool-root": old, "expected_exit": 1})
	contractTestRetainFixture(t, meta["root"], "slice-v3", map[string]any{"group": "contract", "action": "verify", "kind": "slice", "file": meta["ref"], "tool-root": old, "approval-ref": meta["approval"], "unit": "work-unit.slice-backend", "execution_authorization": "not-granted-by-validation", "expected_exit": 0})
}

func TestContractPlatformFingerprintOldOracleUnicode(t *testing.T) {
	if _, e := exec.LookPath("node"); e != nil {
		t.Skip("development-only old oracle unavailable")
	}
	old := governanceOracleRoot(t)
	root := apTestRoot(t)
	if _, err := os.Stat(filepath.Join(old, "scripts/lib/standalone-backend-scaffold.mjs")); err == nil {
		apTestPut(t, root, "scripts/lib/standalone-backend-scaffold.mjs", "synthetic standalone source\n")
	}
	for _, ref := range []string{".agents/skills/yss-ddd-scaffold-generator/scripts/generate_scaffold.mjs", ".agents/skills/yss-layered-mvc-scaffold-generator/scripts/generate_scaffold.mjs", "scripts/lib/backend-platform.mjs", "scripts/lib/scaffold-local-database.mjs", "scripts/lib/backend-platform-provenance.mjs", "scripts/lib/backend-platform-verification.mjs", "scripts/lib/command-runner.mjs", "scripts/vendor/xml.mjs", ".agents/skills/yss-ddd-scaffold-generator/scripts/run_scaffold_verification.mjs", ".agents/skills/yss-ddd-scaffold-generator/assets/wrapper/mvnw"} {
		apTestPut(t, root, ref, ref+"\n")
	}
	for _, skill := range []string{"yss-ddd-scaffold-generator", "yss-layered-mvc-scaffold-generator"} {
		for _, file := range []string{"_case", "-case", "case2", "case10", "Case", "case", "é", "e", "中", "ß", "İ"} {
			ref := ".agents/skills/" + skill + "/assets/" + file
			apTestPut(t, root, ref, "Unicode ordering "+file+"\n")
			if file == "Case" {
				if e := os.Chmod(filepath.Join(root, ref), 0755); e != nil {
					t.Fatal(e)
				}
			}
		}
	}
	for _, source := range []string{root, old} {
		for _, family := range []string{"domain-driven", "layered-mvc"} {
			cmd := exec.Command("node", "--input-type=module", "-e", "import {platformSourceFingerprint} from './scripts/lib/backend-platform-provenance.mjs';console.log(JSON.stringify(platformSourceFingerprint(process.argv[1],process.argv[2])));", family, source)
			cmd.Dir = old
			b, e := cmd.CombinedOutput()
			if e != nil {
				t.Fatalf("fixed old fingerprint: %v %s", e, b)
			}
			var expected map[string]any
			if e = json.Unmarshal(b, &expected); e != nil {
				t.Fatal(e)
			}
			s := newSemanticSession(context.Background(), source, nil)
			actual, e := contractPlatformFingerprint(s, family)
			if e != nil {
				t.Fatal(e)
			}
			if !contractSame(actual, expected) {
				t.Fatalf("fingerprint locale/bytes/mode differential %s: native=%v legacy=%v", family, actual, expected)
			}
			if e = s.finish(); e != nil {
				t.Fatal(e)
			}
		}
	}
}

func contractTacticalFixture() map[string]any {
	return map[string]any{
		"schema_version": 1, "tactical_design_id": "tactical-design.synthetic", "tactical_version": "v1", "version": "v1", "status": "approved", "context_ref": "CONTEXT.md", "digest": "sha256:" + strings.Repeat("1", 64), "evidence_refs": []any{},
		"aggregate_catalog": []any{map[string]any{"aggregate_id": "aggregate.order", "name": "订单", "context_ref": "Sales/Order", "root_entity": "entity.order", "consistency_boundary": "单订单", "invariant_refs": []any{"invariant.required"}, "behavior_refs": []any{"behavior.submit"}, "api_exposure": "internal-only"}},
		"entity_catalog":    []any{map[string]any{"entity_id": "entity.order", "name": "订单", "aggregate_id": "aggregate.order", "identity": "OrderId", "lifecycle": "draft->submitted"}}, "value_object_catalog": []any{},
		"behavior_catalog":  []any{map[string]any{"behavior_id": "behavior.submit", "name": "提交", "aggregate_id": "aggregate.order", "command": "Submit", "preconditions": []any{}, "invariant_refs": []any{"invariant.required"}, "postconditions": []any{}, "event_refs": []any{}}},
		"invariant_catalog": []any{map[string]any{"invariant_id": "invariant.required", "aggregate_id": "aggregate.order", "statement": "材料必填"}}, "state_transition_catalog": []any{map[string]any{"transition_id": "transition.submit", "aggregate_id": "aggregate.order", "from": "draft", "to": "submitted", "behavior_id": "behavior.submit"}},
		"consistency_policy": map[string]any{"transaction_boundary": "单聚合", "concurrency": "version", "idempotency": "requestId", "cross_aggregate_strategy": "outbox"}, "domain_event_catalog": []any{},
		"gateway_catalog": []any{map[string]any{"gateway_id": "gateway.order", "name": "订单存取", "context_ref": "Sales/Order", "layer": "Domain", "capabilities": []any{"save"}}}, "persistence_mapping": []any{map[string]any{"aggregate_id": "aggregate.order", "storage_model": "orders", "notes": "根拥有事务"}},
		"test_seams": []any{map[string]any{"seam_id": "test-seam.submit", "kind": "behavior", "subject_ref": "behavior.submit", "assertion": "必填校验"}}, "adr_candidates": []any{}, "upstream_impact": map[string]any{"spec_ref": "spec.md", "strategic_ref": "strategy.yaml", "api_ref": "api.yaml", "data_ref": "data.yaml", "upstream_current": true},
	}
}
func TestContractTacticalOldOracleDifferential(t *testing.T) {
	root := apTestRoot(t)
	old := governanceOracleRoot(t)
	for _, kind := range []string{"valid", "missing-preconditions", "wrong-root-owner", "invalid-id", "dangling-event", "upstream-stale", "missing-complexity-ref"} {
		t.Run(kind, func(t *testing.T) {
			d := contractTacticalFixture()
			switch kind {
			case "missing-preconditions":
				delete(semMap(semList(d["behavior_catalog"])[0]), "preconditions")
			case "wrong-root-owner":
				semMap(semList(d["entity_catalog"])[0])["aggregate_id"] = "aggregate.other"
			case "invalid-id":
				semMap(semList(d["test_seams"])[0])["seam_id"] = "synthetic.unknown"
			case "dangling-event":
				semMap(semList(d["behavior_catalog"])[0])["event_refs"] = []any{"domain-event.unknown"}
			case "upstream-stale":
				semMap(d["upstream_impact"])["upstream_current"] = false
			case "missing-complexity-ref":
				d["complexity"] = map[string]any{"escalate_to_standalone": true}
			}
			raw, err := json.Marshal(d)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := schema.Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			d = semMap(parsed)
			e := contractTactical(apTestSession(t, root), d)
			passed := e == nil
			if passed != (kind == "valid") {
				t.Fatalf("native %s: %v", kind, e)
			}
			if _, e := exec.LookPath("node"); e != nil {
				return
			}
			apTestPut(t, root, "tactical.json", d)
			cmd := exec.Command("node", "--input-type=module", "-e", "import fs from 'node:fs';import {validate} from './.agents/skills/yss-tactical-design/scripts/validate-tactical-design.mjs';console.log(validate(JSON.parse(fs.readFileSync(process.argv[1],'utf8'))).length===0);", filepath.Join(root, "tactical.json"))
			cmd.Dir = old
			b, e := cmd.CombinedOutput()
			if e != nil {
				t.Fatalf("old tactical oracle %v %s", e, b)
			}
			if got := strings.TrimSpace(string(b)) == "true"; got != passed {
				t.Fatalf("native/legacy mismatch %s native=%v legacy=%v", kind, passed, got)
			}
		})
	}
	d := contractTacticalFixture()
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := schema.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	d = semMap(parsed)
	if e := contractTechnicalDesign(apTestSession(t, root), "tactical.json", d, nil, nil); e == nil {
		t.Fatal("implicit legacy adapter passed")
	}
	if e := contractTechnicalDesign(apTestSession(t, root), "tactical.json", d, nil, map[string]string{"legacy-ddd": "true"}); e != nil {
		t.Fatal(e)
	}
}

func TestContractFrontendGitTemplateFixedTree(t *testing.T) {
	if _, e := exec.LookPath("git"); e != nil {
		t.Skip("Git capability unavailable")
	}
	for _, kind := range []string{"valid", "dirty-checkout", "wrong-head", "wrong-origin", "missing-checkout", "origin-drift"} {
		t.Run(kind, func(t *testing.T) {
			root, _, _, _, _ := verificationFrontendFixture(t)
			contractRef := "docs/.scratch/frontend/scaffold-contract.json"
			checkout, e := filepath.EvalSymlinks(t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			git := func(args ...string) string {
				t.Helper()
				c := exec.Command("git", args...)
				c.Dir = checkout
				b, e := c.CombinedOutput()
				if e != nil {
					t.Fatalf("synthetic Git setup: %v %s", e, b)
				}
				return strings.TrimSpace(string(b))
			}
			git("init", "-q")
			apTestPut(t, checkout, "package.json", `{"name":"synthetic-template"}`)
			git("add", "package.json")
			git("-c", "user.name=Synthetic", "-c", "user.email=synthetic@example.invalid", "commit", "-qm", "Synthetic fixture")
			commit := git("rev-parse", "HEAD")
			origin := "https://example.invalid/template.git"
			git("remote", "add", "origin", origin)
			s := apTestSession(t, root)
			contract, e := s.doc(contractRef)
			if e != nil {
				t.Fatal(e)
			}
			template := map[string]any{"repository": origin, "commit": commit}
			semMap(contract["frontend"])["template"] = template
			apTestPut(t, root, contractRef, contract)
			opts := map[string]string{"template-checkout": checkout}
			switch kind {
			case "dirty-checkout":
				apTestPut(t, checkout, "package.json", "uncommitted content is not template authority")
			case "wrong-head":
				template["commit"] = strings.Repeat("1", 40)
				apTestPut(t, root, contractRef, contract)
			case "wrong-origin":
				git("remote", "set-url", "origin", "https://example.invalid/other.git")
			case "missing-checkout":
				delete(opts, "template-checkout")
			}
			before := verificationTree(t, root)
			s = apTestSession(t, root)
			e = s.verify("scaffold", contractRef, opts)
			valid := kind == "valid" || kind == "dirty-checkout" || kind == "origin-drift"
			if (e == nil) != valid {
				t.Fatalf("%s: %v", kind, e)
			}
			if !contractSame(before, verificationTree(t, root)) {
				t.Fatal("readonly scaffold verification changed project")
			}
			if !valid {
				return
			}
			if kind == "origin-drift" {
				git("remote", "set-url", "origin", "https://example.invalid/drift.git")
				if e = s.finish(); e == nil {
					t.Fatal("source Git drift passed")
				}
			} else if e = s.finish(); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestContractTechnicalAnalysisTransitionOracle(t *testing.T) {
	for _, kind := range []string{"valid", "missing-persisted", "missing-evidence", "stale-api", "persisted-api-mismatch", "context-unreconciled"} {
		t.Run(kind, func(t *testing.T) {
			root := apTestRoot(t)
			contractTestRules(t, root)
			assessment := apTestPut(t, root, "api-assessment.json", map[string]any{"api": false})
			apTestPut(t, root, "api-evidence.md", "Synthetic no-API assessment evidence\n")
			api := map[string]any{"schema_version": 1, "kind": "api-contract-decision", "decision_id": "api-contract.synthetic", "decision_version": "v1", "status": "approved", "current_version": true, "impact": "not-applicable", "assessment_ref": "api-assessment.json", "assessment_digest": "sha256:" + safefs.Digest(assessment), "evidence_refs": []any{"api-evidence.md"}, "reason": "Synthetic no interface change"}
			apiBytes := apTestPut(t, root, "api-decision.json", api)
			binding := map[string]any{"ref": "api-decision.json", "version": "v1", "digest": "sha256:" + safefs.Digest(apiBytes), "impact": "not-applicable"}
			resultRef := "technical-result.json"
			result := map[string]any{"result_schema": "workflow-execution-result-v1", "work_unit": "work-unit.technical-analysis", "result": "completed", "current_version": true, "api_contract_decision": binding, "context_reconciliation": map[string]any{"status": "reconciled", "ref": "CONTEXT.md"}, "evidence_refs": []any{resultRef, "api-decision.json"}}
			apTestPut(t, root, resultRef, result)
			switch kind {
			case "missing-persisted":
				resultRef = "missing-result.json"
			case "missing-evidence":
				result["evidence_refs"] = []any{"api-decision.json"}
			case "stale-api":
				binding["digest"] = "sha256:" + strings.Repeat("0", 64)
			case "persisted-api-mismatch":
				persisted := contractCopy(result)
				persisted["api_contract_decision"] = contractCopy(binding)
				semMap(persisted["api_contract_decision"])["digest"] = "sha256:" + strings.Repeat("0", 64)
				apTestPut(t, root, resultRef, persisted)
			case "context-unreconciled":
				semMap(result["context_reconciliation"])["status"] = "pending"
			}
			state := map[string]any{"technical_analysis_result": result, "technical_analysis_result_ref": resultRef, "user_decision_not_applicable": []any{map[string]any{"boundary": "gate.backend-architecture-platform-approved", "reason": "Synthetic analysis has no backend architecture change"}, map[string]any{"boundary": "gate.engineering-contract-approved", "reason": "Synthetic analysis has no backend module scope"}}}
			cpRef := "transition-checkpoint.json"
			apTestPut(t, root, cpRef, map[string]any{"decision_state": state, "human_review": map[string]any{"not_applicable": state["user_decision_not_applicable"]}})
			s := apTestSession(t, root)
			before := verificationTree(t, root)
			e := s.verify("technical-transition", cpRef, nil)
			if (e == nil) != (kind == "valid") {
				t.Fatalf("native %s: %v", kind, e)
			}
			if e == nil {
				if e = s.finish(); e != nil {
					t.Fatal(e)
				}
			}
			if !contractSame(before, verificationTree(t, root)) {
				t.Fatal("transition modified evidence")
			}
			if _, e := exec.LookPath("node"); e != nil {
				return
			}
			cmd := exec.Command("node", "--input-type=module", "-e", `import fs from 'node:fs';import path from 'node:path';import {validateNextRoute} from './scripts/lib/lifecycle-transition.mjs';const root=process.argv[1],cp=JSON.parse(fs.readFileSync(path.join(root,'transition-checkpoint.json'),'utf8'));console.log(JSON.stringify(validateNextRoute('work-unit.technical-analysis','work-unit.implementation-repository-preparation',cp.decision_state,{root,exists:ref=>fs.existsSync(path.resolve(root,ref)),read:ref=>fs.readFileSync(path.resolve(root,ref),'utf8')})));`, root)
			cmd.Dir = governanceOracleRoot(t)
			raw, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("fixed old transition oracle %v %s", err, raw)
			}
			var outcome map[string]any
			if e = json.Unmarshal(raw, &outcome); e != nil {
				t.Fatalf("old oracle result: %v %s", e, raw)
			}
			if (text(outcome["result"]) == "allowed") != (kind == "valid") {
				t.Fatalf("legacy %s: %s", kind, raw)
			}
		})
	}
}

func TestContractServiceInitializationPublishedFixture(t *testing.T) {
	if _, e := exec.LookPath("node"); e != nil {
		t.Skip("development-only old producer unavailable")
	}
	old := governanceOracleRoot(t)
	producer := `import fs from 'node:fs';
import path from 'node:path';
import {pathToFileURL} from 'node:url';
import {createHash} from 'node:crypto';
import {validateServiceProjectInitialization} from './scripts/lib/lifecycle-transition.mjs';
const context=fs.readFileSync(process.argv[1],'utf8');
const uri=code=>'data:text/javascript;base64,'+Buffer.from(code).toString('base64');
const resolveImports=(code,file,overrides={})=>code.replace(/from (['"])(\.\.?\/[^'"]+)\1/g,(_,quote,ref)=>'from '+JSON.stringify(overrides[ref]||new URL(ref,pathToFileURL(file)).href));
const mvc=path.resolve('.agents/skills/yss-technical-design/tests/fixtures.mjs');
const mvcCode=fs.readFileSync(mvc,'utf8').replace(/('CONTEXT[.]md': )'[^']*'/,(_,prefix)=>prefix+JSON.stringify(context));
const mvcUri=uri(resolveImports(mvcCode,mvc));
const prerequisites=path.resolve('scripts/fixtures/backend-scaffold/design-prerequisites.mjs');
const prerequisiteUri=uri(resolveImports(fs.readFileSync(prerequisites,'utf8'),prerequisites,{'../../../.agents/skills/yss-technical-design/tests/fixtures.mjs':mvcUri}));
const file=path.resolve('.agents/skills/yss-layered-mvc-scaffold-generator/scripts/scaffold-generator.test.mjs');
const source=fs.readFileSync(file,'utf8');
let imports=resolveImports(source.slice(0,source.indexOf('\n\nconst script')),file,{'../../../../scripts/fixtures/backend-scaffold/design-prerequisites.mjs':prerequisiteUri});
const helpers=source.slice(source.indexOf('const capabilityModules'),source.indexOf('\ntest('));
const code=imports+'\nconst digest=value=>\x60sha256:${createHash("sha256").update(value).digest("hex")}\x60;\n'+helpers+'\nconst f=await fixture({after(){}},{architectureProfile:"mvc-data-analysis-v1",profile:{capabilities:[],modules:["server","core","client","repository","adapter","feign-client"]}});export default f;';
const f=(await import(uri(code))).default;
fs.writeFileSync(path.join(f.root,'context-handoff.md'),context);
f.contract.context_handoff_ref='context-handoff.md';
f.contract.context_handoff_digest='sha256:'+createHash('sha256').update(context).digest('hex');
fs.writeFileSync(f.contractFile,JSON.stringify(f.contract));
const state={service_project_initialization:{project_id:f.contract.project_name,contract_ref:'contract.json',contract_digest:'sha256:'+createHash('sha256').update(fs.readFileSync(f.contractFile)).digest('hex'),project_root:f.project,context_handoff_digest:f.contract.context_handoff_digest}};
const result=validateServiceProjectInitialization(state,{root:f.root});
fs.writeFileSync(path.join(f.root,'service-checkpoint.json'),JSON.stringify({decision_state:state}));
console.log(JSON.stringify({root:f.root,project:f.project,result,args:f.args}));`
	nativeRules := apTestRoot(t)
	cmd := exec.Command("node", "--input-type=module", "-e", producer, filepath.Join(nativeRules, "CONTEXT.md"))
	cmd.Dir = old
	tmp := contractTestOracleTMP(t)
	cmd.Env = append(os.Environ(), "TMPDIR="+tmp)
	raw, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("published source producer: %v %s", e, raw)
	}
	var meta struct {
		Root    string         `json:"root"`
		Project string         `json:"project"`
		Result  map[string]any `json:"result"`
		Args    []string       `json:"args"`
	}
	if e = json.Unmarshal(raw, &meta); e != nil {
		t.Fatalf("%v %s", e, raw)
	}
	t.Cleanup(func() {
		if os.Getenv("YSS_NATIVE_FIXTURE_ROOT") == "" {
			_ = os.RemoveAll(meta.Root)
		}
	})
	if text(meta.Result["result"]) != "allowed" {
		t.Fatalf("legacy valid service: %s", raw)
	}
	_ = apTestRoot(t)
	for ref, f := range apFixtureBundle.Files {
		if ref == ".template-spec/agents/issue-tracker.md" || !(strings.HasPrefix(ref, ".agents/skills/") || strings.HasPrefix(ref, ".template-spec/") || strings.HasPrefix(ref, "scripts/lib/")) {
			continue
		}
		if _, e = os.Stat(filepath.Join(meta.Root, filepath.FromSlash(ref))); e == nil {
			continue
		}
		b, e := f.Render(map[string]string{"projectName": "synthetic", "businessDomain": "test-only", "teamSize": "2"})
		if e != nil {
			t.Fatal(e)
		}
		apTestPut(t, meta.Root, ref, b)
	}
	// This published producer consumes the fixed tool's platform catalog. Bind
	// its evidence closure in the synthetic project rather than borrowing live
	// receiver authority during native validation.
	engineering := filepath.Join(old, ".template-source/engineering")
	if e = filepath.WalkDir(engineering, func(file string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		ref, err := filepath.Rel(old, file)
		if err != nil {
			return err
		}
		if _, err = os.Stat(filepath.Join(meta.Root, ref)); err == nil {
			return nil
		}
		b, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		apTestPut(t, meta.Root, filepath.ToSlash(ref), b)
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	apTestPut(t, meta.Root, "yss-project.yaml", map[string]any{"schema_version": 1, "repository_mode": "project-instance", "authority": "project"})
	apTestPut(t, meta.Root, ".template-spec/process/harness-profile.yaml", map[string]any{"schema_version": 2, "profile_id": "harness.spec-template"})
	s := apTestSession(t, meta.Root)
	s.args["tool-root"] = old
	contractTestScaffoldQualification(t, s)
	s = apTestSession(t, meta.Root)
	s.args["tool-root"] = old
	before := verificationTree(t, meta.Root)
	if e = s.verify("service-transition", "service-checkpoint.json", map[string]string{"completed": "false"}); e != nil {
		t.Fatal(e)
	}
	if e = s.finish(); e != nil {
		t.Fatal(e)
	}
	if !contractSame(before, verificationTree(t, meta.Root)) {
		t.Fatal("initialization preflight wrote project files")
	}
	if _, e = os.Stat(meta.Project); !os.IsNotExist(e) {
		t.Fatalf("preflight created target: %v", e)
	}
	if _, e = RunContext(context.Background(), "contract", "verify", meta.Root, map[string]string{"kind": "scaffold", "file": "contract.json", "tool-root": old}); e != nil {
		t.Fatalf("public scaffold contract: %v", e)
	}
	contractTestRetainFixture(t, meta.Root, "service-preflight", map[string]any{"group": "contract", "action": "verify", "kind": "scaffold", "file": "contract.json", "tool-root": old, "expected_exit": 0})
	if os.Getenv("YSS_NATIVE_FIXTURE_ROOT") != "" {
		t.Log("Retain the declared target absence for public CLI replay; full completion/oracle runs without export mode")
		return
	}
	contractTestBackendTechnicalTransition(t, meta.Root, old)
	args := append([]string{filepath.Join(old, "scripts/fixtures/backend-scaffold/generate-candidate.mjs"), filepath.Join(old, ".agents/skills/yss-layered-mvc-scaffold-generator/scripts/generate_scaffold.mjs")}, meta.Args...)
	generate := exec.Command("node", args...)
	generate.Dir = old
	if output, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("development-only published mechanical producer: %v %s", err, output)
	}
	manifestRef := filepath.Join(meta.Project, ".yss/scaffold-generation.json")
	manifest, e := newSemanticSession(context.Background(), meta.Root, nil).doc(manifestRef)
	if e != nil {
		t.Fatal(e)
	}
	manifest["completion_level"] = "empty-scaffold-verified"
	apTestPut(t, meta.Root, filepath.ToSlash(strings.TrimPrefix(manifestRef, meta.Root+string(filepath.Separator))), manifest)
	commands := []any{}
	for _, phase := range []string{"validate", "test", "package"} {
		stdout, stderr := phase+".stdout.log", phase+".stderr.log"
		apTestPut(t, meta.Root, stdout, "Synthetic transition mechanism; Maven was not executed.\n")
		apTestPut(t, meta.Root, stderr, "")
		commands = append(commands, map[string]any{"phase": phase, "command": "./mvnw " + phase, "exit_code": 0, "stdout_ref": filepath.Join(meta.Root, stdout), "stderr_ref": filepath.Join(meta.Root, stderr)})
	}
	verification := map[string]any{"status": "passed", "completion_level": "empty-scaffold-verified", "project_root": meta.Project, "commands": commands}
	apTestPut(t, meta.Root, "scaffold-verification.json", verification)
	apTestPut(t, meta.Root, "service-result.json", map[string]any{"result_schema": "workflow-execution-result-v1", "work_unit": "work-unit.service-project-initialization", "result": "completed", "evidence_refs": []any{manifestRef, filepath.Join(meta.Root, "scaffold-verification.json")}})
	cp, e := newSemanticSession(context.Background(), meta.Root, nil).doc("service-checkpoint.json")
	if e != nil {
		t.Fatal(e)
	}
	request := semMap(semMap(cp["decision_state"])["service_project_initialization"])
	request["manifest_ref"], request["verification_ref"], request["result_ref"] = manifestRef, filepath.Join(meta.Root, "scaffold-verification.json"), filepath.Join(meta.Root, "service-result.json")
	apTestPut(t, meta.Root, "service-checkpoint.json", cp)
	for _, kind := range []string{"valid-completion", "nonzero-maven", "missing-maven", "missing-log", "context-drift", "reinitialize"} {
		t.Run(kind, func(t *testing.T) {
			state := semMap(mustParseContract(mustMarshalContract(cp)))
			request := semMap(semMap(state["decision_state"])["service_project_initialization"])
			report := semMap(mustParseContract(mustMarshalContract(verification)))
			if kind == "nonzero-maven" {
				semMap(semList(report["commands"])[0])["exit_code"] = 1
			}
			if kind == "missing-maven" {
				report["commands"] = semList(report["commands"])[:2]
			}
			if kind == "missing-log" {
				semMap(semList(report["commands"])[0])["stdout_ref"] = filepath.Join(meta.Root, "missing.log")
			}
			if kind == "context-drift" {
				request["context_handoff_digest"] = "sha256:" + strings.Repeat("0", 64)
			}
			apTestPut(t, meta.Root, "service-checkpoint.json", state)
			apTestPut(t, meta.Root, "scaffold-verification.json", report)
			s := apTestSession(t, meta.Root)
			s.args["tool-root"] = old
			completed := "true"
			if kind == "reinitialize" {
				completed = "false"
			}
			before := verificationTree(t, meta.Root)
			e := s.verify("service-transition", "service-checkpoint.json", map[string]string{"completed": completed})
			if (e == nil) != (kind == "valid-completion") {
				t.Fatalf("native %s: %v", kind, e)
			}
			if e == nil {
				if e = s.finish(); e != nil {
					t.Fatal(e)
				}
			}
			if !contractSame(before, verificationTree(t, meta.Root)) {
				t.Fatal("service transition mutated evidence")
			}
			oracle := exec.Command("node", "--input-type=module", "-e", `import fs from 'node:fs';import path from 'node:path';import {validateServiceProjectInitialization} from './scripts/lib/lifecycle-transition.mjs';const root=process.argv[1],state=JSON.parse(fs.readFileSync(path.join(root,'service-checkpoint.json'),'utf8')).decision_state;console.log(JSON.stringify(validateServiceProjectInitialization(state,{root,completed:process.argv[2]==='true'})));`, meta.Root, completed)
			oracle.Dir = old
			raw, err := oracle.CombinedOutput()
			if err != nil {
				t.Fatalf("old completion oracle: %v %s", err, raw)
			}
			var outcome map[string]any
			if e = json.Unmarshal(raw, &outcome); e != nil {
				t.Fatal(e)
			}
			if (outcome["result"] == "allowed") != (kind == "valid-completion") {
				t.Fatalf("old %s: %s", kind, raw)
			}
		})
	}
	apTestPut(t, meta.Root, "service-checkpoint.json", cp)
	apTestPut(t, meta.Root, "scaffold-verification.json", verification)
}

func contractTestBackendTechnicalTransition(t *testing.T, root, old string) {
	t.Helper()
	doc, e := newSemanticSession(context.Background(), root, nil).doc("contract.json")
	if e != nil {
		t.Fatal(e)
	}
	p := semMap(doc["design_prerequisites"])
	state := map[string]any{"project_id": doc["project_name"], "context_reconciliation": map[string]any{"status": "reconciled", "ref": "CONTEXT.md"}, "approved_spec": map[string]any{"status": "approved", "current_version": true, "ref": "spec.md"}, "engineering_contract_approval_ref": p["engineering_contract_approval_ref"]}
	for _, key := range []string{"technical_design", "data_architecture_decision", "api_contract_decision"} {
		binding := contractCopy(semMap(p[key]))
		binding["status"], binding["current_version"] = "approved", true
		state[key] = binding
	}
	for _, kind := range []string{"technical-valid", "technical-stale-api", "technical-missing-approval", "technical-stale-data", "technical-context-pending"} {
		t.Run(kind, func(t *testing.T) {
			current := semMap(mustParseContract(mustMarshalContract(state)))
			switch kind {
			case "technical-stale-api":
				semMap(current["api_contract_decision"])["digest"] = "sha256:" + strings.Repeat("0", 64)
			case "technical-missing-approval":
				current["engineering_contract_approval_ref"] = "missing-approval.json"
			case "technical-stale-data":
				semMap(current["data_architecture_decision"])["current_version"] = false
			case "technical-context-pending":
				semMap(current["context_reconciliation"])["status"] = "pending"
			}
			contextAuthority := contractCopy(semMap(state["context_reconciliation"]))
			contextAuthority["evidence_refs"] = []any{"CONTEXT.md"}
			apTestPut(t, root, "technical-checkpoint.json", map[string]any{"decision_state": current, "context_reconciliation": contextAuthority})
			s := apTestSession(t, root)
			s.args["tool-root"] = old
			before := verificationTree(t, root)
			e := s.verify("technical-transition", "technical-checkpoint.json", nil)
			if (e == nil) != (kind == "technical-valid") {
				t.Fatalf("native %s: %v", kind, e)
			}
			if e == nil {
				if e = s.finish(); e != nil {
					t.Fatal(e)
				}
			}
			if !contractSame(before, verificationTree(t, root)) {
				t.Fatal("technical transition changed assets")
			}
			oracle := exec.Command("node", "--input-type=module", "-e", `import fs from 'node:fs';import path from 'node:path';import {validateTechnicalDesignCompletion} from './submodules/yss-harness-backend-agent/scripts/lib/lifecycle-transition.mjs';const root=process.argv[1],state=JSON.parse(fs.readFileSync(path.join(root,'technical-checkpoint.json'),'utf8')).decision_state;console.log(JSON.stringify(validateTechnicalDesignCompletion(state,{root,exists:ref=>fs.existsSync(path.resolve(root,ref))})));`, root)
			oracle.Dir = old
			raw, err := oracle.CombinedOutput()
			if err != nil {
				t.Fatalf("fixed backend technical oracle: %v %s", err, raw)
			}
			var result map[string]any
			if e = json.Unmarshal(raw, &result); e != nil {
				t.Fatal(e)
			}
			if (result["result"] == "allowed") != (kind == "technical-valid") {
				t.Fatalf("old %s: %s", kind, raw)
			}
		})
	}
	apTestPut(t, root, "technical-checkpoint.json", map[string]any{"decision_state": state})
}

func contractTestScaffoldQualification(t *testing.T, s *semanticSession) {
	t.Helper()
	contract, e := s.doc("contract.json")
	if e != nil {
		t.Fatal(e)
	}
	catalog, e := s.doc(".template-spec/engineering/backend-platforms.json")
	if e != nil {
		t.Fatal(e)
	}
	binding := semMap(contract["platform_configuration"])
	profile := apFind(catalog["profiles"], "id", text(binding["profile_id"]))
	entry := map[string]any{"id": binding["compatibility_id"], "profile_id": binding["profile_id"], "spring_boot_version": binding["spring_boot_version"], "parent": binding["parent"], "bom": binding["bom"], "status": "verified", "capabilities": []any{"external-integration", "published-client", "feign-client"}, "evidence": []any{}}
	if contractPlatformRecipe(profile, entry) != text(binding["compatibility_digest"]) {
		t.Fatal("published candidate producer recipe changed")
	}
	fingerprint, e := contractPlatformFingerprint(s, text(contract["architecture_family"]))
	if e != nil {
		t.Fatal(e)
	}
	commands, artifacts := []any{}, []any{}
	refs := []string{"effective-pom.xml", "dependency-trees.json", "platform-tests.xml", "boot-bom-effective.xml", "runtime-jar-entries.log", "startup.stdout.log", "startup.stderr.log"}
	for _, phase := range []string{"validate", "test", "package"} {
		stdout, stderr := "mvnw-"+phase+".stdout.log", "mvnw-"+phase+".stderr.log"
		refs = append(refs, stdout, stderr)
		commands = append(commands, map[string]any{"command": "./mvnw " + phase, "exit_code": 0, "executed_at": "synthetic-qualification-only", "stdout_ref": stdout, "stderr_ref": stderr})
	}
	for _, ref := range refs {
		data := []byte("Synthetic qualification mechanism fixture; commands were not executed.\n")
		apTestPut(t, s.root, "qualification/"+ref, data)
		artifacts = append(artifacts, map[string]any{"ref": ref, "digest": "sha256:" + safefs.Digest(data)})
	}
	report := map[string]any{"verification_scope": "empty-scaffold", "recipe_digest": binding["compatibility_digest"], "generated_tree_digest": "sha256:" + strings.Repeat("1", 64), "status": "passed", "spring_boot_version": profile["spring_boot_version"], "java_version": profile["java_version"], "architecture_family": contract["architecture_family"], "parent": binding["parent"], "bom": binding["bom"], "dependency_check": "passed", "startup_check": "passed", "integration_tests": map[string]any{"status": "passed"}, "source_fingerprint": fingerprint, "verified_capabilities": []any{}, "commands": commands, "evidence_artifacts": artifacts}
	apTestPut(t, s.root, "qualification/report.json", report)
	raw, e := os.ReadFile(filepath.Join(s.root, "qualification/report.json"))
	if e != nil {
		t.Fatal(e)
	}
	entry["evidence"] = []any{map[string]any{"ref": "qualification/report.json", "digest": "sha256:" + safefs.Digest(raw), "architecture_family": contract["architecture_family"]}}
	catalog["compatibility"] = []any{entry}
	apTestPut(t, s.root, ".template-spec/engineering/backend-platforms.json", catalog)
}

func TestContractBackendProbeCurrentFactsAndCancellation(t *testing.T) {
	for _, kind := range []string{"valid", "revision-drift", "redirect", "oversize", "invalid-json", "yaml-response", "duplicate-json", "cross-origin", "credential-url", "query-url", "authorization-missing", "authorization", "authorization-drift", "empty-pointer-member", "literal-pointer-tilde", "offline", "timeout", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			var drift atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.Header.Get("Accept") != "application/json" {
					t.Error("probe protocol changed")
				}
				if kind == "cancel" || kind == "timeout" {
					<-r.Context().Done()
					return
				}
				if kind == "redirect" {
					http.Redirect(w, r, "/other", http.StatusFound)
					return
				}
				if kind == "oversize" {
					_, _ = io.WriteString(w, strings.Repeat("x", (1<<20)+1))
					return
				}
				if kind == "invalid-json" {
					_, _ = io.WriteString(w, "{")
					return
				}
				if kind == "yaml-response" {
					_, _ = io.WriteString(w, "deployment_id: deployment.synthetic\n")
					return
				}
				if kind == "duplicate-json" {
					_, _ = io.WriteString(w, `{"deployment_id":"stale","deployment_id":"deployment.synthetic"}`)
					return
				}
				if (kind == "authorization" || kind == "authorization-drift") && r.Header.Get("Authorization") != "Bearer synthetic-only" {
					t.Error("registered authorization not used")
				}
				deployment := "deployment.synthetic"
				if drift.Load() {
					deployment = "deployment.changed"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"deployment_id": deployment, "revision": map[string]any{"source/commit": strings.Repeat("a", 40), "source~2commit": strings.Repeat("a", 40), "openapi_digest": "sha256:" + strings.Repeat("b", 64), "artifact_digest": "sha256:" + strings.Repeat("c", 64), "test_data_digest": "sha256:" + strings.Repeat("d", 64)}})
			}))
			defer server.Close()
			env := map[string]any{"base_url": server.URL, "revision_path": "/revision", "deployment_id": "deployment.synthetic", "test_data": map[string]any{"digest": "sha256:" + strings.Repeat("d", 64)}, "revision_pointers": map[string]any{"deployment_id": "/deployment_id", "source_commit": "/revision/source~1commit", "openapi_digest": "/revision/openapi_digest", "artifact_digest": "/revision/artifact_digest", "test_data_digest": "/revision/test_data_digest"}}
			delivery := map[string]any{"environment": env, "build": map[string]any{"source_commit": strings.Repeat("a", 40), "artifact_digest": "sha256:" + strings.Repeat("c", 64)}, "openapi": map[string]any{"digest": "sha256:" + strings.Repeat("b", 64)}}
			if kind == "empty-pointer-member" {
				semMap(env["revision_pointers"])["source_commit"] = "/revision//source~1commit"
			}
			if kind == "literal-pointer-tilde" {
				semMap(env["revision_pointers"])["source_commit"] = "/revision/source~2commit"
			}
			if strings.HasPrefix(kind, "authorization") {
				env["authorization_env"] = "YSS_TEST_BACKEND_AUTH"
				t.Setenv("YSS_TEST_BACKEND_AUTH", "")
				if kind != "authorization-missing" {
					t.Setenv("YSS_TEST_BACKEND_AUTH", "Bearer synthetic-only")
				}
			}
			if kind == "cross-origin" {
				env["revision_path"] = "http://127.0.0.1:1/revision"
			}
			if kind == "credential-url" {
				env["base_url"] = strings.Replace(server.URL, "http://", "http://credential@", 1)
			}
			if kind == "query-url" {
				env["base_url"] = server.URL + "?credential=synthetic-only"
			}
			if kind == "offline" {
				server.Close()
			}
			initialPass := kind == "valid" || kind == "revision-drift" || kind == "authorization" || kind == "authorization-drift" || kind == "literal-pointer-tilde"
			if kind != "cancel" && kind != "timeout" {
				if _, e := exec.LookPath("node"); e == nil {
					payload, _ := json.Marshal(delivery)
					oracle := exec.Command("node", "--input-type=module", "-e", `import fs from 'node:fs';import {probeBackend} from './scripts/lib/frontend-delivery.mjs';try {await probeBackend(JSON.parse(fs.readFileSync(0,'utf8')));console.log(JSON.stringify({passed:true}));}catch(error){console.log(JSON.stringify({passed:false,message:error.message}));}`, "synthetic-oracle")
					oracle.Dir = governanceOracleRoot(t)
					oracle.Stdin = bytes.NewReader(payload)
					out, e := oracle.CombinedOutput()
					if e != nil {
						t.Fatalf("fixed probe oracle: %v %s", e, out)
					}
					var result struct {
						Passed  bool   `json:"passed"`
						Message string `json:"message"`
					}
					if e = json.Unmarshal(out, &result); e != nil || result.Passed != initialPass {
						t.Fatalf("fixed probe oracle %s: %v %s", kind, e, out)
					}
				}
			}
			ctx := context.Background()
			if kind == "cancel" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
				defer cancel()
			}
			s := newSemanticSession(ctx, t.TempDir(), nil)
			start := time.Now()
			e := contractProbeBackend(s, delivery)
			if (e == nil) != initialPass {
				t.Fatalf("initial probe %s: %v", kind, e)
			}
			if kind == "cancel" && time.Since(start) > 2*time.Second {
				t.Fatal("cancelled probe did not return promptly")
			}
			if kind == "timeout" && (time.Since(start) < 4*time.Second || time.Since(start) > 7*time.Second) {
				t.Fatal("fixed five-second probe timeout changed")
			}
			if kind == "offline" || kind == "timeout" {
				if e == nil || !strings.Contains(e.Error(), "当前登记服务版本不可读") {
					t.Fatalf("known offline service must report current execution error: %v", e)
				}
			}
			if !initialPass {
				return
			}
			if kind == "revision-drift" {
				drift.Store(true)
			}
			if kind == "authorization-drift" {
				t.Setenv("YSS_TEST_BACKEND_AUTH", "Bearer changed")
			}
			e = s.finish()
			if (e == nil) != (kind == "valid" || kind == "authorization" || kind == "literal-pointer-tilde") {
				t.Fatalf("final probe %s: %v", kind, e)
			}
			encoded, _ := json.Marshal(s.report)
			if bytes.Contains(encoded, []byte("Bearer synthetic-only")) || bytes.Contains(encoded, []byte("Bearer changed")) || bytes.Contains(encoded, []byte("credential=synthetic-only")) {
				t.Fatal("probe report leaked authorization")
			}
		})
	}
}

func TestContractFrontendBackendDeliveryFixedSourceChain(t *testing.T) {
	for _, version := range []int{4, 5} {
		t.Run(fmt.Sprintf("handoff-v%d", version), func(t *testing.T) { contractFrontendBackendDeliveryChain(t, version) })
	}
}

func contractFrontendBackendDeliveryChain(t *testing.T, handoffVersion int) {
	if _, e := exec.LookPath("node"); e != nil {
		t.Skip("development-only fixed source producer unavailable")
	}
	old := governanceOracleRoot(t)
	root := contractTestRetainedRoot(t, apTestProfileRoot(t, "frontend"), "frontend-online")
	contractTestRules(t, root)
	frontendBundle, err := bundle.Load("frontend")
	if err != nil {
		t.Fatal(err)
	}
	for ref, f := range frontendBundle.Files {
		if strings.HasPrefix(ref, ".agents/skills/harness-orchestrator/") {
			raw, err := f.Render(map[string]string{"projectName": "synthetic", "businessDomain": "test-only", "teamSize": "2"})
			if err != nil {
				t.Fatal(err)
			}
			apTestPut(t, root, ref, raw)
		}
	}
	var revision atomic.Value
	var changed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/version" || r.Header.Get("Accept") != "application/json" {
			t.Error("unexpected frontend current-fact request")
		}
		value := contractCopy(revision.Load().(map[string]any))
		if changed.Load() {
			value["deployment_id"] = "changed-current-service"
		}
		_ = json.NewEncoder(w).Encode(value)
	}))
	defer server.Close()
	input, e := os.ReadFile(filepath.Join(old, ".template-source/tooling/node/test/plugin-terminal.test.mjs"))
	if e != nil {
		t.Fatal(e)
	}
	textSource := string(input)
	imports := textSource[:strings.Index(textSource, "test('real validators")]
	imports = regexp.MustCompile(`from '([^']+)'`).ReplaceAllStringFunc(imports, func(v string) string {
		m := regexp.MustCompile(`from '([^']+)'`).FindStringSubmatch(v)
		if !strings.HasPrefix(m[1], ".") {
			return v
		}
		return "from '" + governanceOracleURL(filepath.Clean(filepath.Join(old, ".template-source/tooling/node/test", m[1]))) + "'"
	})
	body := textSource[strings.Index(textSource, "  const f=terminalReviewFixture();"):strings.Index(textSource, "  const input={delivery:")]
	if handoffVersion == 5 {
		body = strings.ReplaceAll(body, "{handoffVersion:4}", "{handoffVersion:5,businessTickets:true}")
		imports += "\nimport {finalizeDelivery} from '" + governanceOracleURL(filepath.Join(old, "scripts/lib/strategic-handoff.mjs")) + "';\n"
		body = strings.ReplaceAll(body, "const strategy=await exportBundle({sourceRoot:source,handoffRef:'handoff.yaml',output:path.join(f.root,'strategy-package')});", "const strategy=await finalizeDelivery({sourceRoot:source,handoffRef:'handoff.yaml'});fs.cpSync(strategy.delivery,path.join(f.root,'strategy-package'),{recursive:true});")
	}
	body = strings.ReplaceAll(body, "t.after(()=>f.cleanup());", "")
	body = strings.ReplaceAll(body, "base_url:'http://127.0.0.1:1'", "base_url:process.argv[2]")
	body = strings.ReplaceAll(body, "  const source=path.join", "  f.write('.template-spec/process/harness-profile.yaml',{schema_version:2,profile_id:'harness.spec-template'});\n  const source=path.join")
	producer := imports + body + "\n" + `import {importBackendDelivery} from '` + governanceOracleURL(filepath.Join(old, "scripts/lib/backend-delivery.mjs")) + `';
fs.writeFileSync(path.join(process.argv[1],'CONTEXT.md'),fs.readFileSync(path.join(source,'CONTEXT.md')));
${STRATEGIC_IMPORT}const imported=await importBackendDelivery({bundle:exported.output,targetRoot:process.argv[1]});
console.log(JSON.stringify({root:f.root,slice:f.contract.slice_id,acceptance:imported.acceptance_ref,delivery}));`
	if handoffVersion == 5 {
		producer = strings.ReplaceAll(producer, "${STRATEGIC_IMPORT}", "await importBundle({bundle:strategy.delivery,targetRoot:process.argv[1]});")
		producer = "import {importBundle} from '" + governanceOracleURL(filepath.Join(old, "scripts/lib/strategic-handoff.mjs")) + "';\n" + producer
	} else {
		producer = strings.ReplaceAll(producer, "${STRATEGIC_IMPORT}", "")
	}
	producer = strings.ReplaceAll(producer, `\nimport`, "\nimport")
	cmd := exec.Command("node", "--input-type=module", "-e", producer, root, server.URL)
	cmd.Dir = old
	cmd.Env = append(os.Environ(), "TMPDIR="+contractTestOracleTMP(t))
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("fixed frontend producer: %v %s", e, out)
	}
	var meta struct {
		Root, Slice, Acceptance string
		Delivery                map[string]any
	}
	if e = json.Unmarshal(out, &meta); e != nil {
		t.Fatalf("%v %s", e, out)
	}
	t.Cleanup(func() {
		if os.Getenv("YSS_NATIVE_FIXTURE_ROOT") == "" {
			_ = os.RemoveAll(meta.Root)
		}
	})
	delivery := meta.Delivery
	revision.Store(map[string]any{"deployment_id": semMap(delivery["environment"])["deployment_id"], "source_commit": semMap(delivery["build"])["source_commit"], "openapi_digest": semMap(delivery["openapi"])["digest"], "artifact_digest": semMap(delivery["build"])["artifact_digest"], "test_data_digest": semMap(semMap(delivery["environment"])["test_data"])["digest"]})
	s := apTestSession(t, root)
	acceptance, e := s.doc(meta.Acceptance)
	if e != nil {
		t.Fatal(e)
	}
	strategy := semMap(acceptance["strategic_handoff"])
	receipt, e := s.doc(text(strategy["import_receipt_ref"]))
	if e != nil {
		t.Fatal(e)
	}
	bundle, e := contractOpenHandoff(s, text(receipt["package_ref"]))
	if e != nil {
		t.Fatal(e)
	}
	if contractN(bundle.Handoff["schema_version"]) != handoffVersion || contractN(receipt["schema_version"]) != handoffVersion-2 || contractN(acceptance["schema_version"]) != handoffVersion-2 {
		t.Fatal("Handoff receipt and acceptance protocol chain changed")
	}
	t.Run("backend-strategic-input", func(t *testing.T) {
		before := verificationTree(t, meta.Root)
		source := newSemanticSession(context.Background(), meta.Root, nil)
		// As in backendOpenDelivery, the actual receiving Profile supplies the
		// installed schema reader; the exported synthetic producer is evidence.
		source.ruleSession = s
		opened, err := backendOpenStrategicInput(source, text(delivery["strategic_bundle_ref"]))
		if err != nil {
			t.Fatal(err)
		}
		if opened.Manifest["bundle_digest"] != delivery["strategic_bundle_digest"] {
			t.Fatal("formal Backend input changed the approved strategic version")
		}
		if err = source.finish(); err != nil {
			t.Fatal(err)
		}
		if !contractSame(before, verificationTree(t, meta.Root)) {
			t.Fatal("formal Backend strategic query changed source bytes")
		}
	})
	t.Run("backend-strategic-input-raw", func(t *testing.T) {
		before := verificationTree(t, meta.Root)
		source := newSemanticSession(context.Background(), meta.Root, nil)
		source.ruleSession = s
		ref := text(delivery["strategic_bundle_ref"])
		if handoffVersion == 5 {
			ref = path.Join(ref, "package")
		}
		_, err := backendOpenStrategicInput(source, ref)
		if handoffVersion == 5 {
			apTestCode(t, err, "BACKEND_DELIVERY")
		} else if err != nil {
			t.Fatalf("legacy v4 raw package compatibility: %v", err)
		}
		if !contractSame(before, verificationTree(t, meta.Root)) {
			t.Fatal("raw Backend strategic query changed source bytes")
		}
	})
	if handoffVersion == 5 {
		t.Run("rehashed-legacy-layout-refused", func(t *testing.T) {
			prefix := "backend-strategic-rehashed-v5"
			refs, err := s.scan(text(receipt["package_ref"]))
			if err != nil {
				t.Fatal(err)
			}
			for _, ref := range refs {
				raw, err := s.bytes(ref)
				if err != nil {
					t.Fatal(err)
				}
				apTestPut(t, root, path.Join(prefix, strings.TrimPrefix(ref, text(receipt["package_ref"])+"/")), raw)
			}
			manifest := semMap(contractCopy(bundle.Manifest))
			for _, entry := range semList(manifest["files"]) {
				file := semMap(entry)
				original := text(file["original_ref"])
				stored := ""
				if original == text(manifest["handoff_ref"]) {
					stored = "handoff.yaml"
				} else if original == "CONTEXT.md" {
					stored = "payload/source-context.snapshot.md"
				}
				if stored != "" {
					from := path.Join(prefix, text(file["path"]))
					apTestPut(t, root, path.Join(prefix, stored), mustReadSpecBaselineTestFile(t, filepath.Join(root, from)))
					if err := os.Remove(filepath.Join(root, from)); err != nil {
						t.Fatal(err)
					}
					file["path"] = stored
				}
			}
			manifest["bundle_digest"] = contractDigest(contractWithout(manifest, "bundle_digest"))
			apTestPut(t, root, path.Join(prefix, "manifest.json"), manifest)
			before := verificationTree(t, root)
			_, err = contractOpenHandoff(apTestSession(t, root), prefix)
			apTestCode(t, err, "HANDOFF_PATH")
			if !contractSame(before, verificationTree(t, root)) {
				t.Fatal("rehashed v5 package rejection changed files")
			}
		})
	}
	proof := apTestPut(t, root, "frontend-case-evidence.log", "Synthetic frontend success/failure evidence. No browser/build run.")
	contextRef := "frontend-context-reconciliation.json"
	apTestPut(t, root, contextRef, map[string]any{"schema_version": 1, "repository_mode": "project-instance", "stage": "stage.system-data-engineering", "work_unit": "work-unit.frontend-engineering-design", "status": "reconciled", "context_snapshot": bundle.Handoff["source_context_snapshot"], "changes": map[string]any{"added": []any{}, "updated": []any{}, "deprecated": []any{}}, "unresolved_terms": []any{}, "evidence_refs": []any{"frontend-case-evidence.log"}})
	strategy["context_reconciliation_ref"] = contextRef
	for _, item := range semList(strategy["rows"]) {
		row := semMap(item)
		row["disposition"] = "mapped"
		row["dependency_status"] = "known"
		row["dependent_slice_refs"] = []any{meta.Slice}
		row["frontend_case_refs"] = []any{"success", "failure"}
		row["evidence_refs"] = []any{"frontend-case-evidence.log"}
	}
	acceptance["status"] = "accepted"
	preflightBinding := semMap(acceptance["strategic_preflight"])
	preflight, e := s.doc(text(preflightBinding["ref"]))
	if e != nil {
		t.Fatal(e)
	}
	if contractN(preflight["schema_version"]) != handoffVersion-3 {
		t.Fatal("Handoff frontend preflight protocol chain changed")
	}
	preflight["status"] = "verified"
	preflight["context_reconciliation_ref"] = contextRef
	preflightBytes := apTestPut(t, root, text(preflightBinding["ref"]), preflight)
	preflightBinding["digest"] = "sha256:" + safefs.Digest(preflightBytes)
	cases := []any{}
	caseSources := semMap(delivery["scope"])["source_ids"]
	caseKey := "visual_case_ids"
	if handoffVersion == 5 {
		caseKey = "baseline_case_ids"
		ids := []any{}
		for _, row := range bundle.Rules {
			ids = append(ids, semMap(row)["rule_id"])
		}
		for _, row := range bundle.Scenarios {
			if semMap(row)["critical"] == true {
				ids = append(ids, semMap(row)["scenario_id"])
			}
		}
		for _, row := range semList(strategy["rows"]) {
			id := text(semMap(row)["source_id"])
			if id != "" {
				found := false
				for _, value := range ids {
					found = found || value == id
				}
				if !found {
					ids = append(ids, id)
				}
			}
		}
		caseSources = ids
	}
	for _, outcome := range []string{"success", "failure"} {
		cases = append(cases, map[string]any{"case_id": outcome, "source_ids": caseSources, "outcome": outcome, "operation_ids": semMap(delivery["scope"])["operation_ids"], caseKey: []any{"primary-desktop"}, "evidence_ref": "frontend-case-evidence.log", "evidence_digest": "sha256:" + safefs.Digest(proof)})
	}
	acceptance["frontend_cases"] = cases
	clone := func(v map[string]any) map[string]any {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		out, err := schema.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		return semMap(out)
	}
	valid := clone(acceptance)
	for _, state := range []string{"valid", "stale", "unknown-version"} {
		t.Run("public-frontend-consumption-"+state, func(t *testing.T) {
			candidate := clone(valid)
			ref := "frontend-consumption-" + state + ".json"
			if state == "stale" {
				semMap(candidate["strategic_handoff"])["bundle_digest"] = "sha256:" + strings.Repeat("0", 64)
			} else if state == "unknown-version" {
				candidate["schema_version"] = 99
			}
			apTestPut(t, root, ref, candidate)
			before := verificationTree(t, root)
			_, e := RunContext(context.Background(), "handoff", "verify", root, map[string]string{"kind": "consumption", "file": ref, "consumer": "frontend", "profile": "frontend", "tool-root": old})
			if (e == nil) != (state == "valid") {
				t.Fatalf("public frontend consumption %s: %v", state, e)
			}
			if !contractSame(before, verificationTree(t, root)) {
				t.Fatal("Handoff consumption changed files")
			}
			expected := 1
			if state == "valid" {
				expected = 0
			} else if state == "unknown-version" {
				expected = 2
			}
			contractTestRetainFixture(t, root, "frontend-consumption-"+state, map[string]any{"group": "handoff", "action": "verify", "kind": "consumption", "file": ref, "consumer": "frontend", "profile": "frontend", "tool-root": old, "expected_exit": expected})
		})
	}
	for _, name := range []string{"valid", "wrong-operation", "missing-case-evidence", "wrong-source", "context-blocked", "final-service-drift"} {
		t.Run(name, func(t *testing.T) {
			candidate := clone(valid)
			changed.Store(false)
			if name == "wrong-operation" {
				semMap(semList(candidate["frontend_cases"])[0])["operation_ids"] = []any{"unknownOperation"}
			}
			if name == "missing-case-evidence" {
				semMap(semList(candidate["frontend_cases"])[0])["evidence_ref"] = "missing.log"
			}
			if name == "wrong-source" {
				semMap(semList(candidate["frontend_cases"])[0])["source_ids"] = []any{"scenario.unknown"}
			}
			if name == "context-blocked" {
				semMap(candidate["strategic_handoff"])["context_reconciliation_ref"] = "missing-context.json"
			}
			apTestPut(t, root, meta.Acceptance, candidate)
			oracle := exec.Command("node", "--input-type=module", "-e", `import {verifyFrontendDelivery} from './scripts/lib/frontend-delivery.mjs';try{await verifyFrontendDelivery({root:process.argv[1],acceptanceRef:process.argv[2],sliceRef:process.argv[3]});console.log(JSON.stringify({passed:true}));}catch(error){console.log(JSON.stringify({passed:false,message:error.message}));}`, root, meta.Acceptance, meta.Slice)
			oracle.Dir = old
			raw, err := oracle.CombinedOutput()
			if err != nil {
				t.Fatalf("fixed frontend oracle: %v %s", err, raw)
			}
			var result struct {
				Passed  bool
				Message string
			}
			if e = json.Unmarshal(raw, &result); e != nil {
				t.Fatal(e)
			}
			expected := name == "valid" || name == "final-service-drift"
			if result.Passed != expected {
				t.Fatalf("fixed chain %s: %s", name, raw)
			}
			before := verificationTree(t, root)
			s := newSemanticSession(context.Background(), root, map[string]string{"profile": "frontend", "tool-root": old})
			if e = s.authorities(); e != nil {
				t.Fatal(e)
			}
			e = s.verify("frontend-delivery", meta.Acceptance, map[string]string{"slice": meta.Slice, "phase": "inputs"})
			if (e == nil) != expected {
				t.Fatalf("native frontend chain %s: %v", name, e)
			}
			if e == nil {
				if name == "final-service-drift" {
					changed.Store(true)
				}
				e = s.finish()
				if (e == nil) != (name == "valid") {
					t.Fatalf("frontend final %s: %v", name, e)
				}
			}
			if !contractSame(before, verificationTree(t, root)) {
				t.Fatal("frontend query changed evidence")
			}
		})
	}
	apTestPut(t, root, meta.Acceptance, valid)
	for _, name := range []string{"valid", "wrong-slice"} {
		t.Run("public-frontend-task-"+name, func(t *testing.T) {
			changed.Store(false)
			s := apTestSession(t, root)
			role := taskRole(s, "role.architecture-agent")
			if role == nil {
				t.Fatal("frontend role absent")
			}
			task := apTestTask(t, root, 1)
			task["work_unit_id"] = "work-unit.frontend-engineering-design"
			task["role_id"] = "role.architecture-agent"
			task["stage_id"] = "stage.frontend-engineering-design"
			task["allowed_write_paths"] = []any{"frontend-input-report.json"}
			task["skill_source"] = map[string]any{"registry_ref": approvalRolesRef, "defaults_ref": "taskPackageDefaults(role.architecture-agent)", "core_skills": role["core_skills"], "forbidden_skills": role["forbidden_skills"]}
			task["convergence"] = map[string]any{"parent_work_unit": "work-unit.frontend-engineering-design", "convergence_ref": "AGENTS.md"}
			task["slice_id"] = meta.Slice
			bound, _ := s.bind(meta.Acceptance)
			task["frontend_delivery"] = map[string]any{"acceptance_ref": meta.Acceptance, "digest": bound.Digest}
			if name == "wrong-slice" {
				task["slice_id"] = "slice.different"
			}
			taskRef := "frontend-task.json"
			if name != "valid" {
				taskRef = "frontend-task-" + name + ".json"
			}
			apTestPut(t, root, taskRef, task)
			before := verificationTree(t, root)
			_, e := apTestRun(root, "task", taskRef, map[string]string{"profile": "frontend", "tool-root": old})
			if (e == nil) != (name == "valid") {
				t.Fatalf("public frontend task %s: %v", name, e)
			}
			if !contractSame(before, verificationTree(t, root)) {
				t.Fatal("public frontend input check changed evidence")
			}
			expectedExit := 1
			if name == "valid" {
				expectedExit = 0
			}
			contractTestRetainFixture(t, root, "frontend-online-task-"+name, map[string]any{"group": "contract", "action": "verify", "kind": "task", "file": taskRef, "profile": "frontend", "tool-root": old, "mount_roots": []any{meta.Root}, "expected_exit": expectedExit, "http_probe": map[string]any{"schema_version": 1, "listen_address": server.Listener.Addr().String(), "routes": []any{map[string]any{"method": "GET", "path": "/version", "status": 200, "response_json": revision.Load(), "response_headers": map[string]any{"Content-Type": "application/json"}}}, "env": map[string]any{}}})
		})
	}
}

// Optional local evidence export for the parent CLI integration harness. It
// retains only synthetic inputs; this never grants or manufactures approval.
func contractTestOracleTMP(t *testing.T) string {
	t.Helper()
	if target := os.Getenv("YSS_NATIVE_FIXTURE_ROOT"); target != "" {
		if !filepath.IsAbs(target) || filepath.Clean(target) != target {
			t.Fatal("YSS_NATIVE_FIXTURE_ROOT must be canonical absolute path")
		}
		if e := os.MkdirAll(target, 0755); e != nil {
			t.Fatal(e)
		}
		return target
	}
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	return root
}
func contractTestRetainFixture(t *testing.T, root, name string, metadata map[string]any) {
	t.Helper()
	base := os.Getenv("YSS_NATIVE_FIXTURE_ROOT")
	if base == "" {
		return
	}
	if !filepath.IsAbs(base) || filepath.Clean(base) != base || !regexp.MustCompile(`^[a-z0-9-]+$`).MatchString(name) {
		t.Fatal("invalid native fixture export")
	}
	if e := os.MkdirAll(base, 0755); e != nil {
		t.Fatal(e)
	}
	rel, e := filepath.Rel(base, root)
	if e != nil {
		t.Fatal(e)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || metadata["isolate_root"] == true {
		originalRoot := root
		target, e := os.MkdirTemp(base, name+"-")
		if e != nil {
			t.Fatal(e)
		}
		if e = filepath.WalkDir(root, func(file string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("fixture export rejects symlink")
			}
			ref, err := filepath.Rel(root, file)
			if err != nil {
				return err
			}
			to := filepath.Join(target, ref)
			if d.IsDir() {
				return os.MkdirAll(to, 0755)
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			b, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			return os.WriteFile(to, b, info.Mode().Perm())
		}); e != nil {
			t.Fatal(e)
		}
		root = target
		if metadata["isolate_root"] == true {
			metadata = contractCopy(metadata)
			metadata["mount_roots"] = append(semList(metadata["mount_roots"]), originalRoot)
		}
	}
	metadata = contractCopy(metadata)
	metadata["root"], metadata["synthetic"] = root, true
	argv := []any{text(metadata["group"]), text(metadata["action"]), "--root", root}
	for _, key := range []string{"profile", "kind", "file", "checkpoint", "task", "tool-root", "template-checkout", "consumer", "unit", "gate", "approval-ref"} {
		if value := text(metadata[key]); value != "" {
			flag := key
			if key == "file" && text(metadata["group"]) == "handoff" && text(metadata["kind"]) == "package" {
				flag = "package"
			}
			argv = append(argv, "--"+flag, value)
		}
	}
	argv = append(argv, "--json")
	metadata["argv"], metadata["name"] = argv, name
	mounts := []any{root}
	for _, value := range append(semStrings(metadata["mount_roots"]), text(metadata["tool-root"]), text(metadata["template-checkout"])) {
		if value != "" && !semHas(mounts, value) {
			mounts = append(mounts, value)
		}
	}
	metadata["mount_roots"] = mounts
	apTestPut(t, base, name+".json", metadata)
}

// Choose the retained root before generating any absolute synthetic references.
func contractTestRetainedRoot(t *testing.T, root, name string) string {
	t.Helper()
	if os.Getenv("YSS_NATIVE_FIXTURE_ROOT") == "" {
		return root
	}
	base := contractTestOracleTMP(t)
	target, e := os.MkdirTemp(base, name+"-")
	if e != nil {
		t.Fatal(e)
	}
	if e = filepath.WalkDir(root, func(file string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		ref, err := filepath.Rel(root, file)
		if err != nil {
			return err
		}
		to := filepath.Join(target, ref)
		if d.IsDir() {
			return os.MkdirAll(to, 0755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("fixture root refuses non-regular source")
		}
		b, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		return os.WriteFile(to, b, info.Mode().Perm())
	}); e != nil {
		t.Fatal(e)
	}
	return target
}

func TestContractSelectLocalUnitSpecialistResponsibilities(t *testing.T) {
	for _, role := range []string{"role.backend-engineer", "role.frontend-engineer", "role.backend-agent", "role.frontend-agent"} {
		c := &nativeSlice{Raw: map[string]any{"schema_version": json.Number("3"), "contract_id": "approved"}, Normalized: map[string]any{"common": map[string]any{"project_roots": []any{"project"}, "allowed_write_paths": []any{"src"}}, "work_units": []any{map[string]any{"id": "unit", "role_id": role, "project_root": "project", "allowed_write_paths": []any{"src/owned"}}}, "backend": map[string]any{"status": "required"}, "frontend": map[string]any{"status": "required"}}}
		before := contractDigest(c.Normalized)
		selected, err := contractSelectLocalUnit(&semanticSession{}, c, "unit")
		if err != nil {
			t.Fatal(err)
		}
		if selected.Raw["contract_id"] != "approved" || contractDigest(c.Normalized) != before {
			t.Fatal("source identity or view mutated")
		}
		if got := semMap(selected.Normalized["common"]); !contractSame(got["project_roots"], []any{"project"}) || !contractSame(got["allowed_write_paths"], []any{"src/owned"}) {
			t.Fatal("frozen scope was not selected", got)
		}
		opposite := "backend"
		if strings.Contains(role, "backend") {
			opposite = "frontend"
		}
		if semMap(selected.Normalized[opposite])["status"] != "not-applicable" {
			t.Fatal("opposite responsibility retained")
		}
		semMap(apFind(c.Normalized["work_units"], "id", "unit"))["role_id"] = "role.requirements-manager"
		if _, err = contractSelectLocalUnit(&semanticSession{}, c, "unit"); err == nil {
			t.Fatal("analysis role accepted")
		}
	}
}
