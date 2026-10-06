package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

type dailyFixture struct {
	root, repo, base string
	evidence         map[string]any
	sections         string
}

func dailyWrite(t *testing.T, root, ref string, content []byte) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(ref))
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, content, 0644); err != nil {
		t.Fatal(err)
	}
}
func dailyGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = root
	c.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	b, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, b)
	}
	return strings.TrimSpace(string(b))
}
func newDailyFixture(t *testing.T) *dailyFixture {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &dailyFixture{root: filepath.Join(dir, "project"), repo: filepath.Join(dir, "implementation")}
	os.MkdirAll(f.repo, 0755)
	dailyWrite(t, f.root, "yss-project.yaml", []byte("schema_version: 1\nrepository_mode: project-instance\n"))
	dailyWrite(t, f.root, "CONTEXT.md", []byte("# 业务上下文\n"))
	dailyWrite(t, f.root, "AGENTS.md", []byte("# Agent 入口\n"))
	managed := map[string]any{}
	mb, _ := json.Marshal(managed)
	meta := map[string]any{"schemaVersion": 1, "protocolVersion": 1, "profile": "spec", "profileId": "harness.spec-template", "cliVersion": domain.Version, "templateVersion": "1.0.0", "legacyCliVersion": "3.5.10", "templateCommit": strings.Repeat("a", 40), "snapshotHash": strings.Repeat("b", 64), "manifestHash": strings.Repeat("c", 64), "templateSourceState": "committed", "managedFiles": managed, "baselineDigest": safefs.Digest(mb), "variables": map[string]any{}, "distribution": map[string]any{}}
	raw, _ := json.Marshal(meta)
	dailyWrite(t, f.root, ".yss.json", raw)
	dailyWrite(t, f.root, ".agents/skills/yss-product-lifecycle/references/orchestration-contract.yaml", []byte("request_triage:\n  delivery_path:\n    version: 1\n    enabled_profiles: [spec]\n    eligible_requirements: [goal, acceptance, single_repository, current_baseline, skills, commands, rollback, known_risk]\n    excluded_impacts: [data-migration, cross-repository, platform-conversion, architecture-conversion, permission-change, deployment, release, external-approval, breaking-api, unknown-api]\n    active_task_bindings: preserve-governed\n    single_record: markdown-with-embedded-evidence\n    compatible_api: conservative-additive\n    read_only_capabilities: [lifecycle.route, lifecycle.verify-daily]\n    completion_requires: [current-scope, passing-actual-tests, independent-review, no-open-blocking-findings, current-api-evidence-if-applicable]\n"))
	dailyWrite(t, f.root, "skill.md", []byte("# 适用技能\n"))
	dailyWrite(t, f.repo, "main.txt", []byte("before\n"))
	dailyGit(t, f.repo, "init", "-q")
	dailyGit(t, f.repo, "add", ".")
	dailyGit(t, f.repo, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "baseline")
	f.base = dailyGit(t, f.repo, "rev-parse", "HEAD")
	f.evidence = map[string]any{"delivery_path": "daily", "task_id": "task.example", "goal_ref": "#目标", "acceptance_ref": "#验收", "repository": map[string]any{"root": f.repo, "baseline_sha": f.base, "baseline_ref": "#工程基线", "rollback_ref": "#回滚"}, "scope": map[string]any{"paths": []any{"main.txt"}, "impacts": []any{}, "risk_ref": "#范围评估"}, "formal_bindings": []any{}, "skills": []any{map[string]any{"ref": "skill.md", "digest": "sha256:" + safefs.Digest([]byte("# 适用技能\n"))}}, "commands": []any{map[string]any{"argv": []any{"go", "test", "./..."}, "cwd": "."}}, "inputs": []any{}, "api": map[string]any{"mode": "none", "reason": "纯文本维护"}}
	f.save(t)
	return f
}
func (f *dailyFixture) save(t *testing.T) {
	t.Helper()
	b, _ := json.MarshalIndent(f.evidence, "", "  ")
	body := "# 普通 Ticket\n\n## 目标\n修改指定文件。\n\n## 验收\n目标文本正确。\n\n## 工程基线\n工程及既有验证命令已经确认。\n\n## 回滚\n恢复基线中指定文件。\n\n## 范围评估\n单仓可回滚，无外部副作用。\n\n" + f.sections + "<!-- yss-task-evidence -->\n```json\n" + string(b) + "\n```\n"
	dailyWrite(t, f.root, "task.md", []byte(body))
}

