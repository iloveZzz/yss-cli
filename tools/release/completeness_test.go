package release_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/iloveZzz/yss-cli/tools/release"
)

func TestStableAssemblyRejectsIncompletePluginEvidence(t *testing.T) {
	f := newFixture(t)
	editReceipt(t, f, 0, func(r *release.Receipt) {
		editRaw(t, f, &r.Checks[2].Report, func(raw map[string]any) { raw["commands"] = raw["commands"].([]any)[:1] })
	})
	// A claimed successful report cannot qualify with only one actual command.
	err := release.Assemble(f.manifest(t), filepath.Join(f.dir, "out"), f.expected)
	if release.Code(err) != "EVIDENCE" {
		t.Fatalf("incomplete public plugin acceptance qualified: %v", err)
	}
}

func localFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.input.RequiredPlatforms = []string{"darwin/arm64"}
	f.expected.RequiredPlatforms = []string{"darwin/arm64"}
	f.input.Artifacts = []release.Artifact{f.input.Artifacts[1]}
	f.input.QualificationScope = release.LocalQualificationScope
	f.expected.QualificationScope = release.LocalQualificationScope
	f.expected.TemplateRoot = ""
	f.expected.VerificationPlan = nil
	f.expected.VerificationInvocation = nil
	f.expected.VerificationInputSHA256 = ""
	f.input.ReleaseGate = release.FileRef{}
	candidate := filepath.Join(f.dir, "candidate")
	if err := release.AssembleCandidate(f.manifest(t), candidate, f.expected); err != nil {
		t.Fatal(err)
	}
	archiveRef := "candidate/yss_1.0.0_darwin_arm64.tar.gz"
	archiveBytes, err := os.ReadFile(filepath.Join(f.dir, archiveRef))
	if err != nil {
		t.Fatal(err)
	}
	candidateRef := release.FileRef{Path: archiveRef, SHA256: hash(archiveBytes)}
	prefix := "gates/program-installation"
	stdout := f.json(t, prefix+"/stdout.json", map[string]any{"outputVersion": 1, "protocolVersion": 1, "status": "ok", "code": "OK", "result": map[string]any{}})
	stderr := f.file(t, prefix+"/stderr.log", nil)
	docs := map[string]string{}
	for ref, b := range f.expected.Documents {
		docs[ref] = hash(b)
	}
	payload := release.PayloadSHA256("darwin/arm64", f.input.Artifacts[0].Binary.SHA256, docs)
	business := map[string]any{"readonly.txt": map[string]any{"type": "file", "mode": 0444, "sha256": hash([]byte("business"))}}
	cases := []any{map[string]any{"case": "install-plan-read-only", "passed": true, "whole_tree_unchanged": true}, map[string]any{"case": "install-apply", "passed": true, "installed_payload_sha256": payload}, map[string]any{"case": "rollback", "passed": true, "whole_inventory_restored": true}, map[string]any{"case": "recover", "passed": true, "repeated_recovery": true, "no_overwrite": true}, map[string]any{"case": "user-file-preservation", "passed": true, "business_before": business, "business_after": business}}
	commands := []any{}
	for _, action := range []string{"plan", "apply", "recover", "rollback"} {
		commands = append(commands, map[string]any{"args": []string{"/fixture/yss", "update", action}, "actual_exit_code_observed": true, "actual_exit_code": 0, "actual_signal": nil, "timed_out": false, "stdout_sha256": stdout.SHA256, "stderr_sha256": stderr.SHA256})
	}
	program := map[string]any{"schema_version": 1, "kind": "native-program-installation", "fixture": false, "platform": "darwin/arm64", "cli_version": f.input.CLIVersion, "cli_commit": commit, "source_lock_sha256": f.input.SourceLockSHA256, "binary_sha256": f.input.Artifacts[0].Binary.SHA256, "package_payload_sha256": payload, "candidate_archive": candidateRef, "status": "passed", "input_drift": false, "unexecuted": []any{}, "required_cases": release.RequiredGateCoverage("program-installation"), "cases": cases, "commands": commands}
	programSources := []release.SourceReport{f.evidence(t, prefix, "native-program-installation", 1, program), {Kind: "evidence-file", Report: candidateRef}, {Kind: "evidence-file", Report: stdout}, {Kind: "evidence-file", Report: stderr}}
	scopedPrefix := "gates/scoped-template-consumers"
	commandsSource := [][]string{{"scripts/sync-skills", "--check"}, {"scripts/update-skill-lock", "--check"}, {"scripts/sync-profile-skills", "--check", "--profile=all"}, {"scripts/verify-strategic-handoff-tools-lock", "--require-committed"}, {"scripts/verify-lifecycle-registry"}, {"scripts/verify-skill-registry"}, {"node", "--test", "--test-concurrency=1", "tests/read-only-intake.test.mjs", "tests/verification-execution.test.mjs", "tests/instance-metadata.test.mjs", ".template-source/tooling/node/test/retirement.test.mjs"}, {"git", "diff", "--check"}, {"scripts/verify-upstream-skill-source", "--source=iloveZzz/yss-harness-design-agent", "--source-root=/fixed/source/submodules/yss-harness-design-agent"}}
	original := []any{}
	checks := []any{}
	scopedSources := []release.SourceReport{}
	for i, args := range commandsSource {
		log := f.file(t, scopedPrefix+"/source-"+string(rune('0'+i))+".log", nil)
		scopedSources = append(scopedSources, release.SourceReport{Kind: "evidence-file", Report: log})
		original = append(original, map[string]any{"command": args, "cwd": "/fixed/source", "source_sha": commit, "exit_code": 0, "seconds": 0.1, "log": filepath.Base(log.Path)})
		checks = append(checks, map[string]any{"id": release.RequiredGateCoverage("scoped-template-consumers")[i], "original_index": i, "status": "passed", "args": args, "actual_exit_code_observed": true, "actual_exit_code": 0, "actual_signal": nil, "log": log})
	}
	originalRef := f.json(t, scopedPrefix+"/original.json", original)
	scopedSources = append(scopedSources, release.SourceReport{Kind: "evidence-file", Report: originalRef})
	commits := map[string]string{}
	for p, b := range f.input.Bundles {
		commits[p] = b.TemplateCommit
	}
	scoped := map[string]any{"schema_version": 1, "kind": "scoped-template-consumer-verification", "qualification_scope": release.LocalQualificationScope, "current_sources": commits, "source_state": "committed", "inputs_current_git_clean": true, "reused": true, "status": "passed", "input_drift": false, "unexecuted": []any{}, "required_cases": release.RequiredGateCoverage("scoped-template-consumers"), "original_execution": originalRef, "checks": checks}
	scopedSources = append(scopedSources, f.evidence(t, scopedPrefix, "scoped-template-consumer-verification", 1, scoped))
	gateChecks := []release.Check{}
	for _, id := range []string{"program-installation", "legacy-recovery", "scoped-template-consumers"} {
		var wrapper any
		switch id {
		case "program-installation":
			wrapper = asMap(release.GateExecution{SchemaVersion: 1, Kind: "stable-release-gate-execution", GateID: id, QualificationScope: release.LocalQualificationScope, Identity: f.input.Identity, Platform: "darwin/arm64", BinarySHA256: f.input.Artifacts[0].Binary.SHA256, Status: "passed", ExitCode: pointer(0), Signal: json.RawMessage("null"), Error: json.RawMessage("null"), InputDrift: pointer(false), Unexecuted: []string{}, Coverage: release.RequiredGateCoverage(id), SourceReports: programSources})
		case "scoped-template-consumers":
			wrapper = asMap(release.GateExecution{SchemaVersion: 1, Kind: "stable-release-gate-execution", GateID: id, QualificationScope: release.LocalQualificationScope, Identity: f.input.Identity, Platform: "darwin/arm64", BinarySHA256: f.input.Artifacts[0].Binary.SHA256, Status: "passed", ExitCode: pointer(0), Signal: json.RawMessage("null"), Error: json.RawMessage("null"), InputDrift: pointer(false), Unexecuted: []string{}, Coverage: release.RequiredGateCoverage(id), SourceReports: scopedSources})
		default:
			wrapper = f.gateEvidence(t, id)
		}
		gateChecks = append(gateChecks, release.Check{ID: id, Status: "passed", ExitCode: pointer(0), InputDrift: pointer(false), Unexecuted: []string{}, Report: f.json(t, "gates/"+id+".json", wrapper), Stdout: f.file(t, "gates/"+id+".stdout", nil), Stderr: f.file(t, "gates/"+id+".stderr", nil)})
	}
	f.input.ReleaseGate = f.json(t, "gate.json", release.Receipt{SchemaVersion: 1, QualificationScope: release.LocalQualificationScope, Identity: f.input.Identity, Status: "passed", ExitCode: pointer(0), InputDrift: pointer(false), Unexecuted: []string{}, Checks: gateChecks})
	return f
}

