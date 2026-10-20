package governance

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/governance/approval"
	"github.com/iloveZzz/yss-cli/internal/governance/handoff"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const stageDecisionPlanKind = "stage-decision-go-plan"

func PlanStageResult(ctx context.Context, root, checkpointRef, inputRef string) (WritePlan, error) {
	plan := WritePlan{SchemaVersion: 1, ProtocolVersion: 1, Kind: stageDecisionPlanKind, Root: root, Action: "decision", CheckpointRef: checkpointRef, InputRef: inputRef, Options: map[string]string{}, Observed: map[string]domain.Descriptor{}, Scope: "stage-result-and-checkpoint-only"}
	s := kernel.NewSemanticSession(ctx, root, nil)
	if err := s.RequireGovernanceWrite(); err != nil {
		return plan, err
	}
	if !s.StageGatesEnabled() {
		return plan, s.Reject("CAPABILITY", "阶段登记只适用显式四阶段 Spec 政策")
	}
	if _, err := checkpointFeature(s.V, checkpointRef); err != nil {
		return plan, err
	}
	cp, err := s.Doc(checkpointRef)
	if err != nil {
		return plan, err
	}
	if err = s.ValidateSchema(".template-spec/process/schemas/lifecycle-checkpoint-v2.schema.json", cp); err != nil {
		return plan, err
	}
	input, err := s.Doc(inputRef)
	if err != nil {
		return plan, err
	}
	if input["feature_id"] != cp["feature_id"] {
		return plan, s.Reject("STAGE_SCOPE", "阶段输入不属于当前功能")
	}
	if input["outcome"] == "approved" {
		previous := map[string]string{"gate.spec-baseline-approved": "gate.plan-approved", "gate.implementation-admitted": "gate.spec-baseline-approved", "gate.delivery-accepted": "gate.implementation-admitted"}[kernel.Text(input["gate_id"])]
		if previous != "" {
			if err = approval.VerifyCheckpointStage(s, checkpointRef, previous, ""); err != nil {
				return plan, err
			}
		}
	}
	if err = verifyStageCandidate(s, inputRef); err != nil {
		return plan, err
	}
	raw, err := s.Bytes(inputRef)
	if err != nil {
		return plan, err
	}
	recordRef := path.Join(path.Dir(checkpointRef), "stages", kernel.Text(input["gate_id"]), safefs.Digest(raw), "result.json")
	if inputRef == checkpointRef || inputRef == recordRef {
		return plan, s.Reject("STAGE_SELF_BINDING", "登记输入不能占用可变 checkpoint 或目标记录")
	}
	if d, e := s.V.Watch(recordRef); e != nil {
		return plan, e
	} else if d.Type != "missing" {
		previous, e := s.Bytes(recordRef)
		if e != nil || !bytes.Equal(previous, raw) {
			return plan, s.Reject("STAGE_IMMUTABLE", "已有阶段版本不可覆盖")
		}
	}
	if err = s.V.Set(recordRef, raw); err != nil {
		return plan, err
	}
	gates := kernel.ApMap(cp["gates"])
	if gates == nil {
		gates = map[string]any{}
		cp["gates"] = gates
	}
	gates[kernel.Text(input["gate_id"])] = map[string]any{"record_ref": recordRef, "record_digest": safefs.Digest(raw)}
	data, err := jsonBytes(cp)
	if err != nil {
		return plan, err
	}
	if err = s.V.Set(checkpointRef, data); err != nil {
		return plan, err
	}
	if input["outcome"] == "approved" {
		if err = approval.VerifyCheckpointStage(s, checkpointRef, kernel.Text(input["gate_id"]), ""); err != nil {
			return plan, err
		}
	}
	if err = s.Finish(); err != nil {
		return plan, err
	}
	return finishStageDecisionPlan(plan, s)
}

func verifyStageCandidate(s *kernel.SemanticSession, ref string) error {
	record, err := s.Doc(ref)
	if err != nil {
		return err
	}
	err = s.Verify("stage-result", ref, nil)
	if err != nil && record["outcome"] != "approved" && (kernel.SemanticCode(err) == "STAGE_PENDING" || kernel.SemanticCode(err) == "STAGE_CHECK_FAILED") {
		return nil
	}
	return err
}
func finishStageDecisionPlan(plan WritePlan, s *kernel.SemanticSession) (WritePlan, error) {
	for ref, d := range s.V.Observed {
		if ref != ".yss/transactions" && !strings.HasPrefix(ref, ".yss/transactions/") {
			plan.Observed[ref] = d
		}
	}
	ops, err := s.V.Ops()
	if err != nil {
		return plan, err
	}
	plan.Operations = ops
	plan.PlanDigest, err = nativePlanDigest(plan)
	return plan, err
}