func (f *dailyFixture) complete(t *testing.T) map[string]any {
	t.Helper()
	code, env := f.run(t, "route")
	if code != 0 {
		t.Fatalf("route before evidence: %d %#v", code, env)
	}
	result := env["result"].(map[string]any)
	testLog := dailyExecutionJSON(f.repo, []string{"go", "test", "./..."}, 0, "candidate_digest", result["candidate_digest"].(string))
	review := dailyReviewJSON("reviewer.agent", "candidate_digest", result["candidate_digest"].(string))
	f.sections = "## 实际测试\n" + testLog + "## 独立审查\n" + review
	f.evidence["implementation"] = map[string]any{"actor_id": "implementer.agent", "diff_digest": result["diff_digest"], "changed_files": result["changed_files"]}
	f.evidence["tests"] = []any{map[string]any{"command_index": 0, "exit_code": 0, "log_ref": "#实际测试", "log_digest": "sha256:" + safefs.Digest([]byte(testLog)), "candidate_digest": result["candidate_digest"]}}
	f.evidence["review"] = map[string]any{"reviewer_id": "reviewer.agent", "result": "passed", "blocking_findings": []any{}, "ref": "#独立审查", "digest": "sha256:" + safefs.Digest([]byte(review)), "candidate_digest": result["candidate_digest"]}
	f.save(t)
	return result
}

func TestDailyVerifyAcceptsSingleRecordInlineEvidenceAndRejectsSelfReview(t *testing.T) {
	f := newDailyFixture(t)
	dailyWrite(t, f.repo, "main.txt", []byte("after\n"))
	f.complete(t)
	code, env := f.run(t, "verify-daily")
	if code != 0 {
		t.Fatalf("current evidence rejected: %d %#v", code, env)
	}
	result := env["result"].(map[string]any)
	if result["tests_executed"] != false || result["approval_created"] != false {
		t.Fatal("verification executed or approved work")
	}
	f.evidence["review"].(map[string]any)["reviewer_id"] = "implementer.agent"
	f.save(t)
	if code, env = f.run(t, "verify-daily"); code != 1 {
		t.Fatalf("self-review accepted: %d %#v", code, env)
	}
}

func TestDailyEvidenceBecomesStaleAfterActualDiffOrSkillChanges(t *testing.T) {
	for _, kind := range []string{"diff", "skill"} {
		t.Run(kind, func(t *testing.T) {
			f := newDailyFixture(t)
			dailyWrite(t, f.repo, "main.txt", []byte("after\n"))
			f.complete(t)
			if kind == "diff" {
				dailyWrite(t, f.repo, "main.txt", []byte("after again\n"))
			} else {
				dailyWrite(t, f.root, "skill.md", []byte("# 技能变更\n"))
			}
			if code, env := f.run(t, "verify-daily"); code != 1 {
				t.Fatalf("stale %s evidence accepted: %d %#v", kind, code, env)
			}
		})
	}
}

func TestDailyRouteKeepsRelatedFormalBindingAndIgnoresUnrelatedFormalAsset(t *testing.T) {
	f := newDailyFixture(t)
	dailyWrite(t, f.root, "docs/.scratch/other/task.yaml", []byte("task_id: task.other\ncontract:\n  kind: slice-implementation\nstatus: stale\n"))
	if code, env := f.run(t, "route"); code != 0 || env["result"].(map[string]any)["delivery_path"] != "daily" {
		t.Fatalf("unrelated formal blocked daily: %d %#v", code, env)
	}
	dailyWrite(t, f.root, "docs/.scratch/example/history.yaml", []byte("task_id: task.example\ncontract:\n  kind: slice-implementation\nstatus: completed\n"))
	code, env := f.run(t, "route")
	if code != 0 || env["result"].(map[string]any)["delivery_path"] != "governed" {
		t.Fatalf("history binding downgraded: %d %#v", code, env)
	}
	if code, env = f.run(t, "verify-daily"); code != 1 {
		t.Fatalf("governed task verified as daily: %d %#v", code, env)
	}
}

func TestDailyRouteRejectsOutOfScopeDiffAndCannotHideCodeAsLog(t *testing.T) {
	f := newDailyFixture(t)
	dailyWrite(t, f.repo, "other.txt", []byte("unauthorized\n"))
	f.evidence["tests"] = []any{map[string]any{"log_ref": "other.txt"}}
	f.save(t)
	if code, env := f.run(t, "route"); code != 1 {
		t.Fatalf("out of scope accepted: %d %#v", code, env)
	}
}

