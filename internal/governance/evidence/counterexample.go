package evidence

import (
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"strings"
	"time"
)

func MaintenanceCounterexample(s *kernel.SemanticSession, evidence map[string]any, trigger string) error {
	run, err := s.Doc(kernel.Text(evidence["run_ref"]))
	if err != nil {
		return err
	}
	n, ok := kernel.Integer(run["schema_version"])
	exit, exitOK := kernel.Integer(run["exit_code"])
	if !ok || n != 1 || run["kind"] != "maintenance-counterexample-run" || run["trigger"] != trigger || run["command"] != evidence["command"] || !exitOK || exit != 0 {
		return s.Reject("MAINTENANCE_COUNTEREXAMPLE", "反例运行身份、命令或退出码无效")
	}
	start, a := time.Parse(time.RFC3339Nano, kernel.Text(run["started_at"]))
	end, b := time.Parse(time.RFC3339Nano, kernel.Text(run["finished_at"]))
	if a != nil || b != nil || end.Before(start) {
		return s.Reject("MAINTENANCE_COUNTEREXAMPLE", "反例运行时间无效")
	}
	logBinding := kernel.SemMap(run["log"])
	if err = s.Basis([]any{logBinding}); err != nil {
		return err
	}
	bytes, err := s.Bytes(kernel.Text(logBinding["ref"]))
	if err != nil {
		return err
	}
	value, err := schema.Parse(bytes)
	if err != nil {
		return s.Reject("MAINTENANCE_COUNTEREXAMPLE", "运行日志无法解析")
	}
	log, valid := value.([]any)
	if !valid {
		return s.Reject("MAINTENANCE_COUNTEREXAMPLE", "运行日志必须为数组")
	}
	if len(kernel.SemList(run["assertions"])) == 0 {
		return s.Reject("MAINTENANCE_COUNTEREXAMPLE", "反例缺少拒绝断言")
	}
	for _, row := range kernel.SemList(run["assertions"]) {
		assertion := kernel.SemMap(row)
		actual := kernel.ApFind(log, "id", kernel.Text(assertion["id"]))
		x, ok := kernel.Integer(actual["exit_code"])
		output := kernel.Text(actual["stderr"]) + "\n" + kernel.Text(actual["stdout"])
		diagnostic := kernel.Text(assertion["expected_diagnostic"])
		if kernel.Text(assertion["id"]) == "" || assertion["expected"] != "reject" || assertion["actual"] != "rejected" || len(kernel.SemList(actual["command"])) == 0 || !ok || x == 0 || strings.TrimSpace(output) == "" || strings.TrimSpace(diagnostic) == "" || !strings.Contains(output, diagnostic) {
			return s.Reject("MAINTENANCE_COUNTEREXAMPLE", "断言缺少实际拒绝结果或预期原因")
		}
	}
	if err = s.Basis(run["inputs"]); err != nil {
		return err
	}
	// This contract hashes JSON.stringify of the ordered input rows, rather
	// than the sorted structural digest used by approval documents.
	raw, err := s.Bytes(kernel.Text(evidence["run_ref"]))
	if err != nil {
		return err
	}
	encoded, err := kernel.OrderedAssetJSON(raw, "inputs")
	if err != nil {
		return err
	}
	if kernel.Text(run["input_digest"]) != "sha256:"+safefs.Digest(encoded) {
		return s.Reject("MAINTENANCE_COUNTEREXAMPLE", "反例输入范围摘要不一致")
	}
	return nil
}
