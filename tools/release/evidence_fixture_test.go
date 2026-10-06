package release_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/tools/release"
)

func asMap(v any) map[string]any {
	b, _ := json.Marshal(v)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

func completeFixtureInspection(f *fixture, profile string) map[string]any {
	out := asMap(f.input.Bundles[profile])
	out["schemaVersion"] = 2
	out["profile"] = profile
	out["producer"] = map[string]any{"version": "bundle-producer-v2", "commit": commit, "sourceState": "committed"}
	out["sourcePolicy"] = map[string]any{"path": ".template-source/distribution/bundle-profile.json", "digest": hash([]byte("policy")), "kind": "committed"}
	out["manifest"] = map[string]any{"allowRootFiles": []string{"README.md"}}
	out["distribution"] = map[string]any{}
	out["files"] = map[string]any{"README.md": map[string]any{"digest": hash([]byte("asset")), "mode": 0644, "size": 5, "ownership": "managed"}}
	out["initialPaths"] = []string{"README.md"}
	return out
}

var publicPluginLabels = []string{"go-head", "go-tree", "template-head", "template-clean", "go-before-tracked", "go-before-files", "template-before-tracked", "template-before-files", "binary-version", "python-jsonschema", "spec-inspect", "spec-build", "spec-staged-inspect", "spec-verify", "spec-doctor", "spec-preview", "spec-apply", "spec-binding-check", "spec-readonly-project-recover", "spec-readonly-project-rollback", "spec-whole-rollback", "spec-repeat-project-rollback", "spec-repeat-project-recover", "spec-repeat-project-rollback", "spec-repeat-project-recover", "design-inspect", "design-build", "design-staged-inspect", "design-verify", "design-doctor", "design-preview", "design-apply", "design-binding-check", "design-readonly-project-recover", "design-readonly-project-rollback", "design-whole-rollback", "design-repeat-project-rollback", "design-repeat-project-recover", "design-repeat-project-rollback", "design-repeat-project-recover", "go-after-tracked", "go-after-files", "template-after-tracked", "template-after-files"}

func (f *fixture) pluginEvidence(t *testing.T, prefix, platform string) map[string]any {
	parts := strings.Split(platform, "/")
	osname, arch := parts[0], parts[1]
	if osname == "windows" {
		osname = "win32"
	}
	if arch == "amd64" {
		arch = "x64"
	}
	descriptor := map[string]any{"type": "file", "mode": float64(0644), "sha256": hash([]byte("source"))}
	inventory := map[string]any{"sha256": hash([]byte("inventory")), "files": 1, "inventory": []any{map[string]any{"ref": "source.go", "type": "file", "mode": 0644, "sha256": hash([]byte("source"))}}}
	before := map[string]any{"go": inventory, "template": inventory}
	commands := []any{}
	for n, label := range publicPluginLabels {
		out := f.file(t, prefix+"/plugin-"+label+"-"+string(rune('a'+n))+".stdout.log", nil)
		err := f.file(t, prefix+"/plugin-"+label+"-"+string(rune('a'+n))+".stderr.log", nil)
		commands = append(commands, map[string]any{"label": label, "executable": "parser-fixture", "args": []string{"fixture"}, "exit_observed": true, "exit_code": 0, "signal": nil, "error": nil, "stdout": map[string]any{"file": strings.TrimPrefix(out.Path, prefix+"/"), "sha256": out.SHA256}, "stderr": map[string]any{"file": strings.TrimPrefix(err.Path, prefix+"/"), "sha256": err.SHA256}})
	}
	cases := []any{}
	for _, p := range []string{"spec", "design"} {
		business := []any{map[string]any{"ref": "src/user-owned/readonly.txt", "type": "file", "mode": 0444, "sha256": hash([]byte("readonly"))}, map[string]any{"ref": ".github/workflows/user-owned-readonly.yml", "type": "file", "mode": 0444, "sha256": hash([]byte("readonly"))}}
		plugin := "yss-product-design"
		if p == "spec" {
			plugin = "yss-backend-delivery"
		}
		cases = append(cases, map[string]any{"profile": p, "plugin": plugin, "status": "passed", "init_plan_id": "literal-public-plan", "installed_metadata": descriptor, "installed_binding": descriptor, "business_before": business, "business_after": business, "readonly_preview": true, "readonly_recovery": true, "whole_atomic_rollback": true, "repeated_recovery": true})
	}
	return map[string]any{"schemaVersion": 1, "kind": "native-plugin-public-smoke", "status": "passed", "platform": map[string]any{"os": osname, "arch": arch}, "binary_sha256": hash(binaries(t)[platform]), "binary_after_sha256": hash(binaries(t)[platform]), "input_drift": false, "binary_drift": false, "go_head": commit, "go_tree": commit, "template_head": commit, "source_lock_sha256": f.input.SourceLockSHA256, "binary_version": map[string]any{"version": "1.0.0", "protocolVersion": 1, "cliCommit": commit, "sourceState": "committed"}, "inputs_before": before, "inputs_after": before, "cases": cases, "commands": commands, "unexecuted": []string{"outside-this-gate-deferred"}}
}

func (f *fixture) evidence(t *testing.T, prefix, kind string, schema int, v any) release.SourceReport {
	return release.SourceReport{Kind: kind, SchemaVersion: schema, Report: f.json(t, prefix+"/"+kind+".json", v)}
}
func (f *fixture) gateEvidence(t *testing.T, id string) map[string]any {
	binary := hash(binaries(t)["darwin/arm64"])
	prefix := "gates/" + id
	sources := []release.SourceReport{}
	log := f.file(t, prefix+"/raw.log", []byte("actual parser fixture log\n"))
	sources = append(sources, release.SourceReport{Kind: "evidence-file", Report: log})
	legacyNames := []string{"warehouse-fixed-executor-init", "legacy-requires-explicit-migration", "migration-plan-read-only", "migration-apply", "native-doctor", "migration-rollback", "fixed-executor-maintains-restored-instance", "repeat-rollback", "legacy-pending-migration-refusal", "unfinished-fixed-executor-recovery", "modified-after-apply-rollback-refusal", "protection-bytes-and-mode"}
	pins, legacyRows := []any{}, []any{}
	for _, p := range []string{"spec", "design", "backend", "frontend"} {
		pkg := f.file(t, prefix+"/old-"+p+".tgz", []byte("fixed old "+p))
		sources = append(sources, release.SourceReport{Kind: "evidence-file", Report: pkg})
		pins = append(pins, map[string]any{"family": p, "ref": "/old/" + p, "sha256": pkg.SHA256, "version": "3.0.0", "cli_commit": commit})
		for _, name := range legacyNames {
			var code any
			exit := 0
			switch name {
			case "legacy-requires-explicit-migration":
				code = "MIGRATION_REQUIRED"
				exit = 1
			case "legacy-pending-migration-refusal":
				code = "LEGACY_INTERRUPTED"
				exit = 1
			case "modified-after-apply-rollback-refusal":
				code = "CONCURRENT"
				exit = 1
			}
			legacyRows = append(legacyRows, map[string]any{"family": p, "case": name, "passed": true, "exit_code": exit, "expected_code": code, "legacy_package_sha256": pkg.SHA256, "log_ref": "raw.log", "log_sha256": log.SHA256})
		}
	}
	legacy := map[string]any{"schema_version": 1, "kind": "native-legacy-recovery-matrix", "fixture": false, "status": "passed", "binary_sha256": binary, "input_drift": false, "unexecuted": []any{}, "families": []string{"spec", "design", "backend", "frontend"}, "required_cases": legacyNames, "packages": pins, "cases": legacyRows}
	legacyRef := f.evidence(t, prefix, "native-legacy-recovery-matrix", 1, legacy)
	sources = append(sources, legacyRef)
	if id == "full-template-integration" || id == "cli-integration" {
		entries := []any{}
		artifacts := []any{}
		integrations := []any{}
		commands := []any{}
		for _, p := range []string{"spec", "design", "backend", "frontend"} {
			b := f.input.Bundles[p]
			tuple := map[string]any{"namespace": "candidate-release", "family": p, "cli_commit": commit, "template_commit": b.TemplateCommit, "core_commit": commit, "package_name": "yss", "version": "1.0.0", "template_version": b.TemplateVersion, "source_contract_version": 2, "protocol_version": 1, "snapshot_hash": b.SourceSnapshotHash, "manifest_hash": b.ManifestHash, "bundle_hash": b.BundleHash, "binary_sha256": binary}
			entries = append(entries, tuple)
			bin := "/fixture/" + p + "/yss"
			artifacts = append(artifacts, map[string]any{"source_tuple": tuple, "binary": bin, "tarball_sha256": binary, "snapshot_sha256": b.SourceSnapshotHash, "manifest_sha256": b.ManifestHash, "installed_tree_sha256": hash([]byte("tree"))})
			integrations = append(integrations, map[string]any{"family": p, "status": "passed", "migration_fixture": "native-managed-upgrade", "historical_recovery": "separate-required-gate", "cases": []string{"init", "doctor", "diff", "sync-dry-run", "sync-apply", "migration-plan-read-only", "migration-apply", "idempotency", "rollback", "user-file-preservation"}})
			for _, args := range [][]string{{"bundle", "export"}, {"init", "--plan"}, {"init", "--apply"}, {"doctor"}, {"diff"}, {"sync"}, {"migrate", "plan"}, {"migrate", "plan"}, {"migrate", "apply"}, {"migrate", "rollback"}} {
				commands = append(commands, map[string]any{"command": bin, "args": args, "exit_code": 0, "actual_exit_code": 0, "actual_exit_code_observed": true, "actual_exit_signal": nil, "log": "raw.log", "log_sha256": log.SHA256})
			}
		}
		manifest := map[string]any{"schema_version": 2, "kind": "template-release-sources", "root_commit": commit, "families": []string{"spec", "design", "backend", "frontend"}, "entries": entries}
		sourceRef := f.evidence(t, prefix, "template-release-sources", 2, manifest)
		sources = append(sources, sourceRef)
		full := map[string]any{"schema_version": 2, "kind": "template-verification-report", "purpose": "verification", "status": "passed", "root": f.expected.TemplateRoot, "scope": map[string]any{"kind": "complete-candidate"}, "plan": f.expected.VerificationPlan, "invocation": f.expected.VerificationInvocation, "final_exit": map[string]any{"code": 0, "signal": nil, "observed": true}, "input_sha256": f.expected.VerificationInputSHA256, "input_after_sha256": f.expected.VerificationInputSHA256, "input_drift": false, "unexecuted": []any{}, "sources_manifest": manifest, "started_at": "2026-10-06T00:00:00Z", "finished_at": "2026-10-06T00:00:01Z", "results": []any{map[string]any{"task_id": "task.fixture", "command": "fixture check", "code": 0, "actual_exit_code": 0, "actual_exit_code_observed": true, "actual_exit_signal": nil, "stdoutFile": "raw.log", "stderrFile": "raw.log", "log_digests": map[string]any{"stdoutFile": log.SHA256, "stderrFile": log.SHA256}}}, "gate_results": []any{map[string]any{"id": "check.fixture", "status": "passed", "task_ids": []string{"task.fixture"}}}}
		fullRef := f.evidence(t, prefix, "template-verification-report", 2, full)
		sources = append(sources, fullRef)
		outer := map[string]any{"schema_version": 2, "kind": "template-release-verification", "purpose": "verification", "status": "passed", "requested_commit": commit, "cli_families": []string{"spec", "design", "backend", "frontend"}, "started_at": "2026-10-06T00:00:00Z", "finished_at": "2026-10-06T00:00:01Z", "commands": commands, "full_verification": map[string]any{"report": "/fixture/full/report.json", "report_sha256": fullRef.Report.SHA256, "input_sha256": f.expected.VerificationInputSHA256, "status": "passed"}, "sources_manifest": manifest, "sources_manifest_sha256": sourceRef.Report.SHA256, "artifacts": artifacts, "cli_integrations": integrations, "legacy_recovery": map[string]any{"sha256": legacyRef.Report.SHA256, "binary_sha256": binary, "families": []string{"spec", "design", "backend", "frontend"}, "cases": 48}}
		sources = append(sources, f.evidence(t, prefix, "template-release-verification", 2, outer))
	}
	if id == "real-project-isolation" {
		rows := []any{}
		for _, name := range release.RequiredGateCoverage(id) {
			row := map[string]any{"case": name, "passed": true, "protected_unchanged": true, "snapshot_mismatch_preserved": true, "whole_tree_unchanged": true, "whole_inventory_restored": true, "all_inventory_including_history_unchanged": true, "same_full_public_doctor_report": true, "whole_inventory_including_history_unchanged": true}
			switch name {
			case "legacy-migration-required-refusal":
				row["expected_code"] = "MIGRATION_REQUIRED"
				row["actual_exit_code"] = 1
			case "modified-after-apply-rollback-refusal":
				row["expected_code"] = "CONCURRENT"
				row["actual_exit_code"] = 1
			}
			rows = append(rows, row)
		}
		commits := map[string]string{}
		for p, b := range f.input.Bundles {
			commits[p] = b.TemplateCommit
		}
		real := map[string]any{"schema_version": 1, "kind": "native-real-spec-isolated-migration", "fixture": false, "status": "passed", "binary_sha256": binary, "input_drift": false, "unexecuted": []any{}, "original_project_written": false, "business_delivery_claimed": false, "source_project": "/actual/original", "required_cases": release.RequiredGateCoverage(id), "cases": rows, "root_binding": map[string]any{"authorized_by_root": true, "binary_sha256": binary, "version": "1.0.0", "source_state": "committed", "cli_commit": commit, "template_commits": commits, "root_authorization_ref": "root-freeze"}, "commands": []any{map[string]any{"actual_exit_code": 0, "actual_exit_code_observed": true, "actual_signal": nil, "timed_out": false, "expected_code": "OK", "stdout_sha256": log.SHA256, "stderr_sha256": log.SHA256}}}
		sources = append(sources, f.evidence(t, prefix, "native-real-spec-isolated-migration", 1, real))
		inventory := map[string]any{}
		for _, name := range []string{"CONTEXT.md", ".yss-template.json", "yss-project.yaml", ".git/index"} {
			inventory[name] = map[string]any{"type": "file", "mode": 0644, "sha256": hash([]byte(name))}
		}
		for _, kind := range []string{"real-original-before", "real-original-after", "real-whole-restored", "real-protected-before"} {
			sources = append(sources, f.evidence(t, prefix, kind, 0, inventory))
		}
	}
	return asMap(release.GateExecution{SchemaVersion: 1, Kind: "stable-release-gate-execution", GateID: id, QualificationScope: f.expected.QualificationScope, Identity: f.input.Identity, Platform: "darwin/arm64", BinarySHA256: binary, Status: "passed", ExitCode: pointer(0), Signal: json.RawMessage("null"), Error: json.RawMessage("null"), InputDrift: pointer(false), Unexecuted: []string{}, Coverage: release.RequiredGateCoverage(id), SourceReports: sources})
}