func TestLocalScopeCandidateAndStableShareExactPayload(t *testing.T) {
	f := localFixture(t)
	out := filepath.Join(f.dir, "qualified")
	if err := release.Assemble(f.manifest(t), out, f.expected); err != nil {
		t.Fatal(err)
	}
	old := readArchive(t, filepath.Join(f.dir, "candidate/yss_1.0.0_darwin_arm64.tar.gz"))
	current := readArchive(t, filepath.Join(out, "yss_1.0.0_darwin_arm64.tar.gz"))
	for ref, body := range old {
		if ref != "release-manifest.json" && !bytes.Equal(body, current[ref]) {
			t.Fatalf("payload changed %s", ref)
		}
	}
	var a, b map[string]any
	_ = json.Unmarshal(old["release-manifest.json"], &a)
	_ = json.Unmarshal(current["release-manifest.json"], &b)
	if a["stableReady"] != false || b["stableReady"] != true || !reflect.DeepEqual(a["bundles"], b["bundles"]) {
		t.Fatal("candidate qualification or complete inspections changed")
	}
	for _, key := range []string{"sourceLockSha256", "requiredPlatforms", "supportedPlatforms", "nativeReceiptSha256", "releaseGateSha256"} {
		if _, ok := a[key]; ok {
			t.Fatalf("candidate invented proof %s", key)
		}
		if _, ok := b[key]; !ok {
			t.Fatalf("stable proof missing %s", key)
		}
	}
	entries, err := os.ReadDir(out)
	if err != nil || len(entries) != 2 {
		t.Fatal("local scope silently required other platforms", err)
	}
}

