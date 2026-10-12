package governance

import (
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"strings"
)

func init() { registerSemanticValidator("approval", verifyApprovalSemantic) }

// verifyApprovalSemantic consumes only independently selected checkpoint/task
// context. History performs structure checks and never current authorization.
func verifyApprovalSemantic(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	opts = semanticOptions(opts)
	if opts["gate"] == "" {
		opts["gate"] = opts["boundary"]
	}
	record, e := s.Doc(ref)
	if e != nil {
		return e
	}
	if kernel.ApText(record["kind"]) == "review-bundle" {
		if e = s.ValidateSchema(".template-spec/process/schemas/review-bundle.schema.json", record); e != nil {
			return e
		}
	} else {
		if e = s.ValidateSchema(kernel.ApprovalSchemaRef, record); e != nil {
			return e
		}
	}
	if opts["expected"] != "" && (opts["task"] != "" || opts["checkpoint"] != "" || s.CheckpointRef != "") {
		return kernel.ApFail(s, "APPROVAL_CONTEXT_REQUIRED", "--expected 不能与 checkpoint 或 task 同用")
	}
	if opts["history"] == "true" {
		if opts["checkpoint"] != "" || s.CheckpointRef != "" || opts["task"] != "" || opts["require-approved"] == "true" {
			return kernel.ApFail(s, "APPROVAL_CONTEXT_REQUIRED", "history 不得用于当前批准校验")
		}
		if kernel.ApText(record["kind"]) == "review-bundle" {
			if _, e = kernel.ReviewBundleRowsSemantic(s, record); e != nil {
				return e
			}
		}
		s.Report.Applicability = append(s.Report.Applicability, map[string]any{"kind": "approval", "mode": "history-only", "execution_authorization": "not-evaluated"})
		return nil
	}
	if err := kernel.RejectProgressionEvidence(s, ref); err != nil {
		return err
	}
	if err := kernel.CurrentApprovalReferencesSemantic(s, record); err != nil {
		return err
	}
	checkpointRef := opts["checkpoint"]
	if checkpointRef == "" {
		checkpointRef = s.CheckpointRef
	}
	var checkpoint map[string]any
	if checkpointRef != "" {
		checkpoint, e = s.Doc(checkpointRef)
		if e != nil {
			return e
		}
		if e = s.ValidateSchema(".template-spec/process/schemas/lifecycle-checkpoint.schema.json", checkpoint); e != nil {
			return e
		}
	}
	rows := []map[string]any{record}
	if kernel.ApText(record["kind"]) == "review-bundle" {
		rows, e = kernel.ReviewBundleRowsSemantic(s, record)
		if e != nil {
			return e
		}
	}
	for _, row := range rows {
		gate := kernel.ApText(row["gate_id"])
		if opts["gate"] != "" && gate != opts["gate"] {
			continue
		}
		selected, e := kernel.SelectApprovalRecordSemantic(s, record, gate)
		if e != nil {
			return e
		}
		if gate == "gate.plan-approved" {
			if checkpointRef == "" {
				return kernel.ApFail(s, "APPROVAL_CONTEXT_REQUIRED", "Plan 聚合批准须完整当前 checkpoint")
			}
			if e = s.Verify("plan-aggregate", checkpointRef, map[string]string{"approval": ref, "gate": gate}); e != nil {
				return e
			}
			continue
		}
		var expected map[string]any
		if checkpoint != nil {
			state := kernel.ApMap(kernel.ApMap(checkpoint["gates"])[gate])
			if state == nil {
				state = kernel.ApMap(kernel.ApMap(checkpoint["checks"])[gate])
			}
			expected, e = kernel.ApprovalExpectationFromState(s, gate, state)
		} else if opts["expected"] != "" {
			// 调用方独立给出的当前消费期望（Node 适配层或其他消费者），与 checkpoint、task 互斥。
			expected, e = approvalExplicitExpectation(s, opts["expected"], gate)
		} else if opts["task"] != "" {
			task, e2 := s.Doc(opts["task"])
			if e2 != nil {
				return e2
			}
			bound, e2 := s.Bind(opts["task"])
			if e2 != nil {
				return e2
			}
			expected, e = kernel.ApprovalExpectedFromTaskSemantic(s, opts["task"], task, gate, strings.TrimPrefix(bound.Digest, "sha256:"), nil)
		} else {
			return kernel.ApFail(s, "APPROVAL_CONTEXT_REQUIRED", "当前批准必须由 checkpoint 或显式 task 提供期望")
		}
		if e != nil {
			return e
		}
		if e = kernel.AssertCurrentApprovalSemantic(s, selected, expected); e != nil {
			return e
		}
	}
	if opts["gate"] != "" && kernel.ApFind(rows, "gate_id", opts["gate"]) == nil {
		return kernel.ApFail(s, "APPROVAL_BUNDLE_INVALID", "批准资产不包含消费者 gate")
	}
	s.Report.Applicability = append(s.Report.Applicability, map[string]any{"kind": "approval", "mode": "current-binding", "execution_authorization": "not-evaluated"})
	return nil
}

// expected 文件可以是单个期望对象，或按 boundary 区分的数组；只取与当前 gate 对应的一项。
func approvalExplicitExpectation(s *kernel.SemanticSession, ref, gate string) (map[string]any, error) {
	b, e := s.Bytes(ref)
	if e != nil {
		return nil, e
	}
	value, e := schema.Parse(b)
	if e != nil {
		return nil, s.Unavailable("INPUT", e.Error())
	}
	if single, ok := value.(map[string]any); ok {
		return single, nil
	}
	if rows, ok := value.([]any); ok {
		if row := kernel.ApFind(rows, "boundary", gate); row != nil {
			return row, nil
		}
	}
	return nil, kernel.ApFail(s, "APPROVAL_CONTEXT_REQUIRED", "期望文件没有对应当前 gate 的项")
}
