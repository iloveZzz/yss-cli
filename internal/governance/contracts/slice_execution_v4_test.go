package contracts

import (
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"testing"
)

func TestSlice4ResultUsesCompletedTaskAndActualCoverage(t *testing.T) {
	for _, name := range []string{"passed", "wrong-unit", "missing-verification", "wrong-cwd", "open-impact", "wrong-candidate", "wrong-slice", "failed-status"} {
		t.Run(name, func(t *testing.T) {
			s, task := completedDraft(t)
			candidate, err := draftExecutionCandidate(s, task)
			if err != nil {
				t.Fatal(err)
			}
			kernel.ApMap(task["result"])["changed_files"] = []string{"drafts/check.json"}
			task["work_unit_id"] = "impl"
			kernel.ApMap(task["result"])["work_unit"] = "impl"
			c := &NativeSlice{Ref: "input.json", Raw: map[string]any{"schema_version": json.Number("4"), "contract_id": "slice.c", "contract_version": "v1"}, Normalized: map[string]any{"resolution": map[string]any{"registry_digest": "registry", "compiler_contract_digest": "compiler"}, "work_units": []any{map[string]any{"id": "impl", "project_root": s.Root, "allowed_write_paths": []string{"drafts"}, "work_unit": map[string]any{"verification": []any{map[string]any{"id": "check", "command": "check drafts/spec.md", "cwd": s.Root, "acceptance_refs": []string{"AC-1"}, "expected_evidence": []string{"drafts/check.json"}}}, "expected_evidence": []string{"drafts/check.json"}}}}}}
			result := map[string]any{"schema_version": json.Number("2"), "status": "implemented", "evidence_binding_version": json.Number("1"), "work_unit_id": "impl", "consumed_contract": map[string]any{"contract_id": "slice.c", "contract_version": "v1", "contract_digest": "sha256:" + kernel.Text(kernel.ApRows(task["input_bindings"])[0]["digest"]), "registry_digest": "registry", "compiler_contract_digest": "compiler"}, "changed_files": kernel.ApMap(task["result"])["changed_files"], "evidence_files": []any{}, "verification_results": []any{}, "constraint_results": []any{}, "seam_deferred": []any{}, "deviations": []any{}, "new_impacts": []any{}, "not_applicable_reason": "", "source_bindings": []any{}}
			binding, err := s.Bind("drafts/check.json")
			if err != nil {
				t.Fatal(err)
			}
			result["evidence_files"] = []any{map[string]any{"path": "drafts/check.json", "digest": binding.Digest, "behavior_ref": "AC-1"}}
			row := ContractCopy(kernel.ApRows(task["verification_results"])[0])
			row["verification_id"] = "check"
			row["cwd"] = s.Root
			row["acceptance_refs"] = []string{"AC-1"}
			row["evidence_refs"] = []string{"drafts/check.json"}
			result["verification_results"] = []any{row}
			result["source_bindings"] = []any{map[string]any{"path": "drafts/check.json", "project_root": s.Root, "digest": binding.Digest}}
			switch name {
			case "wrong-unit":
				result["work_unit_id"] = "other"
			case "missing-verification":
				result["verification_results"] = []any{}
			case "wrong-cwd":
				row["cwd"] = "/other"
			case "open-impact":
				result["new_impacts"] = []string{"api"}
			case "wrong-slice":
				result["slice_id"] = "other"
			case "failed-status":
				row["status"] = "failed"
			case "wrong-candidate":
				row["candidate_digest"] = "stale"
			}
			taskFile(t, s.Root, "result.json", result)
			result, err = s.Doc("result.json")
			if err != nil {
				t.Fatal(err)
			}
			err = validateSlice4ExecutionResult(s, result, c, task, candidate)
			if (err == nil) != (name == "passed") {
				t.Fatalf("%s: %v", name, err)
			}
		})
	}
}
