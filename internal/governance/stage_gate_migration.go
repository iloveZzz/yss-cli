package governance

import (
	"context"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/governance/approval"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"path"
	"strings"
)

// Migration reads the original policy, decisions and evidence. Missing or
// unverifiable material becomes a gap, never a synthetic approval.
func PlanStageGateMigration(ctx context.Context, root, checkpointRef, mappingRef string) (WritePlan, error) {
	plan := WritePlan{SchemaVersion: 1, ProtocolVersion: 1, Kind: stageDecisionPlanKind, Root: root, Action: "migrate-decision", CheckpointRef: checkpointRef, InputRef: mappingRef, Options: map[string]string{}, Observed: map[string]domain.Descriptor{}, Scope: "stage-result-and-checkpoint-only"}
	s := kernel.NewSemanticSession(ctx, root, nil)
	if err := s.RequireGovernanceWrite(); err != nil {
		return plan, err
	}
	if !s.StageGatesEnabled() {
		return plan, s.Reject("CAPABILITY", "先显式装载完整四阶段来源；普通同步不迁移产品状态")
	}
	old, err := s.Doc(checkpointRef)
	if err != nil {
		return plan, err
	}
	if kernel.ContractN(old["schema_version"]) != 1 {
		return plan, s.Reject("STAGE_MIGRATION", "来源必须是保留原义的 checkpoint1")
	}
	mapping, err := s.Doc(mappingRef)
	if err != nil {
		return plan, err
	}
	if len(mapping) != 5 || kernel.ContractN(mapping["schema_version"]) != 1 || !kernel.ApIsArray(mapping["stage_sources"]) {
		return plan, s.Reject("STAGE_MIGRATION", "迁移映射字段无效")
	}
	target := kernel.Text(mapping["target_checkpoint_ref"])
	if target == checkpointRef || !kernel.ContractPath(target) {
		return plan, s.Reject("STAGE_MIGRATION", "新 checkpoint 必须使用另一安全路径，保留原件")
	}
	oldFeature, err := checkpointFeature(s.V, checkpointRef)
	if err != nil {
		return plan, err
	}
	newFeature, err := checkpointFeature(s.V, target)
	if err != nil || newFeature != oldFeature {
		return plan, s.Reject("STAGE_MIGRATION", "迁移不能改变功能归属")
	}
	sourceRef := kernel.Text(mapping["source_policy_ref"])
	raw, err := s.Bytes(sourceRef)
	if err != nil {
		return plan, err
	}
	if safefs.Digest(raw) != strings.TrimPrefix(kernel.Text(mapping["source_policy_digest"]), "sha256:") {
		return plan, s.Reject("STAGE_MIGRATION", "原政策摘要不符")
	}
	sourcePolicy, _, sourceDigest, err := schema.LoadOrchestration(s.Bytes, sourceRef)
	if err != nil {
		return plan, err
	}
	if kernel.ContractN(sourcePolicy["schema_version"]) != 2 || sourcePolicy["user_decision_policy"] == nil || sourcePolicy["stage_gates"] != nil {
		return plan, s.Reject("STAGE_MIGRATION", "旧角色政策须保留完整原来源后单独核验；不能推定批准")
	}
	source := *s
	source.GovernancePolicy = sourcePolicy
	source.GovernanceDigest = sourceDigest
	source.GovernanceVersion = 2
	cp := kernel.ApCopy(old)
	cp["schema_version"] = 2
	cp["gates"] = map[string]any{}
	cp["checks"] = map[string]any{}
	for _, key := range []string{"plan_continuation_ref", "plan_review_ref", "plan_approval_ref", "plan_user_decision_ref", "plan_review_control"} {
		delete(cp, key)
	}
	seen := map[string]bool{}
	gaps := []string{}
	for _, row := range kernel.ApRows(mapping["stage_sources"]) {
		gate := kernel.Text(row["gate_id"])
		if len(row) != 4 || s.StageGate(gate) == nil || seen[gate] || !kernel.ApUnique(row["asset_refs"]) || !kernel.ApIsArray(row["decision_refs"]) || !kernel.ApIsArray(row["evidence_refs"]) {
			return plan, s.Reject("STAGE_MIGRATION", "阶段来源映射无效或重复")
		}
		seen[gate] = true
		assets := []any{}
		for _, ref := range kernel.ApStrings(row["asset_refs"]) {
			b, e := s.Bind(ref)
			if e != nil {
				return plan, e
			}
			assets = append(assets, map[string]any{"ref": ref, "digest": b.Digest})
		}
		migrated := false
		for _, decisionRef := range kernel.ApStrings(row["decision_refs"]) {
			decision, e := source.Doc(decisionRef)
			if e != nil {
				continue
			}
			if decision["request"] == nil {
				continue
			} // Never treat an old approval label as an original human reply.
			for _, item := range kernel.ApRows(kernel.ApMap(decision["request"])["items"]) {
				if item["boundary"] != gate || kernel.Text(kernel.ApMap(item["subject"])["ref"]) != kernel.ApStrings(row["asset_refs"])[0] {
					continue
				}
				if _, e = approval.ValidateUserDecisionSemantic(&source, decision, []map[string]any{{"boundary": gate, "subject_ref": kernel.ApStrings(row["asset_refs"])[0], "scope": item["scope"]}}); e != nil {
					continue
				}
				checks := []any{}
				execution := ""
				for _, id := range kernel.ApStrings(s.StageGate(gate)["checks"]) {
					for _, proofRef := range kernel.ApStrings(row["evidence_refs"]) {
						log, e := s.Doc(proofRef)
						if e != nil || !kernel.ApContains(log["check_ids"], id) {
							continue
						}
						b, e := s.Bind(proofRef)
						if e != nil {
							return plan, e
						}
						result := "passed"
						if log["applicable"] == false {
							result = "not-applicable"
						}
						check := map[string]any{"id": id, "result": result, "inputs": log["input_bindings"], "evidence": []any{map[string]any{"ref": proofRef, "digest": b.Digest}}}
						if result == "not-applicable" {
							check["reason"] = log["reason"]
						}
						checks = append(checks, check)
						execution = proofRef
						break
					}
				}
				db, e := s.Bind(decisionRef)
				if e != nil {
					return plan, e
				}
				record := map[string]any{"schema_version": 1, "kind": "stage-result", "gate_id": gate, "feature_id": old["feature_id"], "scope": item["scope"], "assets": assets, "checks": checks, "outcome": "approved", "blocking_findings": []any{}, "authorization": map[string]any{"kind": "user-decision", "ref": decisionRef, "digest": db.Digest}, "provenance": map[string]any{"tool": "yss lifecycle decision migrate-plan", "execution_ref": execution}}
				data, e := jsonBytes(record)
				if e != nil {
					return plan, e
				}
				ref := path.Join(path.Dir(target), "stages", gate, safefs.Digest(data), "result.json")
				d, e := s.V.Watch(ref)
				if e != nil {
					return plan, e
				}
				if d.Type != "missing" {
					prior, e := s.Bytes(ref)
					if e != nil || string(prior) != string(data) {
						return plan, s.Reject("STAGE_IMMUTABLE", "阶段记录冲突")
					}
				}
				// Verify in an overlay before deciding whether a migrated exit exists.
				if e = s.V.Set(ref, data); e != nil {
					return plan, e
				}
				if e = s.Verify("stage-result", ref, nil); e != nil {
					delete(s.V.Overlay, ref)
					continue
				}
				kernel.ApMap(cp["gates"])[gate] = map[string]any{"record_ref": ref, "record_digest": safefs.Digest(data)}
				migrated = true
				break
			}
			if migrated {
				break
			}
		}
		if !migrated {
			gaps = append(gaps, gate)
		}
	}
	for _, gate := range kernel.StageGateIDs {
		if !seen[gate] {
			gaps = append(gaps, gate)
		}
	}
	// Gaps are explicit resumable state, with every original byte retained.
	cp["status"] = "running"
	cp["next_work_unit"] = "work-unit.plan-requirements"
	cp["stage"] = "stage.plan"
	if err = s.ValidateSchema(".template-spec/process/schemas/lifecycle-checkpoint-v2.schema.json", cp); err != nil {
		return plan, err
	}
	data, err := jsonBytes(cp)
	if err != nil {
		return plan, err
	}
	d, err := s.V.Watch(target)
	if err != nil {
		return plan, err
	}
	if d.Type != "missing" {
		prior, e := s.Bytes(target)
		if e != nil || string(prior) != string(data) {
			return plan, s.Reject("STAGE_MIGRATION", "目标 checkpoint 已存在，保留原件")
		}
	}
	if err = s.V.Set(target, data); err != nil {
		return plan, err
	}
	plan.Options["target_checkpoint_ref"] = target
	plan.Options["unresolved_gates"] = strings.Join(gaps, ",")
	if err = s.Finish(); err != nil {
		return plan, err
	}
	return finishStageDecisionPlan(plan, s)
}