func TestDailyRouteNeedsExplicitParametersAndReadableFacts(t *testing.T) {
	for _, kind := range []string{"base", "repository", "legacy-record", "acceptance", "policy-version", "profile-capability", "duplicate-flag"} {
		t.Run(kind, func(t *testing.T) {
			f := newDailyFixture(t)
			var extra []string
			switch kind {
			case "base":
				f.base = "HEAD"
			case "repository":
				f.evidence["repository"].(map[string]any)["root"] = "/different"
				f.save(t)
			case "legacy-record":
				dailyWrite(t, f.root, "task.md", []byte("# old task\n"))
			case "acceptance":
				f.evidence["acceptance_ref"] = "#missing"
				f.save(t)
			case "policy-version":
				p := filepath.Join(f.root, filepath.FromSlash(".agents/skills/yss-product-lifecycle/references/orchestration-contract.yaml"))
				b, _ := os.ReadFile(p)
				dailyWrite(t, f.root, ".agents/skills/yss-product-lifecycle/references/orchestration-contract.yaml", bytes.Replace(b, []byte("version: 1"), []byte("version: 2"), 1))
			case "profile-capability":
				dailyWrite(t, f.root, ".agents/skills/yss-product-lifecycle/references/orchestration-contract.yaml", []byte("request_triage: {}\n"))
			case "duplicate-flag":
				extra = []string{"--base", f.base}
			}
			code, env := f.run(t, "route", extra...)
			want := 2
			if code != want {
				t.Fatalf("%s: got %d want %d %#v", kind, code, want, env)
			}
		})
	}
	f := newDailyFixture(t)
	var out, stderr bytes.Buffer
	code := Run(context.Background(), []string{"lifecycle", "route", "--root", f.root, "--task", "task.md", "--json"}, &out, &stderr)
	if code != 2 {
		t.Fatalf("missing explicit repository/base not rejected: %d %s", code, out.String())
	}
}
func (f *dailyFixture) run(t *testing.T, action string, extra ...string) (int, map[string]any) {
	t.Helper()
	args := []string{"lifecycle", action, "--root", f.root, "--implementation-root", f.repo, "--base", f.base, "--task", "task.md", "--profile", "spec", "--json"}
	args = append(args, extra...)
	var out, stderr bytes.Buffer
	code := Run(context.Background(), args, &out, &stderr)
	var envelope map[string]any
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("invalid JSON %d: %s %s", code, out.String(), stderr.String())
	}
	return code, envelope
}

func TestDailyRouteUsesExplicitRepositoryAndReadableMarkdownBaseline(t *testing.T) {
	f := newDailyFixture(t)
	code, env := f.run(t, "route")
	if code != 0 {
		t.Fatalf("route failed: %d %#v", code, env)
	}
	result := env["result"].(map[string]any)
	if result["delivery_path"] != "daily" || result["read_only"] != true || result["approval_created"] != false || result["execution_authorization"] != "not-evaluated" {
		t.Fatalf("route contract: %#v", result)
	}
	if !strings.HasPrefix(result["candidate_digest"].(string), "sha256:") {
		t.Fatal("missing current candidate digest")
	}
}

func newDailyAPIFixture(t *testing.T) *dailyFixture {
	t.Helper()
	f := newDailyFixture(t)
	baseline := "openapi: 3.1.0\ninfo: {title: Fixture, version: 1.0.0}\npaths:\n  /old:\n    get:\n      operationId: getOld\n      responses:\n        '200':\n          description: ok\n          content:\n            application/json:\n              schema:\n                $ref: './schema.yaml#/Item'\n"
	dailyWrite(t, f.repo, "api.yaml", []byte(baseline))
	dailyWrite(t, f.repo, "schema.yaml", []byte("Item:\n  type: object\n  properties:\n    id: {type: string}\n"))
	dailyGit(t, f.repo, "add", ".")
	dailyGit(t, f.repo, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "api baseline")
	f.base = dailyGit(t, f.repo, "rev-parse", "HEAD")
	f.evidence["repository"].(map[string]any)["baseline_sha"] = f.base
	candidate := baseline + "  /new:\n    get:\n      operationId: getNew\n      responses:\n        '200':\n          description: ok\n          content:\n            application/json:\n              schema: {type: string}\n"
	dailyWrite(t, f.repo, "api.yaml", []byte(candidate))
	lock := []byte(`{"packages":{"node_modules/@redocly/cli":{"version":"1.2.3"}}}`)
	dailyWrite(t, f.root, "package-lock.json", lock)
	f.evidence["scope"].(map[string]any)["paths"] = []any{"main.txt", "api.yaml", "schema.yaml"}
	f.evidence["scope"].(map[string]any)["impacts"] = []any{"compatible-additive-api"}
	f.evidence["api"] = map[string]any{"mode": "compatible-additive", "baseline": map[string]any{"ref": "api.yaml", "digest": "sha256:" + safefs.Digest([]byte(baseline))}, "candidate": map[string]any{"ref": "api.yaml", "digest": "sha256:" + safefs.Digest([]byte(candidate))}, "new_operations": []any{map[string]any{"path": "/new", "method": "get", "operation_id": "getNew"}}, "tools": []any{map[string]any{"name": "@redocly/cli", "version": "1.2.3", "lock_ref": "package-lock.json", "digest": "sha256:" + safefs.Digest(lock)}}, "rules": []any{map[string]any{"ref": ".agents/skills/yss-dto/references/openapi-wire-profile.yaml", "digest": "sha256:" + safefs.Digest([]byte("schema_version: 1\nkind: yss-dto-openapi-wire-profile\n"))}}}
	dailyWrite(t, f.root, ".agents/skills/yss-dto/references/openapi-wire-profile.yaml", []byte("schema_version: 1\nkind: yss-dto-openapi-wire-profile\n"))
	f.save(t)
	return f
}

