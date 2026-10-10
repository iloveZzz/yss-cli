package governance

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaintenanceIntensityTwoLevels(t *testing.T) {
	root := apTestRoot(t)
	apTestPut(t, root, "yss-project.yaml", map[string]any{"schema_version": 1, "repository_mode": "template-source"})
	policy := map[string]any{"schema_version": 2, "default_level": "L2", "levels": map[string]any{
		"L1": map[string]any{"triggers": []any{"textual-only", "link-only", "deterministic-projection"}},
		"L2": map[string]any{"triggers": []any{"local-rule", "template-structure", "non-core-validator", "lifecycle-gate", "permission-boundary", "generation-semantics", "release-semantics", "cross-repo-contract", "core-validator"}},
	}, "counterexample_triggers": []any{"permission-boundary", "lifecycle-gate", "release-semantics"}}
	apTestPut(t, root, ".template-source/process/maintenance-intensity.yaml", policy)
	checkpoint := map[string]any{"schema_version": 2, "intensity": "L2", "classification_reason": "两级维护", "triggers": []any{"core-validator"}, "changed_assets": []any{"scripts/verify-maintenance-checkpoint"}, "review_mode": "self-check", "escalation": "none", "target_state": "implementation-ready", "current_state": "implementation-ready", "verification_profile": "fast", "review_round": 0, "candidate_digest": nil, "verification_evidence": []any{
		map[string]any{"kind": "self-check", "command": "test-only", "result": "pass"}, map[string]any{"kind": "fresh-verification", "command": "test-only", "result": "pass"},
	}}
	legacyPolicy := apTestJSONClone(t, policy)
	legacyPolicy["schema_version"] = 1
	legacyLevels := semMap(legacyPolicy["levels"])
	legacyLevels["L2"] = map[string]any{"triggers": []any{"local-rule", "template-structure", "non-core-validator"}}
	legacyLevels["L3"] = map[string]any{"triggers": []any{"lifecycle-gate", "ticket-state", "permission-boundary", "generation-semantics", "release-semantics", "cross-repo-contract", "core-validator", "historical-important-escape", "aggregate-behavior-change", "release-candidate"}}
	apTestPut(t, root, ".template-source/process/maintenance-intensity-v1.yaml", legacyPolicy)
	verify := func(t *testing.T, doc map[string]any, history, pass bool, diagnostic string) {
		t.Helper()
		apTestPut(t, root, "maintenance.json", doc)
		opts := map[string]string{}
		if history {
			opts["history"] = "true"
		}
		err := newSemanticSession(context.Background(), root, nil).verify("maintenance-checkpoint", "maintenance.json", opts)
		if (err == nil) != pass || err != nil && !strings.Contains(err.Error(), diagnostic) {
			t.Fatalf("pass=%v diagnostic=%q err=%v", pass, diagnostic, err)
		}
		if source := os.Getenv("YSS_LEGACY_ORACLE_ROOT"); source != "" {
			args := []string{filepath.Join(source, "scripts/verify-maintenance-checkpoint")}
			if history {
				args = append(args, "--history")
			}
			cmd := exec.Command("node", args...)
			input, _ := json.Marshal(doc)
			cmd.Stdin = strings.NewReader(string(input))
			out, oracleErr := cmd.CombinedOutput()
			if (oracleErr == nil) != pass {
				t.Fatalf("Node/Go verdict differs: %s", out)
			}
		}
	}
	verify(t, checkpoint, false, true, "")
	for _, trigger := range []string{"ticket-state", "historical-important-escape", "aggregate-behavior-change", "release-candidate"} {
		t.Run("retired-"+trigger, func(t *testing.T) {
			doc := apTestJSONClone(t, checkpoint)
			doc["triggers"] = []any{trigger}
			verify(t, doc, false, false, "未知维护 trigger: "+trigger)
			verify(t, doc, true, false, "低于")
			doc["intensity"] = "L3"
			verify(t, doc, true, true, "")
		})
	}
	for _, trigger := range semStrings(semMap(semMap(policy["levels"])["L2"])["triggers"]) {
		t.Run(trigger, func(t *testing.T) {
			doc := apTestJSONClone(t, checkpoint)
			doc["triggers"] = []any{trigger}
			risk := semHas(policy["counterexample_triggers"], trigger)
			verify(t, doc, false, !risk, "定向反例")
			if risk {
				doc["verification_evidence"] = append(semList(doc["verification_evidence"]), map[string]any{"kind": "counterexample", "trigger": trigger, "command": "text-only", "result": "pass"})
				verify(t, doc, false, false, "定向反例")
			}
		})
	}
	for _, tc := range []struct {
		name, intensity string
		triggers        []any
		history, pass   bool
		diagnostic      string
	}{
		{"default", "L2", []any{}, false, true, ""},
		{"unknown", "L2", []any{"unknown"}, false, false, "未知维护 trigger"},
		{"current-l3", "L3", []any{"core-validator"}, false, false, "L3"},
		{"history-l3", "L3", []any{"core-validator"}, true, true, ""},
		{"history-minimum", "L2", []any{"core-validator"}, true, false, "低于"},
		{"history-l2-evidence", "L2", []any{"local-rule"}, true, false, "counterexample"},
		{"current-l1-minimum", "L1", []any{"core-validator"}, false, false, "低于"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := apTestJSONClone(t, checkpoint)
			doc["intensity"], doc["triggers"] = tc.intensity, tc.triggers
			verify(t, doc, tc.history, tc.pass, tc.diagnostic)
		})
	}
	for _, missing := range []string{"self-check", "fresh-verification"} {
		doc := apTestJSONClone(t, checkpoint)
		kept := []any{}
		for _, row := range semList(doc["verification_evidence"]) {
			if semMap(row)["kind"] != missing {
				kept = append(kept, row)
			}
		}
		doc["verification_evidence"] = kept
		verify(t, doc, false, false, missing)
	}
	formal := apTestJSONClone(t, checkpoint)
	formal["review_mode"] = "formal-independent"
	formal["verification_evidence"] = semList(formal["verification_evidence"])[1:]
	verify(t, formal, false, true, "") // Implementation-ready permits a pending independent review.
	voluntary := apTestJSONClone(t, checkpoint)
	voluntary["verification_evidence"] = append(semList(voluntary["verification_evidence"]), map[string]any{"kind": "counterexample", "trigger": "core-validator", "command": "text-only", "result": "pass", "run_ref": nil})
	verify(t, voluntary, false, false, "")
}