func ApplyStageResult(ctx context.Context, root, planRef string) error {
	raw, err := os.ReadFile(planRef)
	if err != nil {
		return err
	}
	var plan WritePlan
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&plan); err != nil {
		return err
	}
	digest, err := nativePlanDigest(plan)
	if err != nil || plan.Kind != stageDecisionPlanKind || plan.Root != root || plan.SchemaVersion != 1 || plan.ProtocolVersion != 1 || plan.PlanDigest != digest || plan.Scope != "stage-result-and-checkpoint-only" || plan.ApprovalCreated {
		return domain.Fail("PLAN", "阶段登记计划身份或摘要无效")
	}
	var fresh WritePlan
	if plan.Action == "migrate-decision" {
		fresh, err = PlanStageGateMigration(ctx, root, plan.CheckpointRef, plan.InputRef)
	} else if plan.Action == "decision" {
		fresh, err = PlanStageResult(ctx, root, plan.CheckpointRef, plan.InputRef)
	} else {
		return domain.Fail("PLAN", "未知阶段登记动作")
	}
	if err != nil {
		return err
	}
	if fresh.PlanDigest != plan.PlanDigest {
		// Retry succeeds only when every planned output already has the exact bytes
		// and all other observed input descriptors remain unchanged.
		s := kernel.NewSemanticSession(ctx, root, nil)
		written := map[string]bool{}
		for _, op := range plan.Operations {
			if op.Delete || op.Directory {
				return domain.Fail("PLAN", "阶段登记不删除文件")
			}
			b, e := s.Bytes(op.Path)
			if e != nil || !bytes.Equal(b, op.Data) {
				return domain.Fail("INPUT_DRIFT", "阶段登记输入或 checkpoint 已变化")
			}
			written[op.Path] = true
		}
		for ref, before := range plan.Observed {
			if !written[ref] {
				now, e := s.V.Watch(ref)
				if e != nil || now != before {
					return domain.Fail("INPUT_DRIFT", "阶段登记依据已变化: "+ref)
				}
			}
		}
		return nil
	}
	validate := func() error {
		s := kernel.NewSemanticSession(ctx, root, nil)
		s.InsideTransaction = true
		if e := s.Authorities(); e != nil {
			return e
		}
		if !s.StageGatesEnabled() {
			return domain.Fail("CAPABILITY", "阶段政策已改变")
		}
		if plan.Action == "decision" {
			if e := verifyStageCandidate(s, plan.InputRef); e != nil {
				return e
			}
			input, e := s.Doc(plan.InputRef)
			if e != nil {
				return e
			}
			if input["outcome"] == "approved" {
				if e = approval.VerifyCheckpointStage(s, plan.CheckpointRef, kernel.Text(input["gate_id"]), ""); e != nil {
					return e
				}
			}
		}
		return s.Finish()
	}
	paths := []string{}
	for _, op := range fresh.Operations {
		paths = append(paths, op.Path)
	}
	if len(paths) > 0 {
		if err = ValidateStageDecisionTransaction(root, "spec", paths); err != nil {
			return err
		}
	}
	_, err = transaction.ApplyContextWithValidation(ctx, root, "stage-decision", fresh.Operations, fresh.Observed, nil, validate)
	return err
}

// Lifecycle decision plan/apply share the existing transaction engine and never
// manufacture a user's response. Plan files are explicit new output files.
func RunStageDecision(ctx context.Context, action, root string, args map[string]string) (any, error) {
	if action == "apply" {
		if args["plan"] == "" {
			return nil, domain.Fail("PLAN_REQUIRED", "apply 必须消费 --plan <已保存计划>")
		}
		err := ApplyStageResult(ctx, root, args["plan"])
		return map[string]any{"status": "applied", "approval_created": false, "execution_authorization": "not-evaluated"}, err
	}
	var plan WritePlan
	var err error
	if action == "plan" {
		plan, err = PlanStageResult(ctx, root, args["checkpoint"], args["input"])
	} else if action == "migrate-plan" {
		plan, err = PlanStageGateMigration(ctx, root, args["checkpoint"], args["input"])
	} else {
		return nil, domain.Fail("ARGUMENT", "未知阶段登记动作")
	}
	if err != nil {
		return nil, err
	}
	if args["out"] == "" {
		return nil, domain.Fail("PLAN_REQUIRED", "计划需要 --out 新文件")
	}
	out, err := filepath.Abs(args["out"])
	if err != nil {
		return nil, err
	}
	for _, op := range plan.Operations {
		if out == filepath.Join(root, filepath.FromSlash(op.Path)) {
			return nil, domain.Fail("PROTECTED", "计划不能占用目标资产")
		}
	}
	if err = handoff.SaveBaselineJSON(out, plan); err != nil {
		return nil, err
	}
	return plan, nil
}

// Recovery can restore only one feature's checkpoint and immutable stage rows.
func ValidateStageDecisionTransaction(root, profile string, paths []string) error {
	if profile != "spec" {
		return domain.Fail("IDENTITY", "阶段登记事务只允许 Spec")
	}
	s := kernel.NewSemanticSession(context.Background(), root, nil)
	layout, err := kernel.ViewWorkLayout(s.V)
	if err != nil {
		return err
	}
	feature := ""
	checkpointCount := 0
	for _, ref := range paths {
		current := ""
		if candidate, e := layout.CheckpointFeature(ref); e == nil {
			current = candidate
			checkpointCount++
		} else {
			prefix := layout.Root + "/"
			if !strings.HasPrefix(ref, prefix) {
				return domain.Fail("PROTECTED", "阶段记录事务越界")
			}
			parts := strings.Split(strings.TrimPrefix(ref, prefix), "/")
			if len(parts) != 5 || parts[1] != "stages" || !kernel.SemHas(kernel.StageGateIDs, parts[2]) || len(parts[3]) != 64 || parts[4] != "result.json" {
				return domain.Fail("PROTECTED", "阶段记录事务路径非法")
			}
			for _, c := range parts[3] {
				if !strings.ContainsRune("0123456789abcdef", c) {
					return domain.Fail("PROTECTED", "阶段版本摘要非法")
				}
			}
			current = parts[0]
		}
		if feature != "" && feature != current {
			return domain.Fail("PROTECTED", "阶段登记不能跨功能")
		}
		feature = current
	}
	if checkpointCount != 1 || len(paths) > 5 {
		return domain.Fail("PROTECTED", "阶段登记必须恰好更新一个功能 checkpoint")
	}
	return nil
}