func (f *dailyFixture) completeAPI(t *testing.T) map[string]any {
	t.Helper()
	result := f.complete(t)
	api := f.evidence["api"].(map[string]any)
	apiLog := dailyExecutionJSON(f.repo, []string{"pnpm", "exec", "redocly", "lint", "api.yaml"}, 0, "api_digest", result["api_digest"].(string))
	apiReview := dailyReviewJSON("api-reviewer.agent", "api_digest", result["api_digest"].(string))
	f.sections += "## API校验\n" + apiLog + "## API契约审查\n" + apiReview
	api["lint"] = map[string]any{"tool": "@redocly/cli", "version": "1.2.3", "argv": []any{"pnpm", "exec", "redocly", "lint", "api.yaml"}, "exit_code": 0, "log_ref": "#API校验", "log_digest": "sha256:" + safefs.Digest([]byte(apiLog)), "api_digest": result["api_digest"]}
	api["compatibility"] = map[string]any{"tool": "yss-native-conservative", "api_digest": result["api_digest"]}
	api["wire"] = map[string]any{"applicability": "not-applicable", "reason": "Fixture 新 operation 返回普通 string，无 YSS wrapper"}

	api["review"] = map[string]any{"reviewer_id": "api-reviewer.agent", "result": "passed", "blocking_findings": []any{}, "ref": "#API契约审查", "digest": "sha256:" + safefs.Digest([]byte(apiReview)), "api_digest": result["api_digest"]}
	api["freeze"] = map[string]any{"digest": result["api_digest"]}
	api["contract_tests"] = f.evidence["tests"]
	f.save(t)
	return result
}

func TestDailyAPIOnlyAddsIndependentOperationAndCodeDiffKeepsAPIFreeze(t *testing.T) {
	f := newDailyAPIFixture(t)
	initial := f.completeAPI(t)
	if code, env := f.run(t, "verify-daily"); code != 0 {
		t.Fatalf("compatible additive API rejected: %d %#v", code, env)
	}
	dailyWrite(t, f.repo, "main.txt", []byte("implementation after freeze\n"))
	code, env := f.run(t, "route")
	if code != 0 {
		t.Fatalf("code-only route: %d %#v", code, env)
	}
	next := env["result"].(map[string]any)
	if initial["api_digest"] != next["api_digest"] || initial["candidate_digest"] == next["candidate_digest"] {
		t.Fatal("API freshness was coupled to code diff")
	}
	f.evidence["implementation"].(map[string]any)["diff_digest"] = next["diff_digest"]
	f.evidence["implementation"].(map[string]any)["changed_files"] = next["changed_files"]
	f.evidence["tests"].([]any)[0].(map[string]any)["candidate_digest"] = next["candidate_digest"]
	f.evidence["review"].(map[string]any)["candidate_digest"] = next["candidate_digest"]
	f.save(t)
	if code, env = f.run(t, "verify-daily"); code != 1 {
		t.Fatalf("metadata-only rebinding reused stale actual test/review facts: %d %#v", code, env)
	}
	oldLog := dailyExecutionJSON(f.repo, []string{"go", "test", "./..."}, 0, "candidate_digest", initial["candidate_digest"].(string))
	newLog := dailyExecutionJSON(f.repo, []string{"go", "test", "./..."}, 0, "candidate_digest", next["candidate_digest"].(string))
	f.sections = strings.Replace(f.sections, oldLog, newLog, 1)
	f.evidence["tests"].([]any)[0].(map[string]any)["log_digest"] = "sha256:" + safefs.Digest([]byte(newLog))
	oldReview := dailyReviewJSON("reviewer.agent", "candidate_digest", initial["candidate_digest"].(string))
	newReview := dailyReviewJSON("reviewer.agent", "candidate_digest", next["candidate_digest"].(string))
	f.sections = strings.Replace(f.sections, oldReview, newReview, 1)
	f.evidence["review"].(map[string]any)["digest"] = "sha256:" + safefs.Digest([]byte(newReview))
	f.save(t)
	if code, env = f.run(t, "verify-daily"); code != 0 {
		t.Fatalf("unchanged API freeze was invalidated by code-only diff: %d %#v", code, env)
	}
}

