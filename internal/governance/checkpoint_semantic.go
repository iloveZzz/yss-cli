package governance

import (
	"encoding/json"
	"fmt"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"path/filepath"
	"strings"
)

func init() {
	registerSemanticValidator("checkpoint", verifyCheckpointSemantic)
	registerSemanticValidator("tracking-entry", verifyTrackingEntrySemantic)
	registerSemanticValidator("checkpoint-boundary", verifyCheckpointBoundarySemantic)
}

func verifyCheckpointBoundarySemantic(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	cp, err := s.Doc(ref)
	if err != nil {
		return err
	}
	policy, _, err := s.Orchestration()
	if err != nil {
		return err
	}
	if kernel.SemanticDefinition(s.Registry, "stages", kernel.Text(cp["stage"])) == nil {
		return s.Reject("CHECKPOINT_BOUNDARY", "当前 stage 未登记")
	}
	if cp["next_work_unit"] != nil && kernel.SemanticDefinition(s.Registry, "work_units", kernel.Text(cp["next_work_unit"])) == nil {
		return s.Reject("CHECKPOINT_BOUNDARY", "下一工作单元未登记")
	}
	boundary := kernel.SemMap(cp["phase_boundary"])
	trace := kernel.SemMap(cp["stage_trace"])
	bpolicy := kernel.SemMap(policy["phase_boundary"])
	at := kernel.SemHas([]string{"paused-human-gate", "completed"}, kernel.Text(cp["status"])) || kernel.SemHas([]string{"handoff", "subagent", "compact"}, kernel.Text(boundary["decision"])) || (kernel.SemHas([]string{"orchestrate", "resume"}, kernel.Text(cp["mode"])) && (kernel.Text(cp["stage"]) != "stage.entry-triage" || kernel.Text(trace["completed_work_unit"]) != ""))
	if at && (boundary == nil || trace == nil) {
		return s.Reject("CHECKPOINT_BOUNDARY", "当前边界缺少 phase_boundary 或 stage_trace")
	}
	if cp["phase_boundary"] != nil {
		if boundary == nil {
			return s.Reject("CHECKPOINT_BOUNDARY", "phase_boundary 必须为对象")
		}
		evidence := kernel.SemMap(bpolicy["evidence"])
		for _, key := range kernel.SemStrings(evidence["required"]) {
			if strings.TrimSpace(kernel.Text(boundary[key])) == "" {
				return s.Reject("CHECKPOINT_BOUNDARY", "缺少 phase_boundary."+key)
			}
		}
		if !kernel.SemHas(kernel.SemStrings(bpolicy["choices"]), kernel.Text(boundary["decision"])) {
			return s.Reject("CHECKPOINT_BOUNDARY", "边界决策未登记")
		}
		for _, key := range kernel.SemStrings(kernel.SemMap(evidence["conditional"])[kernel.Text(boundary["decision"])]) {
			r := kernel.Text(boundary[key])
			if strings.TrimSpace(r) == "" {
				return s.Reject("CHECKPOINT_BOUNDARY", "缺少 "+key)
			}
			if strings.HasSuffix(key, "_ref") {
				if _, err = s.Bytes(r); err != nil {
					return err
				}
			}
		}
		if boundary["decision"] == "compact" && kernel.SemanticDefinition(s.Registry, "stages", kernel.Text(boundary["next_phase"])) == nil {
			return s.Reject("CHECKPOINT_BOUNDARY", "compact next_phase 未登记")
		}
	}
	if cp["stage_trace"] != nil {
		if trace == nil {
			return s.Reject("CHECKPOINT_BOUNDARY", "stage_trace 必须为对象")
		}
		for _, key := range kernel.SemStrings(kernel.SemMap(policy["checkpoint_policy"])["required_stage_trace_fields"]) {
			if key == "stage" {
				if trace[key] != cp["stage"] {
					return s.Reject("CHECKPOINT_BOUNDARY", "stage_trace.stage 不一致")
				}
			} else {
				if _, ok := trace[key].([]any); !ok {
					return s.Reject("CHECKPOINT_BOUNDARY", "stage_trace."+key+" 必须为数组")
				}
			}
		}
		if trace["completed_work_unit"] != nil && kernel.SemanticDefinition(s.Registry, "work_units", kernel.Text(trace["completed_work_unit"])) == nil {
			return s.Reject("CHECKPOINT_BOUNDARY", "completed_work_unit 未登记")
		}
		for _, key := range []string{"upstream_refs", "artifact_refs"} {
			for _, row := range kernel.SemList(trace[key]) {
				r := kernel.Text(row)
				if r == "" {
					r = kernel.Text(kernel.SemMap(row)["ref"])
				}
				if _, err = s.Bytes(r); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func verifyTrackingEntrySemantic(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	cp, err := s.Doc(ref)
	if err != nil {
		return err
	}
	identity, err := s.Doc("yss-project.yaml")
	if err != nil {
		return err
	}
	if identity["repository_mode"] == "template-source" {
		s.Report.Applicability = append(s.Report.Applicability, map[string]any{"id": "stage-tracking", "status": "not-applicable", "reason": "模板维护不创建产品阶段工作项"})
		return nil
	}
	workUnit := ""
	if kernel.Text(cp["task_id"]) != "" {
		workUnit = kernel.Text(cp["work_unit_id"])
		if kernel.TrackedUnits[workUnit] == "" {
			return nil
		}
		checkpointRef := kernel.Text(cp["checkpoint_ref"])
		if opts["checkpoint"] != "" && checkpointRef != opts["checkpoint"] {
			return s.Reject("TRACKING", "任务 checkpoint 与消费者范围不一致")
		}
		if checkpointRef == "" {
			config, e := kernel.Tracker(s.V)
			if e != nil {
				return e
			}
			version, _ := kernel.Integer(config["lifecycle_tracking_version"])
			if version == 1 {
				return s.Reject("TRACKING", "阶段任务缺少持久化 checkpoint 引用")
			}
			return nil
		}
		ref = checkpointRef
		cp, err = s.Doc(ref)
		if err != nil {
			return err
		}
		if err = s.Verify("checkpoint-boundary", ref, nil); err != nil {
			return err
		}
	}
	if cp["stage_tracking"] != nil {
		if err = s.ValidateSchema(kernel.TrackingSchemaRef, cp["stage_tracking"]); err != nil {
			return err
		}
	}
	_, err = kernel.CheckStage(s.V, cp, ref, s)
	if err != nil {
		return s.Reject("TRACKING", err.Error())
	}
	if workUnit != "" {
		t, err := kernel.AsTracking(cp)
		if err != nil {
			return err
		}
		if t == nil {
			return nil
		}
		index := map[string]kernel.WorkItem{}
		for _, item := range t.Items {
			index[item.ID] = item
		}
		runnable := false
		exists := false
		for _, item := range t.Items {
			if item.WorkUnit != workUnit {
				continue
			}
			exists = true
			if item.Progress == "blocked" || item.Progress == "cancelled" || item.Deferred != nil || item.RecheckRequired {
				continue
			}
			ready := true
			for _, dep := range item.Dependencies {
				if index[dep].Progress != "completed" {
					ready = false
				}
			}
			for _, binding := range item.SourceRefs {
				b, e := s.Bind(binding.Ref)
				if e != nil || b.Digest != binding.Digest {
					ready = false
				}
			}
			for _, completion := range item.Completion {
				for _, binding := range completion.EvidenceRefs {
					b, e := s.Bind(binding.Ref)
					if e != nil || b.Digest != binding.Digest {
						ready = false
					}
				}
			}
			if ready {
				runnable = true
			}
		}
		if !exists || !runnable {
			return s.Reject("TRACKING", "阶段入口缺少可执行的当前工作项")
		}
	}
	return nil
}

func verifyCheckpointSemantic(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	cp, err := s.Doc(ref)
	if err != nil {
		return err
	}
	if opts["history"] == "true" {
		return verifyHistoricalCheckpointSemantic(s, cp)
	}
	if err = s.ValidateSchema(".template-spec/process/schemas/lifecycle-checkpoint.schema.json", cp); err != nil {
		return err
	}
	if err = s.CurrentAsset(ref); err != nil {
		return err
	}
	identity, err := s.Doc("yss-project.yaml")
	if err != nil {
		return err
	}
	if cp["repository_mode"] != identity["repository_mode"] {
		return s.Reject("IDENTITY", "checkpoint 仓库身份与根合同矛盾")
	}
	for _, bucket := range []string{"gates", "checks", "artifacts"} {
		for id := range kernel.SemMap(cp[bucket]) {
			if kernel.SemanticDefinition(s.Registry, bucket, id) == nil {
				return s.Reject("IDENTITY", "未知或退役稳定ID: "+id)
			}
		}
	}
	for _, kind := range []string{"checkpoint-boundary", "tracking-entry"} {
		if err = s.Verify(kind, ref, opts); err != nil {
			return err
		}
	}
	required := kernel.SemHas([]string{"work-unit.technical-analysis", "work-unit.strategic-design-handoff", "work-unit.implementation-repository-preparation", "work-unit.ticket-decomposition", "work-unit.slice-implementation"}, kernel.Text(cp["next_work_unit"]))
	if err = s.Verify("business-checkpoint", ref, map[string]string{"required": fmt.Sprint(required)}); err != nil {
		return err
	}
	for id, row := range kernel.SemMap(cp["gates"]) {
		if kernel.SemMap(row)["status"] == "approved" {
			if err = s.GateChecks(cp, ref, id); err != nil {
				return err
			}
		}
	}
	if err = s.Verify("plan-checkpoint", ref, opts); err != nil {
		return err
	}
	if err = s.Verify("checkpoint-user-decisions", ref, opts); err != nil {
		return err
	}
	if err = s.Verify("execution-scope", ref, opts); err != nil {
		return err
	}
	if kernel.Text(kernel.SemMap(cp["stage_trace"])["completed_work_unit"]) != "" {
		if err = s.Verify("next-route", ref, opts); err != nil {
			return err
		}
	}
	recon := kernel.SemMap(cp["context_reconciliation"])
	if cp["repository_mode"] == "project-instance" && kernel.SemHas([]string{"paused-human-gate", "completed"}, kernel.Text(cp["status"])) {
		if recon["status"] != "reconciled" || kernel.Text(recon["ref"]) == "" {
			return s.Reject("CONTEXT", "批准或完成前缺少当前 Context 对账")
		}
		if err = s.Verify("context-reconciliation", kernel.Text(recon["ref"]), opts); err != nil {
			return err
		}
	} else if cp["repository_mode"] == "template-source" && recon["status"] != "not-applicable" {
		return s.Reject("CONTEXT", "模板维护 Context 必须带原因记录不适用")
	}
	return nil
}

// The retained historical contract changes exactly these two definitions. It
// cannot establish current ownership, approval, transition, or authorization.
func verifyHistoricalCheckpointSemantic(s *kernel.SemanticSession, cp map[string]any) error {
	const ref = ".template-spec/process/schemas/lifecycle-checkpoint.schema.json"
	path, err := safefs.Path(s.Root, ref)
	if err != nil {
		return err
	}
	issues, err := schema.ValidateValueWithReader(path, cp, func(file string) ([]byte, error) {
		local, err := filepath.Rel(s.Root, file)
		if err != nil {
			return nil, err
		}
		bytes, err := s.Bytes(filepath.ToSlash(local))
		if err != nil {
			return nil, err
		}
		if filepath.Clean(file) != filepath.Clean(path) {
			return bytes, nil
		}
		value, err := schema.Parse(bytes)
		if err != nil {
			return nil, err
		}
		doc := kernel.SemMap(value)
		properties := kernel.SemMap(doc["properties"])
		if properties["phase_boundary"] == nil || properties["stage_trace"] == nil {
			return nil, fmt.Errorf("历史结构适配不支持该本地 Schema")
		}
		properties["phase_boundary"] = map[string]any{"type": "object"}
		properties["stage_trace"] = map[string]any{"type": "object"}
		return json.Marshal(doc)
	})
	if err != nil {
		return s.Unavailable("CAPABILITY", err.Error())
	}
	if len(issues) > 0 {
		return s.Reject("SCHEMA", fmt.Sprintf("历史 checkpoint 结构拒绝: %v", issues))
	}
	s.Report.Scope = "historical-structure"
	s.Report.Applicability = append(s.Report.Applicability, map[string]any{"id": "checkpoint", "historical_only": true, "current_approval_checked": false, "transition_checked": false})
	return nil
}
