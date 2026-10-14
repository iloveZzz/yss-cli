package approval

import (
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"strings"
)

func init() { kernel.RegisterSemanticValidator("approval", verifyApprovalSemantic) }

// verifyApprovalSemantic consumes only independently selected checkpoint/task
// context. History performs structure checks and never current authorization.
func verifyApprovalSemantic(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	opts = kernel.SemanticOptions(opts)
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
	if opts["binding"] != "" && opts["expected"] != "" {
		return kernel.ApFail(s, "APPROVAL_CONTEXT_REQUIRED", "--binding 不能与 --expected 同用")
	}
	if s.Report.Coverage == nil {
		s.Report.Coverage = map[string]any{}
	}
	if (opts["expected"] != "" || opts["binding"] != "") && (opts["task"] != "" || opts["checkpoint"] != "" || s.CheckpointRef != "") {
		return kernel.ApFail(s, "APPROVAL_CONTEXT_REQUIRED", "--expected 不能与 checkpoint 或 task 同用")
	}
	if opts["history"] == "true" {
		if opts["checkpoint"] != "" || s.CheckpointRef != "" || opts["task"] != "" || opts["require-approved"] == "true" {
			return kernel.ApFail(s, "APPROVAL_CONTEXT_REQUIRED", "history 不得用于当前批准校验")
		}
		if kernel.ApText(record["kind"]) == "review-bundle" {
			if _, e = ReviewBundleRowsSemantic(s, record); e != nil {
				return e
			}
		}
		s.Report.Applicability = append(s.Report.Applicability, map[string]any{"kind": "approval", "mode": "history-only", "execution_authorization": "not-evaluated"})
		return nil
	}
	if err := kernel.RejectProgressionEvidence(s, ref); err != nil {
		return err
	}
	if err := CurrentApprovalReferencesSemantic(s, record); err != nil {
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
		rows, e = ReviewBundleRowsSemantic(s, record)
		if e != nil {
			return e
		}
	}
	for _, row := range rows {
		gate := kernel.ApText(row["gate_id"])
		if opts["gate"] != "" && gate != opts["gate"] {
			continue
		}
		selected, e := SelectApprovalRecordSemantic(s, record, gate)
		if e != nil {
			return e
		}
		if gate == "gate.plan-approved" {
			// Older source policies use independently bound approvals without a bounded review cycle.
			bounded := selected["plan_review_binding"] != nil || checkpoint["plan_review_control"] != nil
			for _, rule := range kernel.ApRows(kernel.ApMap(kernel.ApMap(s.Roles["gate_policy"])["review_execution"])["review_bundles"]) {
				bounded = bounded || rule["aggregate_gate"] == gate && rule["aggregate_additional_review_task"] == "forbidden"
			}
			if bounded {
				if checkpointRef == "" {
					return kernel.ApFail(s, "APPROVAL_CONTEXT_REQUIRED", "Plan 聚合批准须完整当前 checkpoint")
				}
				if e = s.Verify("plan-aggregate", checkpointRef, map[string]string{"approval": ref, "gate": gate}); e != nil {
					return e
				}
				continue
			}
		}
		var expected map[string]any
		if checkpoint != nil {
			state := kernel.ApMap(kernel.ApMap(checkpoint["gates"])[gate])
			if state == nil {
				state = kernel.ApMap(kernel.ApMap(checkpoint["checks"])[gate])
			}
			expected, e = ApprovalExpectationFromState(s, gate, state)
		} else if opts["binding"] != "" {
			// 调用方只给出消费绑定，期望由 yss 自己计算（对应 Node 的 approvalExpectationFrom*/ForBoundAsset）。
			expected, e = approvalBindingExpectation(s, opts["binding"], gate)
			if e == nil {
				s.Report.Coverage["approval_expected"] = expected
			}
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
			// 没有消费期望时仍先核验会签身份与规则（否决、角色等），再要求消费上下文；与 Node 的顺序一致。
			if _, e = ApprovalRuleSemantic(s, selected); e != nil {
				return e
			}
			return kernel.ApFail(s, "APPROVAL_CONTEXT_REQUIRED", "当前批准必须由 checkpoint 或显式 task 提供期望")
		}
		if e != nil {
			return e
		}
		if e = AssertCurrentApprovalSemantic(s, selected, expected); e != nil {
			return e
		}
		rule, e := ApprovalRuleSemantic(s, selected)
		if e != nil {
			return e
		}
		validated, _ := s.Report.Coverage["approval_validated"].([]any)
		s.Report.Coverage["approval_validated"] = append(validated, map[string]any{"gate_id": gate, "rule": rule})
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

// binding 文件：{mode: state|subject|bound-asset, boundary?, state|subject_ref|binding, scope?, review_package?}；
// 数组按 boundary 取项。boundary 缺省取当前 gate。
func approvalBindingExpectation(s *kernel.SemanticSession, ref, gate string) (map[string]any, error) {
	b, e := s.Bytes(ref)
	if e != nil {
		return nil, e
	}
	value, e := schema.Parse(b)
	if e != nil {
		return nil, s.Unavailable("INPUT", e.Error())
	}
	var row map[string]any
	switch v := value.(type) {
	case map[string]any:
		row = v
	case []any:
		for _, item := range kernel.ApRows(v) {
			if kernel.ApText(item["boundary"]) == gate {
				row = item
			}
		}
	}
	if row == nil {
		return nil, kernel.ApFail(s, "APPROVAL_CONTEXT_REQUIRED", "绑定文件没有对应当前 gate 的项")
	}
	boundary := kernel.ApText(row["boundary"])
	if boundary == "" {
		boundary = gate
	}
	var expected map[string]any
	switch kernel.ApText(row["mode"]) {
	case "state":
		expected, e = ApprovalExpectationFromStateWith(s, boundary, kernel.ApMap(row["state"]), row["review_package"])
	case "subject":
		expected, e = ApprovalExpectationFromSubject(s, boundary, kernel.ApText(row["subject_ref"]), row["review_package"])
	case "bound-asset":
		var scope []string
		if row["scope"] != nil {
			scope = kernel.ApStrings(row["scope"])
		}
		expected, e = ApprovalExpectationForBoundAsset(s, boundary, row["binding"], scope, row["review_package"])
	default:
		return nil, kernel.ApFail(s, "APPROVAL_CONTEXT_REQUIRED", "绑定模式必须是 state、subject 或 bound-asset")
	}
	if e != nil {
		return nil, e
	}
	// 调用方在算出期望时冻结的值：校验当下按绑定重算出的期望必须与之一致，保持「期望冻结」语义，
	// 避免主体在算出期望之后被改动、记录又随之改动时被放行。
	if frozen := kernel.ApMap(row["frozen"]); frozen != nil {
		same := kernel.ApText(frozen["subject_ref"]) == kernel.ApText(expected["subject_ref"]) &&
			strings.TrimPrefix(kernel.ApText(frozen["subject_digest"]), "sha256:") == strings.TrimPrefix(kernel.ApText(expected["subject_digest"]), "sha256:") &&
			kernel.ApSetEqual(frozen["approval_scope"], expected["approval_scope"]) &&
			kernel.ApText(frozen["drafter_principal_ref"]) == kernel.ApText(expected["drafter_principal_ref"])
		if same {
			for _, item := range kernel.ApRows(frozen["basis"]) {
				found := false
				for _, got := range kernel.ApRows(expected["basis"]) {
					if kernel.ApText(item["ref"]) == kernel.ApText(got["ref"]) && strings.TrimPrefix(kernel.ApText(item["digest"]), "sha256:") == strings.TrimPrefix(kernel.ApText(got["digest"]), "sha256:") {
						found = true
					}
				}
				same = same && found
			}
		}
		if !same {
			return nil, kernel.ApFail(s, "APPROVAL_CURRENT_INVALID", "当前主体摘要与消费边界冻结摘要不匹配")
		}
	}
	return expected, nil
}