func TestDailyAPIOldReferenceClosureOrUnknownImpactCannotBeLabeledAway(t *testing.T) {
	for _, kind := range []string{"old-ref", "none-label", "tool-lock", "freeze", "api-review-binding"} {
		t.Run(kind, func(t *testing.T) {
			f := newDailyAPIFixture(t)
			f.completeAPI(t)
			switch kind {
			case "old-ref":
				dailyWrite(t, f.repo, "schema.yaml", []byte("Item:\n  type: object\n  properties:\n    id: {type: integer}\n"))
			case "none-label":
				f.evidence["api"] = map[string]any{"mode": "none", "reason": "caller says none"}
				f.save(t)
			case "tool-lock":
				dailyWrite(t, f.root, "package-lock.json", []byte(`{"packages":{"node_modules/lint-tool":{"version":"9.9.9"}}}`))
			case "freeze":
				f.evidence["api"].(map[string]any)["freeze"].(map[string]any)["digest"] = "sha256:" + strings.Repeat("0", 64)
				f.save(t)
			case "api-review-binding":
				f.evidence["api"].(map[string]any)["review"].(map[string]any)["api_digest"] = "sha256:" + strings.Repeat("0", 64)
				f.save(t)
			}
			code, env := f.run(t, "verify-daily")
			if code != 1 {
				t.Fatalf("%s incorrectly accepted: %d %#v", kind, code, env)
			}
			if kind == "old-ref" || kind == "none-label" {
				if env["result"].(map[string]any)["delivery_path"] != "governed" {
					t.Fatalf("%s did not restore governance", kind)
				}
			}
		})
	}
}

func TestDailyNoneAPICannotHideChangedReferencedSchemaOrRemovedOASDeclaration(t *testing.T) {
	for _, kind := range []string{"ref-only", "removed-root"} {
		t.Run(kind, func(t *testing.T) {
			f := newDailyAPIFixture(t)
			if kind == "ref-only" {
				dailyWrite(t, f.repo, "api.yaml", []byte(dailyGit(t, f.repo, "show", f.base+":api.yaml")+"\n"))
				dailyWrite(t, f.repo, "schema.yaml", []byte("Item:\n  type: integer\n"))
			} else {
				dailyWrite(t, f.repo, "api.yaml", []byte("configuration: plain\n"))
			}
			f.evidence["api"] = map[string]any{"mode": "none", "reason": "caller says no API impact"}
			f.save(t)
			code, env := f.run(t, "route")
			if code != 0 || env["result"].(map[string]any)["delivery_path"] != "governed" {
				t.Fatalf("%s escaped API impact check: %d %#v", kind, code, env)
			}
		})
	}
}

func dailyExecutionJSON(root string, argv []string, exit int, binding ...string) string {
	m := map[string]any{"argv": argv, "cwd": root, "exit_code": exit, "executed_at": "2026-10-06T00:00:00Z", "stdout": "controlled fixture execution output"}
	if len(binding) == 2 {
		m[binding[0]] = binding[1]
	}
	b, _ := json.Marshal(m)
	return string(b) + "\n\n"
}

func dailyReviewJSON(reviewer, field, digest string) string {
	b, _ := json.Marshal(map[string]any{"reviewer_id": reviewer, "result": "passed", "blocking_findings": []any{}, field: digest, "summary": "受控 fixture 独立审查结论"})
	return string(b) + "\n\n"
}

func TestDailyDeletedSliceHistoryCannotDowngradeByChangingTaskID(t *testing.T) {
	f := newDailyFixture(t)
	dailyWrite(t, f.root, "docs/.scratch/example/slice.yaml", []byte("schema_version: 3\nslice_id: slice.example\nscope:\n  ticket_ref: task.md\n"))
	dailyGit(t, f.root, "init", "-q")
	dailyGit(t, f.root, "add", ".")
	dailyGit(t, f.root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "formal slice")
	dailyGit(t, f.root, "rm", "docs/.scratch/example/slice.yaml")
	dailyGit(t, f.root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "remove current slice")
	f.evidence["task_id"] = "task.renamed"
	f.save(t)
	code, env := f.run(t, "route")
	if code != 0 || env["result"].(map[string]any)["delivery_path"] != "governed" {
		t.Fatalf("deleted real slice history downgraded: %d %#v", code, env)
	}
}

