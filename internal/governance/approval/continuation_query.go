package approval

import (
	"encoding/json"
	"sort"

	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
)

func init() {
	kernel.RegisterRoleIndependentValidator("continuation-comparison", func(s *kernel.SemanticSession, ref string, _ map[string]string) error {
		input, err := s.Doc(ref)
		if err != nil {
			return err
		}
		before, after := kernel.ApMap(input["before"]), kernel.ApMap(input["after"])
		a, err := s.EvidenceAsset(before)
		if err != nil {
			return err
		}
		b, err := s.EvidenceAsset(after)
		if err != nil {
			return err
		}
		av, ak, err := comparisonModel(kernel.Text(after["ref"]), a)
		if err != nil {
			return s.Reject("CONTINUATION_COMPARISON", err.Error())
		}
		bv, bk, err := comparisonModel(kernel.Text(after["ref"]), b)
		if err != nil || ak != bk {
			return s.Reject("CONTINUATION_COMPARISON", "比较格式不支持或不同")
		}
		changes := actualDifferences(av, bv)
		paths := []string{}
		for path := range changes {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		rows := []any{}
		for _, path := range paths {
			rows = append(rows, changes[path])
		}
		difference := map[string]any{"schema_version": 1, "changes": rows}
		exact, err := json.Marshal(difference)
		if err != nil {
			return s.Reject("CONTINUATION_COMPARISON", "实际差异不能精确序列化: "+err.Error())
		}
		kernel.SemanticCoverage(s, "continuation_comparison", map[string]any{"before": before, "after": after, "difference": difference, "difference_json": string(exact), "read_only": true, "execution_allowed": false, "approval_created": false})
		return nil
	})
	for _, register := range []func(string, kernel.SemanticValidator){kernel.RegisterSemanticValidator, kernel.RegisterRoleIndependentValidator} {
		register("work-unit-user-decision", func(s *kernel.SemanticSession, ref string, _ map[string]string) error {
			input, err := s.Doc(ref)
			if err != nil {
				return err
			}
			if kernel.ApFind(s.Registry["work_units"], "id", kernel.Text(input["work_unit"])) == nil {
				return s.Reject("USER_DECISION_CONTEXT_REQUIRED", "缺少当前工作单元")
			}
			return AssertWorkUnitUserDecisionSemantic(s, kernel.Text(input["work_unit"]), kernel.ApMap(input["state"]))
		})
	}
}