func editSource(t *testing.T, f *fixture, check int, kind string, change func(map[string]any)) {
	t.Helper()
	editGate(t, f, func(receipt *release.Receipt) {
		editRaw(t, f, &receipt.Checks[check].Report, func(wrapper map[string]any) {
			for _, v := range wrapper["sourceReports"].([]any) {
				source := v.(map[string]any)
				if source["kind"] == kind {
					ref := asMap(source["report"])
					descriptor := release.FileRef{Path: ref["path"].(string), SHA256: ref["sha256"].(string)}
					editRaw(t, f, &descriptor, change)
					source["report"] = asMap(descriptor)
					return
				}
			}
			t.Fatalf("missing source %s", kind)
		})
	})
}

func TestLocalQualificationRejectsIncompleteOrContradictoryEvidence(t *testing.T) {
	tests := []struct {
		name, code string
		change     func(*testing.T, *fixture)
	}{
		{"input-self-trims-platforms", "PLATFORMS", func(t *testing.T, f *fixture) { f.expected.RequiredPlatforms = platforms }},
		{"input-self-trims-scope", "EVIDENCE", func(t *testing.T, f *fixture) { f.input.QualificationScope = release.FullQualificationScope }},
		{"missing-version", "EVIDENCE", func(t *testing.T, f *fixture) {
			editReceipt(t, f, 0, func(r *release.Receipt) { r.VersionCommand = nil })
		}},
		{"plugin-duplicate-label", "EVIDENCE", func(t *testing.T, f *fixture) {
			editReceipt(t, f, 0, func(r *release.Receipt) {
				editRaw(t, f, &r.Checks[2].Report, func(o map[string]any) {
					rows := o["commands"].([]any)
					rows[1].(map[string]any)["label"] = rows[0].(map[string]any)["label"]
				})
			})
		}},
		{"plugin-false-rollback", "EVIDENCE", func(t *testing.T, f *fixture) {
			editReceipt(t, f, 0, func(r *release.Receipt) {
				editRaw(t, f, &r.Checks[2].Report, func(o map[string]any) { o["cases"].([]any)[0].(map[string]any)["whole_atomic_rollback"] = false })
			})
		}},
		{"plugin-wrong-source", "PROVENANCE", func(t *testing.T, f *fixture) {
			editReceipt(t, f, 0, func(r *release.Receipt) {
				editRaw(t, f, &r.Checks[2].Report, func(o map[string]any) { o["go_head"] = hash([]byte("wrong"))[:40] })
			})
		}},
		{"gate-signal", "EVIDENCE", func(t *testing.T, f *fixture) {
			editGate(t, f, func(r *release.Receipt) {
				editRaw(t, f, &r.Checks[0].Report, func(o map[string]any) { o["signal"] = "SIGTERM" })
			})
		}},
		{"gate-error", "EVIDENCE", func(t *testing.T, f *fixture) {
			editGate(t, f, func(r *release.Receipt) {
				editRaw(t, f, &r.Checks[0].Report, func(o map[string]any) { o["error"] = "failed" })
			})
		}},
		{"program-incomplete", "EVIDENCE", func(t *testing.T, f *fixture) {
			editSource(t, f, 0, "native-program-installation", func(o map[string]any) { o["cases"] = o["cases"].([]any)[:4] })
		}},
		{"program-payload", "EVIDENCE", func(t *testing.T, f *fixture) {
			editSource(t, f, 0, "native-program-installation", func(o map[string]any) { o["package_payload_sha256"] = hash([]byte("wrong")) })
		}},
		{"legacy-typed-refusal", "EVIDENCE", func(t *testing.T, f *fixture) {
			editSource(t, f, 1, "native-legacy-recovery-matrix", func(o map[string]any) {
				for _, v := range o["cases"].([]any) {
					r := v.(map[string]any)
					if r["case"] == "modified-after-apply-rollback-refusal" {
						r["expected_code"] = "INPUT_DRIFT"
						return
					}
				}
			})
		}},
		{"scoped-missing-command", "EVIDENCE", func(t *testing.T, f *fixture) {
			editSource(t, f, 2, "scoped-template-consumer-verification", func(o map[string]any) { o["checks"] = o["checks"].([]any)[:8] })
		}},
		{"scoped-wrong-source", "PROVENANCE", func(t *testing.T, f *fixture) {
			editSource(t, f, 2, "scoped-template-consumer-verification", func(o map[string]any) { o["current_sources"].(map[string]any)["design"] = hash([]byte("wrong"))[:40] })
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := localFixture(t)
			tc.change(t, f)
			manifest := f.manifest(t)
			before := inventory(t, f.dir)
			err := release.Assemble(manifest, filepath.Join(f.dir, "rejected"), f.expected)
			if release.Code(err) != tc.code {
				t.Fatalf("want %s got %v", tc.code, err)
			}
			if !reflect.DeepEqual(before, inventory(t, f.dir)) {
				t.Fatal("rejection changed inventory")
			}
		})
	}
}