func TestDailyStageOnlyCheckpointAndHistoricalTicketRemainGoverned(t *testing.T) {
	for _, kind := range []string{"checkpoint", "historical-ticket", "copied-historical-ticket"} {
		t.Run(kind, func(t *testing.T) {
			f := newDailyFixture(t)
			dailyWrite(t, f.root, "docs/.scratch/checkpoint.yaml", []byte("stage: implement\nstatus: active\n"))
			f.evidence["formal_bindings"] = []any{map[string]any{"ref": "docs/.scratch/checkpoint.yaml"}}
			f.save(t)
			if kind != "checkpoint" {
				if kind == "copied-historical-ticket" {
					raw, err := os.ReadFile(filepath.Join(f.root, "task.md"))
					if err != nil {
						t.Fatal(err)
					}
					dailyWrite(t, f.root, "docs/tasks/copy.md", raw)
				}
				dailyGit(t, f.root, "init", "-q")
				dailyGit(t, f.root, "add", ".")
				dailyGit(t, f.root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "bound Ticket")
				dailyGit(t, f.root, "rm", "docs/.scratch/checkpoint.yaml")
				f.evidence["formal_bindings"] = []any{}
				f.evidence["task_id"] = "task.renamed"
				f.save(t)
				dailyGit(t, f.root, "add", "task.md")
				dailyGit(t, f.root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "relabel Ticket")
			}
			code, env := f.run(t, "route")
			if code != 0 || env["result"].(map[string]any)["delivery_path"] != "governed" {
				t.Fatalf("%s binding downgraded: %d %#v", kind, code, env)
			}
		})
	}
}

func TestDailyIndexCannotHideOutOfScopeChangeBehindRestoredWorktree(t *testing.T) {
	f := newDailyFixture(t)
	dailyWrite(t, f.repo, "other.txt", []byte("baseline\n"))
	dailyGit(t, f.repo, "add", "other.txt")
	dailyGit(t, f.repo, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "second baseline file")
	f.base = dailyGit(t, f.repo, "rev-parse", "HEAD")
	f.evidence["repository"].(map[string]any)["baseline_sha"] = f.base
	dailyWrite(t, f.repo, "other.txt", []byte("staged unauthorized change\n"))
	dailyGit(t, f.repo, "add", "other.txt")
	dailyWrite(t, f.repo, "other.txt", []byte("baseline\n"))
	f.save(t)
	if code, env := f.run(t, "route"); code != 1 {
		t.Fatalf("hidden staged change accepted: %d %#v", code, env)
	}
}

func TestDailyReadOnlyRouteNeverExecutesRepositoryCleanFilter(t *testing.T) {
	f := newDailyFixture(t)
	dailyWrite(t, f.repo, ".gitattributes", []byte("main.txt filter=sentinel\n"))
	dailyGit(t, f.repo, "add", ".gitattributes")
	dailyGit(t, f.repo, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "attributes baseline")
	f.base = dailyGit(t, f.repo, "rev-parse", "HEAD")
	f.evidence["repository"].(map[string]any)["baseline_sha"] = f.base
	sentinel := filepath.Join(f.repo, "filter-executed")
	dailyGit(t, f.repo, "config", "filter.sentinel.clean", "touch '"+sentinel+"'; cat")
	dailyWrite(t, f.repo, "main.txt", []byte("after\n"))
	f.save(t)
	if code, env := f.run(t, "route"); code != 0 {
		t.Fatalf("raw readonly observation rejected ordinary change: %d %#v", code, env)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("read-only route executed repository clean filter: %v", err)
	}
}

func TestDailySelectedAPIRootCannotHideSecondChangedContract(t *testing.T) {
	f := newDailyAPIFixture(t)
	dailyWrite(t, f.repo, "api2.yaml", []byte("openapi: 3.1.0\ninfo: {title: Other, version: 1.0.0}\npaths:\n  /other:\n    get:\n      operationId: getOther\n      responses: {'200': {description: ok}}\n"))
	dailyGit(t, f.repo, "add", "api2.yaml")
	dailyGit(t, f.repo, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "other contract baseline")
	f.base = dailyGit(t, f.repo, "rev-parse", "HEAD")
	f.evidence["repository"].(map[string]any)["baseline_sha"] = f.base
	f.evidence["scope"].(map[string]any)["paths"] = []any{"main.txt", "api.yaml", "api2.yaml", "schema.yaml"}
	dailyWrite(t, f.repo, "api2.yaml", []byte("openapi: 3.1.0\ninfo: {title: Other, version: 1.0.0}\npaths: {}\n"))
	f.save(t)
	code, env := f.run(t, "route")
	if code != 0 || env["result"].(map[string]any)["delivery_path"] != "governed" {
		t.Fatalf("second API deletion escaped selected root: %d %#v", code, env)
	}
}

func TestDailyPassedReviewCannotHideOpenBlockingFinding(t *testing.T) {
	for _, kind := range []string{"code", "api"} {
		t.Run(kind, func(t *testing.T) {
			f := newDailyFixture(t)
			if kind == "api" {
				f = newDailyAPIFixture(t)
				f.completeAPI(t)
				f.evidence["api"].(map[string]any)["review"].(map[string]any)["blocking_findings"] = []any{"P1: unresolved contract defect"}
			} else {
				f.complete(t)
				f.evidence["review"].(map[string]any)["blocking_findings"] = []any{"P1: unresolved code defect"}
			}
			f.save(t)
			if code, env := f.run(t, "verify-daily"); code != 1 {
				t.Fatalf("passed %s review hid blocker: %d %#v", kind, code, env)
			}
		})
	}
}

