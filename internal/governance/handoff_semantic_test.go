package governance

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandoffLegacyPersistedOldOracle(t *testing.T) {
	fixture := "/Users/zhudaoming/.yss-harness/runtime/5391a3c4185fae417c28ef908ade1508f7dbb75bdbec2c8283412e6bb15ef967/maintenance/research/2026-10-05-unified-go-cli/extension-r7/review/domain-r2/legacy-handoff-package"
	if _, e := os.Stat(fixture); e != nil {
		t.Skip("persisted independent old oracle unavailable")
	}
	root := contractTestRetainedRoot(t, apTestRoot(t), "handoff-v3")
	contractTestRules(t, root)
	if e := filepath.WalkDir(fixture, func(file string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		b, e := os.ReadFile(file)
		if e != nil {
			return e
		}
		ref, e := filepath.Rel(fixture, file)
		if e != nil {
			return e
		}
		apTestPut(t, root, "package/"+filepath.ToSlash(ref), b)
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	s := apTestSession(t, root)
	if _, e := contractOpenHandoff(s, "package"); e != nil {
		t.Fatal(e)
	}
	if e := s.finish(); e != nil {
		t.Fatal(e)
	}
	archive := contractTestZIP(t, fixture)
	apTestPut(t, root, "package.ZIP", archive)
	before := verificationTree(t, root)
	s = apTestSession(t, root)
	bundle, e := contractOpenHandoff(s, "package.ZIP")
	if e != nil {
		t.Fatal(e)
	}
	if contractN(bundle.Handoff["schema_version"]) != 3 {
		t.Fatal("archive changed source protocol")
	}
	if e = s.finish(); e != nil {
		t.Fatal(e)
	}
	if !contractSame(before, verificationTree(t, root)) {
		t.Fatal("ZIP validation extracted or modified evidence")
	}
	for _, ref := range []string{"package", "package.ZIP"} {
		if _, e = RunContext(context.Background(), "handoff", "verify", root, map[string]string{"kind": "package", "package": ref, "profile": "spec"}); e != nil {
			t.Fatalf("public Handoff v3 %s: %v", ref, e)
		}
	}
	contractTestRetainFixture(t, root, "handoff-v3-directory", map[string]any{"group": "handoff", "action": "verify", "kind": "package", "file": "package", "profile": "spec", "expected_exit": 0})
	contractTestRetainFixture(t, root, "handoff-v3-zip", map[string]any{"group": "handoff", "action": "verify", "kind": "package", "file": "package.ZIP", "expected_exit": 0})
	s = apTestSession(t, root)
	if _, e = contractOpenHandoff(s, "package.ZIP"); e != nil {
		t.Fatal(e)
	}
	apTestPut(t, root, "package.ZIP", append(archive, '!'))
	if e = s.finish(); e == nil {
		t.Fatal("ZIP drift passed final verification")
	}
	apTestPut(t, root, "package.ZIP", archive)
}

func contractTestZIP(t *testing.T, root string) []byte {
	t.Helper()
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	if e := filepath.WalkDir(root, func(file string, d fs.DirEntry, e error) error {
		if e != nil || d.IsDir() {
			return e
		}
		ref, e := filepath.Rel(root, file)
		if e != nil {
			return e
		}
		b, e := os.ReadFile(file)
		if e != nil {
			return e
		}
		header := &zip.FileHeader{Name: filepath.ToSlash(ref), Method: zip.Deflate}
		header.SetMode(0644)
		f, e := w.CreateHeader(header)
		if e != nil {
			return e
		}
		_, e = f.Write(b)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	if e := w.Close(); e != nil {
		t.Fatal(e)
	}
	return out.Bytes()
}

func contractTestRules(t *testing.T, root string) {
	t.Helper()
	if apFixtureBundle == nil {
		_ = apTestRoot(t)
	}
	for ref, f := range apFixtureBundle.Files {
		if len(ref) > 15 && ref[:15] == ".agents/skills/" {
			raw, e := f.Render(map[string]string{"projectName": "synthetic", "businessDomain": "test-only", "teamSize": "2"})
			if e != nil {
				t.Fatal(e)
			}
			apTestPut(t, root, ref, raw)
		}
	}
}

func TestHandoffClosureDirectoryAndUnsafePaths(t *testing.T) {
	root := apTestRoot(t)
	apTestPut(t, root, "handoff.yaml", map[string]any{"schema_version": 3})
	apTestPut(t, root, "preview/index.html", "<main>portable</main>")
	s := apTestSession(t, root)
	b := &nativeHandoff{Handoff: map[string]any{"source": map[string]any{}}, Config: map[string]any{"additional_files": []any{"preview"}}}
	refs, e := contractHandoffClosure(s, "handoff.yaml", b)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, ref := range refs {
		found = found || ref == "preview/index.html"
	}
	if !found {
		t.Fatal("directory files missing from closure")
	}
	if _, e = contractLocalDependency("preview/index.html", "?"); e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{"../../outside", "%2e%2e/%2e%2e/outside", "file:///tmp/x", "/tmp/x"} {
		if _, e = contractLocalDependency("preview/index.html", path); e == nil {
			t.Fatalf("unsafe ref %s passed", path)
		}
	}
	if e = s.finish(); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = contractHandoffClosure(newSemanticSession(ctx, root, nil), "handoff.yaml", b); e == nil {
		t.Fatal("cancelled closure passed")
	}
}

func TestHandoffSourceV5OldOracleDifferential(t *testing.T) {
	if _, e := exec.LookPath("node"); e != nil {
		t.Skip("development-only old oracle unavailable")
	}
	old := governanceOracleRoot(t)
	if _, e := os.Stat(filepath.Join(old, "scripts/fixtures/strategic-handoff/fixture.mjs")); e != nil {
		t.Skip("fixed old oracle unavailable")
	}
	root := contractTestRetainedRoot(t, apTestRoot(t), "handoff-v5")
	contractTestRules(t, root)
	source := filepath.Join(contractTestOracleTMP(t), "handoff-v5-source")
	if e := os.MkdirAll(source, 0755); e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command("node", "--input-type=module", "-e", "import {fixture} from './scripts/fixtures/strategic-handoff/fixture.mjs';import {exportBundle,importBundle} from './scripts/lib/strategic-handoff.mjs';const root=process.argv[1];await fixture(root,{handoffVersion:5,businessTickets:true});const exported=await exportBundle({sourceRoot:root,handoffRef:'handoff.yaml',output:process.argv[2],zip:true});await importBundle({bundle:exported.zip,targetRoot:process.argv[3]});", source, filepath.Join(root, "package"), root)
	cmd.Dir = old
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("old oracle export: %v %s", e, out)
	}
	s := apTestSession(t, root)
	if _, e = contractOpenHandoff(s, "package"); e != nil {
		t.Fatal(e)
	}
	if e = s.finish(); e != nil {
		t.Fatal(e)
	}
	before := verificationTree(t, root)
	s = apTestSession(t, root)
	bundle, e := contractOpenHandoff(s, "package.zip")
	if e != nil {
		t.Fatal(e)
	}
	if contractN(bundle.Handoff["schema_version"]) != 5 || bundle.Business == nil {
		t.Fatal("ZIP lost v5 business source closure")
	}
	refs, e := s.scan("docs/handoffs")
	if e != nil {
		t.Fatal(e)
	}
	seen := false
	for _, ref := range refs {
		if !strings.HasSuffix(ref, "/import-receipt.json") {
			continue
		}
		receipt, e := s.doc(ref)
		if e != nil {
			t.Fatal(e)
		}
		if e = contractReceipt(s, receipt); e != nil {
			t.Fatal(e)
		}
		if receipt["bundle_digest"] != bundle.Manifest["bundle_digest"] || receipt["status"] != "pending-context-reconciliation" {
			t.Fatal("published receipt changed pending state or bundle identity")
		}
		if _, e = contractOpenHandoff(s, text(receipt["package_ref"])); e != nil {
			t.Fatal(e)
		}
		seen = true
	}
	if !seen {
		t.Fatal("old ZIP import did not persist receipt")
	}
	if e = s.finish(); e != nil {
		t.Fatal(e)
	}
	if !contractSame(before, verificationTree(t, root)) {
		t.Fatal("receipt/ZIP query changed assets")
	}
	for _, ref := range []string{"package", "package.zip"} {
		if _, e = RunContext(context.Background(), "handoff", "verify", root, map[string]string{"kind": "package", "package": ref, "profile": "spec"}); e != nil {
			t.Fatalf("public Handoff v5 %s: %v", ref, e)
		}
	}
	contractTestRetainFixture(t, root, "handoff-v5-directory", map[string]any{"group": "handoff", "action": "verify", "kind": "package", "file": "package", "profile": "spec", "expected_exit": 0})
	contractTestRetainFixture(t, root, "handoff-v5-zip", map[string]any{"group": "handoff", "action": "verify", "kind": "package", "file": "package.zip", "expected_exit": 0})
	contractTestTacticalConsumption(t, root, old)
	for _, ref := range []string{trackerRef, ".template-spec/process/checkpoint-boundary.yaml", ".agents/skills/yss-product-lifecycle/references/orchestration-contract.yaml"} {
		raw, err := os.ReadFile(filepath.Join(old, ref))
		if err != nil {
			t.Fatal(err)
		}
		apTestPut(t, root, ref, raw)
	}
	const cpRef = "docs/.scratch/feature-demo/checkpoint.json"
	cp := map[string]any{"schema_version": 1, "repository_mode": "project-instance", "feature_id": "feature-demo", "mode": "audit", "status": "running", "stage": "stage.entry-triage", "artifacts": map[string]any{}, "gates": map[string]any{}, "context_reconciliation": map[string]any{"status": "pending", "ref": nil, "evidence_refs": []any{}}, "next_work_unit": nil, "ticket_sync": map[string]any{}, "verification": map[string]any{"commands": []any{}, "evidence_refs": []any{"tactical-consumption-valid.json"}}, "human_review": map[string]any{}, "git_checkpoint": map[string]any{}, "blockers": []any{}, "rollback": []any{}}
	apTestPut(t, root, cpRef, cp)
	if report, err := RunContext(context.Background(), "project-ci", "verify", root, nil); err != nil {
		t.Fatalf("full CI closed Handoff package/consumption: %v diagnostics=%+v", err, report.(*SemanticReport).Diagnostics)
	}
	contractTestRetainFixture(t, root, "complete-ci-handoff-consumption", map[string]any{"group": "project-ci", "action": "verify", "isolate_root": true, "expected_exit": 0})
	cp["verification"] = map[string]any{"commands": []any{}, "evidence_refs": []any{"tactical-consumption-stale.json"}}
	apTestPut(t, root, cpRef, cp)
	_, err := RunContext(context.Background(), "project-ci", "verify", root, nil)
	apTestCode(t, err, "PROJECT_CI_REJECTED")
	contractTestRetainFixture(t, root, "complete-ci-handoff-consumption-refusal", map[string]any{"group": "project-ci", "action": "verify", "isolate_root": true, "expected_exit": 1})
	apTestPut(t, root, "package/unregistered.txt", "extra bytes")
	if _, e = contractOpenHandoff(apTestSession(t, root), "package"); e == nil {
		t.Fatal("unregistered evidence passed")
	}
	if e = os.Remove(filepath.Join(root, "package/unregistered.txt")); e != nil {
		t.Fatal(e)
	}
}

func TestHandoffSourcePolicyExplicitPublishedVersions(t *testing.T) {
	for _, name := range []string{"published-versionless-v3", "published-versionless-v4", "current-v5", "missing-policy", "empty-policy", "missing-source-kinds", "unknown-source-kind", "unknown-schema", "unknown-versionless-default", "unknown-versionless-v5"} {
		t.Run(name, func(t *testing.T) {
			root := apTestRoot(t)
			s := apTestSession(t, root)
			policy := map[string]any{"schema_version": json.Number("1"), "gates": []any{}, "work_units": map[string]any{}, "source_kinds": []any{"platform-message", "session-export", "user-confirmation-file"}}
			s.roles = map[string]any{"schema_version": json.Number("1"), "status": "active", "gate_policy": map[string]any{"default_if_unlisted": "biological-human", "biological_human": []any{}, "digital_human_review": []any{}}, "user_decision_policy": policy}
			version := 5
			switch name {
			case "published-versionless-v3":
				delete(policy, "schema_version")
				version = 3
			case "published-versionless-v4":
				delete(policy, "schema_version")
				version = 4
			case "missing-policy":
				delete(s.roles, "user_decision_policy")
			case "empty-policy":
				s.roles["user_decision_policy"] = map[string]any{}
			case "missing-source-kinds":
				delete(policy, "source_kinds")
			case "unknown-source-kind":
				policy["source_kinds"] = []any{"agent-generated"}
			case "unknown-schema":
				policy["schema_version"] = json.Number("99")
			case "unknown-versionless-default":
				delete(policy, "schema_version")
				version = 3
				semMap(s.roles["gate_policy"])["default_if_unlisted"] = "digital-agent"
			case "unknown-versionless-v5":
				delete(policy, "schema_version")
			}
			e := contractHandoffSourcePolicy(s, version)
			want := strings.HasPrefix(name, "published-") || name == "current-v5"
			if (e == nil) != want {
				t.Fatalf("source policy %s: %v", name, e)
			}
		})
	}
}

func TestHandoffConsumptionRejectsUnknownContractVersionsAndArchitecture(t *testing.T) {
	root := apTestRoot(t)
	for _, consumer := range []string{"tactical", "frontend"} {
		for _, version := range []any{json.Number("99"), json.Number("2.0000000000000000001"), "2", nil} {
			apTestPut(t, root, "unknown-consumer.json", map[string]any{"schema_version": version})
			_, err := RunContext(context.Background(), "handoff", "verify", root, map[string]string{"kind": "consumption", "file": "unknown-consumer.json", "consumer": consumer})
			apTestCode(t, err, "CAPABILITY")
		}
	}
	s := apTestSession(t, root)
	for _, family := range []any{"unknown", nil} {
		err := contractHandoffConsumption(s, map[string]any{"schema_version": json.Number("2"), "architecture": map[string]any{"family": family}}, map[string]string{"consumer": "tactical"})
		apTestCode(t, err, "HANDOFF_CONSUMER")
	}
}

// Complete the published imported traceability draft in a synthetic consumer.
// Its approved source package remains byte-for-byte unchanged; no real gate
// or implementation permission is created by this test record.
func contractTestTacticalConsumption(t *testing.T, root, old string) {
	t.Helper()
	base := "docs/handoffs/strategic-design-handoff.supplier/v1"
	contextRaw, err := os.ReadFile(filepath.Join(root, "package/payload/files/source-context.snapshot.md"))
	if err != nil {
		t.Fatal(err)
	}
	apTestPut(t, root, "CONTEXT.md", contextRaw)
	s := apTestSession(t, root)
	bundle, err := contractOpenHandoff(s, "package")
	if err != nil {
		t.Fatal(err)
	}
	draft, err := s.doc(base + "/backend-technical-traceability-draft.json")
	if err != nil {
		t.Fatal(err)
	}
	contextRef := "tactical-context-reconciliation.json"
	apTestPut(t, root, "tactical-evidence.log", "Synthetic mapping evidence; no application command executed.\n")
	apTestPut(t, root, contextRef, map[string]any{"schema_version": 1, "repository_mode": "project-instance", "stage": "stage.system-data-engineering", "work_unit": "work-unit.technical-analysis", "status": "reconciled", "context_snapshot": bundle.Handoff["source_context_snapshot"], "changes": map[string]any{"added": []any{}, "updated": []any{}, "deprecated": []any{}}, "unresolved_terms": []any{}, "evidence_refs": []any{"tactical-evidence.log"}})
	draft["context_reconciliation_ref"] = contextRef
	for _, v := range semList(draft["rows"]) {
		row := semMap(v)
		row["disposition"] = "implemented"
		row["dependency_status"] = "known"
		row["dependent_slice_refs"] = []any{"slice.synthetic"}
		row["tactical_refs"] = []any{"module.synthetic"}
		row["test_seam_refs"] = []any{"test-seam.success", "test-seam.failure"}
		row["evidence_refs"] = []any{"tactical-evidence.log"}
		row["scenario_tests"] = []any{map[string]any{"outcome": "success", "seam_ref": "test-seam.success"}, map[string]any{"outcome": "failure", "seam_ref": "test-seam.failure"}}
	}
	valid := map[string]any{"schema_version": 2, "status": "approved", "architecture": map[string]any{"family": "layered-mvc"}, "strategic_handoff": draft, "design": map[string]any{"module_catalog": []any{map[string]any{"module_id": "module.synthetic"}}, "test_seams": []any{map[string]any{"seam_id": "test-seam.success", "subject_ref": "module.synthetic"}, map[string]any{"seam_id": "test-seam.failure", "subject_ref": "module.synthetic"}}}}
	for _, state := range []string{"valid", "stale", "wrong-route", "missing-success", "blocked-row", "wrong-architecture", "unknown-version"} {
		t.Run("public-tactical-consumption-"+state, func(t *testing.T) {
			candidate := contractCopy(valid)
			binding := semMap(candidate["strategic_handoff"])
			switch state {
			case "stale":
				binding["bundle_digest"] = "sha256:" + strings.Repeat("0", 64)
			case "wrong-route":
				binding["route_id"] = "route.frontend"
			case "missing-success":
				for _, v := range semList(binding["rows"]) {
					semMap(v)["scenario_tests"] = []any{}
				}
			case "blocked-row":
				semMap(semList(binding["rows"])[0])["disposition"] = "pending"
			case "wrong-architecture":
				semMap(candidate["architecture"])["family"] = "unknown"
			case "unknown-version":
				candidate["schema_version"] = 99
			}
			ref := "tactical-consumption-" + state + ".json"
			apTestPut(t, root, ref, candidate)
			before := verificationTree(t, root)
			_, err := RunContext(context.Background(), "handoff", "verify", root, map[string]string{"kind": "consumption", "file": ref, "consumer": "tactical"})
			if (err == nil) != (state == "valid") {
				t.Fatalf("%s: %v", state, err)
			}
			if !contractSame(before, verificationTree(t, root)) {
				t.Fatal("consumption mutated project")
			}
			expected := 1
			if state == "valid" {
				expected = 0
			}
			if state == "unknown-version" {
				expected = 2
			}
			contractTestRetainFixture(t, root, "tactical-consumption-"+state, map[string]any{"group": "handoff", "action": "verify", "kind": "consumption", "file": ref, "consumer": "tactical", "profile": "spec", "expected_exit": expected})
			// The published consumer already checks architecture. Version 99 is a
			// documented tightening of its formerly permissive generic adapter.
			if state != "unknown-version" {
				cmd := exec.Command("node", "--input-type=module", "-e", `import fs from 'node:fs';import {verifyConsumption} from './scripts/lib/strategic-handoff-consumption.mjs';try{await verifyConsumption(JSON.parse(fs.readFileSync(process.argv[2],'utf8')),{root:process.argv[1],consumer:'tactical',readOnly:true});console.log('accepted')}catch(error){console.log('refused: '+error.message);process.exitCode=1}`, root, filepath.Join(root, ref))
				cmd.Dir = old
				out, err := cmd.CombinedOutput()
				if (err == nil) != (state == "valid") {
					t.Fatalf("old %s: %v %s", state, err, out)
				}
				t.Logf("fixed-old tactical %s: %s", state, out)
				if !contractSame(before, verificationTree(t, root)) {
					t.Fatal("read-only oracle mutated project")
				}
			}
		})
	}
}

func TestHandoffReceiptConsumerBindsActualProfileAndCapability(t *testing.T) {
	root := apTestProfileRoot(t, "frontend")
	s := apTestSession(t, root)
	for _, state := range []string{"valid", "wrong-profile", "unselected-capability"} {
		r := map[string]any{"schema_version": json.Number("2"), "target_profile_id": "harness.frontend-delivery", "selected_consumer_capabilities": []any{"frontend-engineering-design"}}
		if state == "wrong-profile" {
			r["target_profile_id"] = "harness.backend-delivery"
		}
		if state == "unselected-capability" {
			r["selected_consumer_capabilities"] = []any{"backend-technical-design"}
		}
		err := contractReceiptConsumer(s, r, "frontend-engineering-design")
		if (err == nil) != (state == "valid") {
			t.Fatalf("%s: %v", state, err)
		}
	}
}