func TestDailyExecutionLogMustMatchActualCommandRepositoryAndExit(t *testing.T) {
	for _, kind := range []string{"argv", "cwd", "exit", "plain-text"} {
		t.Run(kind, func(t *testing.T) {
			f := newDailyFixture(t)
			f.complete(t)
			root, argv, exit := f.repo, []string{"go", "test", "./..."}, 0
			switch kind {
			case "argv":
				argv = []string{"echo", "passed"}
			case "cwd":
				root = f.root
			case "exit":
				exit = 1
			}
			candidate := f.evidence["tests"].([]any)[0].(map[string]any)["candidate_digest"].(string)
			log := dailyExecutionJSON(root, argv, exit, "candidate_digest", candidate)
			want := 1
			if kind == "plain-text" {
				log, want = "arbitrary text claiming pass\n\n", 2
			}
			old := dailyExecutionJSON(f.repo, []string{"go", "test", "./..."}, 0, "candidate_digest", candidate)
			f.sections = strings.Replace(f.sections, old, log, 1)
			f.evidence["tests"].([]any)[0].(map[string]any)["log_digest"] = "sha256:" + safefs.Digest([]byte(log))
			f.save(t)
			if code, env := f.run(t, "verify-daily"); code != want {
				t.Fatalf("%s inconsistent log accepted: %d want %d %#v", kind, code, want, env)
			}
		})
	}
}

func TestDailyAPIToolsMustBeCanonicalAndYSSWrapperCannotClaimWireNA(t *testing.T) {
	for _, kind := range []string{"fake-tool", "wrapper-na"} {
		t.Run(kind, func(t *testing.T) {
			f := newDailyAPIFixture(t)
			if kind == "fake-tool" {
				f.evidence["api"].(map[string]any)["tools"].([]any)[0].(map[string]any)["name"] = "lint-tool"
				f.save(t)
				if code, env := f.run(t, "route"); code != 2 {
					t.Fatalf("fake lint tool allowed: %d %#v", code, env)
				}
				return
			}
			candidate, _ := os.ReadFile(filepath.Join(f.repo, "api.yaml"))
			candidate = bytes.Replace(candidate, []byte("schema: {type: string}"), []byte("schema: {type: object, properties: {code: {type: integer}, data: {type: string}}}"), 1)
			dailyWrite(t, f.repo, "api.yaml", candidate)
			f.evidence["api"].(map[string]any)["candidate"].(map[string]any)["digest"] = "sha256:" + safefs.Digest(candidate)
			f.save(t)
			if code, env := f.run(t, "route"); code != 2 || env["result"].(map[string]any)["delivery_path"] != "needs-info" {
				t.Fatalf("YSS wrapper acquired unsupported target-wire capability: %d %#v", code, env)
			}
		})
	}
}

func TestDailyNoneAPICannotHideArbitraryExtensionReachableRef(t *testing.T) {
	for _, suffix := range []string{"txt", "YAML"} {
		t.Run(suffix, func(t *testing.T) {
			f := newDailyFixture(t)
			root := "openapi: 3.1.0\ninfo: {title: Fixture, version: 1.0.0}\npaths:\n  /old:\n    get:\n      operationId: getOld\n      responses:\n        '200':\n          description: ok\n          content:\n            application/json:\n              schema:\n                $ref: './schema." + suffix + "#/Item'\n"
			dailyWrite(t, f.repo, "api.yaml", []byte(root))
			dailyWrite(t, f.repo, "schema."+suffix, []byte("Item: {type: string}\n"))
			dailyGit(t, f.repo, "add", ".")
			dailyGit(t, f.repo, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "arbitrary extension ref baseline")
			f.base = dailyGit(t, f.repo, "rev-parse", "HEAD")
			f.evidence["repository"].(map[string]any)["baseline_sha"] = f.base
			f.evidence["scope"].(map[string]any)["paths"] = []any{"schema." + suffix}
			dailyWrite(t, f.repo, "schema."+suffix, []byte("Item: {type: integer}\n"))
			f.save(t)
			code, env := f.run(t, "route")
			if code != 0 || env["result"].(map[string]any)["delivery_path"] != "governed" {
				t.Fatalf("%s changed ref evaded actual API graph: %d %#v", suffix, code, env)
			}
		})
	}
}

func TestDailyWireApplicableCannotAcceptEchoOrProfileOnlyEvidence(t *testing.T) {
	for _, argv := range [][]string{{"echo", "verify-yss-dto-openapi-profile"}, {"node", "scripts/verify-yss-dto-openapi-profile.mjs", "api.yaml"}} {
		f := newDailyAPIFixture(t)
		f.completeAPI(t)
		api := f.evidence["api"].(map[string]any)
		log := dailyExecutionJSON(f.repo, argv, 0)
		f.sections += "## wire校验\n" + log
		api["wire"] = map[string]any{"applicability": "applicable", "argv": argv, "exit_code": 0, "log_ref": "#wire校验", "log_digest": "sha256:" + safefs.Digest([]byte(log)), "api_digest": api["freeze"].(map[string]any)["digest"]}
		f.save(t)
		if code, env := f.run(t, "verify-daily"); code != 2 {
			t.Fatalf("non-target wire command accepted: %d %#v", code, env)
		}
	}
}

func TestDailyActualAPIAndReviewFactsCannotBeRenewedOnlyInMetadata(t *testing.T) {
	f := newDailyAPIFixture(t)
	initial := f.completeAPI(t)
	api := f.evidence["api"].(map[string]any)
	rule := []byte("schema_version: 1\nkind: yss-dto-openapi-wire-profile\n# changed actual rule bytes\n")
	dailyWrite(t, f.root, ".agents/skills/yss-dto/references/openapi-wire-profile.yaml", rule)
	api["rules"].([]any)[0].(map[string]any)["digest"] = "sha256:" + safefs.Digest(rule)
	f.save(t)
	code, env := f.run(t, "route")
	if code != 0 {
		t.Fatalf("current API route: %d %#v", code, env)
	}
	next := env["result"].(map[string]any)
	f.evidence["implementation"].(map[string]any)["diff_digest"] = next["diff_digest"]
	f.evidence["implementation"].(map[string]any)["changed_files"] = next["changed_files"]
	oldCodeLog := dailyExecutionJSON(f.repo, []string{"go", "test", "./..."}, 0, "candidate_digest", initial["candidate_digest"].(string))
	newCodeLog := dailyExecutionJSON(f.repo, []string{"go", "test", "./..."}, 0, "candidate_digest", next["candidate_digest"].(string))
	f.sections = strings.Replace(f.sections, oldCodeLog, newCodeLog, 1)
	codeRow := f.evidence["tests"].([]any)[0].(map[string]any)
	codeRow["candidate_digest"] = next["candidate_digest"]
	codeRow["log_digest"] = "sha256:" + safefs.Digest([]byte(newCodeLog))
	codeReview := f.evidence["review"].(map[string]any)
	codeReview["candidate_digest"] = next["candidate_digest"]
	api["lint"].(map[string]any)["api_digest"] = next["api_digest"]
	api["compatibility"].(map[string]any)["api_digest"] = next["api_digest"]
	api["review"].(map[string]any)["api_digest"] = next["api_digest"]
	api["freeze"].(map[string]any)["digest"] = next["api_digest"]
	f.save(t)
	if code, env = f.run(t, "verify-daily"); code != 1 {
		t.Fatalf("unchanged code review body renewed by metadata alone: %d %#v", code, env)
	}
	oldCodeReview := dailyReviewJSON("reviewer.agent", "candidate_digest", initial["candidate_digest"].(string))
	newCodeReview := dailyReviewJSON("reviewer.agent", "candidate_digest", next["candidate_digest"].(string))
	f.sections = strings.Replace(f.sections, oldCodeReview, newCodeReview, 1)
	codeReview["digest"] = "sha256:" + safefs.Digest([]byte(newCodeReview))
	f.save(t)
	if code, env = f.run(t, "verify-daily"); code != 1 {
		t.Fatalf("unchanged lint log renewed by metadata alone: %d %#v", code, env)
	}
	oldLintLog := dailyExecutionJSON(f.repo, []string{"pnpm", "exec", "redocly", "lint", "api.yaml"}, 0, "api_digest", initial["api_digest"].(string))
	newLintLog := dailyExecutionJSON(f.repo, []string{"pnpm", "exec", "redocly", "lint", "api.yaml"}, 0, "api_digest", next["api_digest"].(string))
	f.sections = strings.Replace(f.sections, oldLintLog, newLintLog, 1)
	api["lint"].(map[string]any)["log_digest"] = "sha256:" + safefs.Digest([]byte(newLintLog))
	f.save(t)
	if code, env = f.run(t, "verify-daily"); code != 1 {
		t.Fatalf("unchanged API review body renewed by metadata alone: %d %#v", code, env)
	}
	oldAPIReview := dailyReviewJSON("api-reviewer.agent", "api_digest", initial["api_digest"].(string))
	newAPIReview := dailyReviewJSON("api-reviewer.agent", "api_digest", next["api_digest"].(string))
	f.sections = strings.Replace(f.sections, oldAPIReview, newAPIReview, 1)
	api["review"].(map[string]any)["digest"] = "sha256:" + safefs.Digest([]byte(newAPIReview))
	f.save(t)
	if code, env = f.run(t, "verify-daily"); code != 0 {
		t.Fatalf("all current actual facts were rejected: %d %#v", code, env)
	}
}
