package handoff

import (
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/governance/approval"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"os"
	"strings"
)

func CheckpointAsset(cp map[string]any, key string, ids ...string) string {
	if ref := kernel.Text(cp[key]); ref != "" {
		return ref
	}
	for _, id := range ids {
		if ref := kernel.Text(kernel.SemMap(kernel.SemMap(cp["artifacts"])[id])["ref"]); ref != "" {
			return ref
		}
	}
	return ""
}

func InspectSpecBaselineSource(s *kernel.SemanticSession, cpRef string, local ...bool) (map[string]any, []string, error) {
	if !kernel.ContractPath(cpRef) {
		return nil, nil, s.Reject("SPEC_BASELINE", "Spec 基线需要项目内当前 checkpoint")
	}
	if err := s.Authorities(); err != nil {
		return nil, nil, err
	}
	identity, err := s.Doc("yss-project.yaml")
	if err != nil {
		return nil, nil, err
	}
	profile, err := s.Doc(".template-spec/process/harness-profile.yaml")
	if err != nil {
		return nil, nil, err
	}
	if identity["repository_mode"] != "project-instance" || profile["profile_id"] != "harness.spec-template" {
		return nil, nil, s.Reject("SPEC_BASELINE", "导出来源必须为 Spec 项目实例")
	}
	cp, err := s.Doc(cpRef)
	if err != nil {
		return nil, nil, err
	}
	if s.StageGatesEnabled() {
		return inspectStageSpecBaselineSource(s, cpRef, cp)
	}
	if kernel.SemMap(kernel.SemMap(cp["gates"])["gate.spec-baseline-approved"])["status"] != "approved" {
		return nil, nil, s.Reject("SPEC_BASELINE", "当前 Spec 基线尚未批准")
	}
	if err = s.ValidateSchema(".template-spec/process/schemas/lifecycle-checkpoint.schema.json", cp); err != nil {
		return nil, nil, err
	}
	if cp["repository_mode"] != "project-instance" || cp["upstream_spec_baseline"] != nil {
		return nil, nil, s.Reject("SPEC_BASELINE_CURRENT", "来源 checkpoint 必须是本地批准的 Spec 项目实例")
	}
	reconciliation := kernel.SemMap(cp["context_reconciliation"])
	if reconciliation["status"] != "reconciled" || kernel.Text(reconciliation["ref"]) == "" {
		return nil, nil, s.Reject("SPEC_BASELINE_CURRENT", "来源 checkpoint 的 Context 对账尚未完成")
	}
	if err = s.Verify("context-reconciliation", kernel.Text(reconciliation["ref"]), map[string]string{}); err != nil {
		return nil, nil, err
	}
	specAsset := kernel.SemMap(kernel.SemMap(cp["artifacts"])["artifact.spec"])
	specRef := kernel.Text(specAsset["ref"])
	if specAsset["status"] != "approved" || specRef == "" {
		return nil, nil, s.Reject("SPEC_BASELINE_CURRENT", "来源 Spec 资产必须是当前已批准的 artifact.spec")
	}
	if direct := kernel.Text(cp["spec_ref"]); direct != "" && direct != specRef {
		return nil, nil, s.Reject("SPEC_BASELINE_CURRENT", "来源 checkpoint 的当前 Spec 引用冲突")
	}
	specBytes, err := s.Bytes(specRef)
	if err != nil {
		return nil, nil, err
	}
	currentSpecBound := false
	for _, row := range kernel.SemList(kernel.SemMap(kernel.SemMap(cp["gates"])["gate.spec-baseline-approved"])["basis"]) {
		binding := kernel.SemMap(row)
		if binding["ref"] == specRef && strings.TrimPrefix(kernel.Text(binding["digest"]), "sha256:") == safefs.Digest(specBytes) {
			currentSpecBound = true
		}
	}
	if !currentSpecBound {
		return nil, nil, s.Reject("SPEC_BASELINE_CURRENT", "来源 checkpoint 的 Spec 批准依据未直接绑定当前原始字节")
	}
	if err = s.CurrentAsset(cpRef); err != nil {
		return nil, nil, err
	}
	for _, gate := range []string{"gate.plan-approved", "gate.spec-baseline-approved"} {
		if kernel.SemMap(kernel.SemMap(cp["gates"])[gate])["status"] != "approved" {
			return nil, nil, s.Reject("SPEC_BASELINE", "来源当前批准缺失: "+gate)
		}
		if err = approval.GateChecks(s, cp, cpRef, gate); err != nil {
			return nil, nil, err
		}
	}
	if err = s.Verify("plan-spec-entry", cpRef, map[string]string{}); err != nil {
		return nil, nil, err
	}
	planRef := CheckpointAsset(cp, "plan_ref", "artifact.plan")
	if review := kernel.Text(cp["plan_review_ref"]); review != "" {
		d, e := s.Doc(review)
		if e != nil {
			return nil, nil, e
		}
		if planRef == "" {
			planRef = kernel.Text(d["plan_ref"])
		} else if planRef != kernel.Text(d["plan_ref"]) {
			return nil, nil, s.Reject("SPEC_BASELINE_CURRENT", "来源当前 Plan 资产与已批准 Plan 审阅包冲突")
		}
	}
	if declared := kernel.Text(kernel.SemMap(kernel.SemMap(cp["artifacts"])["artifact.plan"])["ref"]); declared != "" && declared != planRef {
		return nil, nil, s.Reject("SPEC_BASELINE_CURRENT", "来源 checkpoint 的当前 Plan 引用冲突")
	}
	strategyRef := CheckpointAsset(cp, "domain_strategy_ref", "artifact.domain-strategy")
	stageRef := CheckpointAsset(cp, "stage_decision_package_ref", "artifact.stage-decision-package")
	ticketsRef := CheckpointAsset(cp, "business_ticket_set_ref", "artifact.business-ticket-set")
	if specRef == "" || ticketsRef == "" {
		return nil, nil, s.Reject("SPEC_BASELINE", "缺少当前 Spec 或业务票草案引用")
	}
	business, err := dependencies.ContractBusinessTicketsMode(s, ticketsRef, "draft")
	if err != nil {
		return nil, nil, err
	}
	if kernel.SemMap(business["spec"])["ref"] != specRef {
		return nil, nil, s.Reject("SPEC_BASELINE_SCOPE", "业务票草案未绑定本次批准的 Spec")
	}
	if err = specBaselineAssetCoverage(s, cp, "gate.spec-baseline-approved", specRef); err != nil {
		return nil, nil, err
	}
	for _, ref := range []string{planRef, strategyRef, stageRef} {
		if ref != "" {
			if err = specBaselineAssetCoverage(s, cp, "gate.plan-approved", ref); err != nil {
				return nil, nil, err
			}
		}
	}
	if err = specBaselineDesignAssets(s, strategyRef, stageRef); err != nil {
		return nil, nil, err
	}
	productDesign := kernel.SemMap(cp["delivery_impacts"])["ui"] == true
	if stageRef != "" {
		stage, e := s.Doc(stageRef)
		if e != nil {
			return nil, nil, e
		}
		impact := kernel.SemMap(stage["impact_assessment"])
		if v, ok := impact["product_design"].(bool); ok {
			productDesign = v
		} else {
			productDesign = impact["ui"] != false
		}
	} else {
		productDesign = true
	}
	cpBytes, err := s.Bytes(cpRef)
	if err != nil {
		return nil, nil, err
	}
	metadata, err := s.Doc(domain.MetadataFile)
	if err != nil {
		return nil, nil, err
	}
	templateCommit := kernel.Text(metadata["templateCommit"])
	if !kernel.CiCommit.MatchString(templateCommit) {
		return nil, nil, s.Reject("IDENTITY", "Spec 基线需要固定模板来源")
	}
	source := map[string]any{"profile_id": profile["profile_id"], "feature_id": cp["feature_id"], "checkpoint_ref": cpRef, "checkpoint_digest": "sha256:" + safefs.Digest(cpBytes), "template_commit": templateCommit, "spec_ref": specRef, "spec_digest": "sha256:" + safefs.Digest(specBytes), "plan_ref": planRef, "domain_strategy_ref": strategyRef, "stage_decision_package_ref": stageRef, "business_ticket_set_ref": ticketsRef, "product_design_required": productDesign}
	extra := []any{domain.MetadataFile, "yss-project.yaml", ".template-spec/process/harness-profile.yaml", ".template-spec/process/lifecycle-registry.yaml", ".template-spec/agents/yss-skill-registry.yaml"}
	_, orchestrationRef, e := s.Orchestration()
	if e != nil {
		return nil, nil, e
	}
	extra = append(extra, orchestrationRef)
	for _, ref := range []string{specRef, planRef, strategyRef, stageRef, ticketsRef} {
		if ref != "" {
			extra = append(extra, ref)
		}
	}
	fake := &NativeHandoff{Handoff: map[string]any{"source": map[string]any{}, "evidence_and_version_digests": []any{}}, Config: map[string]any{"approvals": map[string]any{}, "additional_files": extra}}
	refs, err := contractHandoffClosure(s, cpRef, fake)
	if err != nil {
		return nil, nil, err
	}
	return source, refs, nil
}

func SaveBaselineJSON(out string, value any) error {
	if out == "" {
		return domain.Fail("ARGUMENT", "需要 --out 新文件路径")
	}
	b, e := json.MarshalIndent(value, "", "  ")
	if e != nil {
		return e
	}
	f, e := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write(append(b, '\n'))
	closeErr := f.Close()
	if e != nil {
		return e
	}
	return closeErr
}

func specBaselineAssetCoverage(s *kernel.SemanticSession, cp map[string]any, gate, ref string) error {
	state := kernel.SemMap(kernel.SemMap(cp["gates"])[gate])
	record, err := s.Doc(kernel.Text(state["approval_ref"]))
	if err != nil {
		return err
	}
	record, err = approval.SelectApprovalRecordSemantic(s, record, gate)
	if err != nil {
		return err
	}
	// gateChecks has verified this actual approval record and its subject.
	// A checkpoint's extra basis rows only declare inputs; they grant no scope.
	basis := kernel.SemList(record["basis"])
	if gate == "gate.plan-approved" {
		// plan-spec-entry verified the review, its actual user decision and the
		// aggregate approval's coverage of every review basis row.
		review, err := s.Doc(kernel.Text(cp["plan_review_ref"]))
		if err != nil {
			return err
		}
		basis = kernel.SemList(review["basis"])
	}
	bytes, err := s.Bytes(ref)
	if err != nil {
		return err
	}
	if gate == "gate.spec-baseline-approved" && record["subject_ref"] == ref && strings.TrimPrefix(kernel.Text(record["subject_digest"]), "sha256:") == safefs.Digest(bytes) {
		return nil
	}
	for _, row := range basis {
		binding := kernel.SemMap(row)
		if binding["ref"] == ref && strings.TrimPrefix(kernel.Text(binding["digest"]), "sha256:") == safefs.Digest(bytes) {
			return nil
		}
	}
	return s.Reject("SPEC_BASELINE_SCOPE", "导出资产不在当前批准的实际依据范围: "+ref)
}

func specBaselineDesignAssets(s *kernel.SemanticSession, strategyRef, stageRef string) error {
	documents := map[string]map[string]any{}
	for _, asset := range []struct{ label, ref string }{{"domain-strategy", strategyRef}, {"stage-decision-package", stageRef}} {
		if asset.ref == "" {
			continue
		}
		doc, err := s.Doc(asset.ref)
		if err != nil {
			return err
		}
		version, valid := kernel.Integer(doc["schema_version"])
		if !valid || (version != 2 && version != 3) {
			return s.Reject("SPEC_BASELINE_SOURCE", "不支持的来源资产版本: "+asset.label)
		}
		suffix := ""
		if version == 3 {
			suffix = "-v3"
		}
		if err = s.ValidateSchema(".template-spec/process/schemas/strategic-handoff-"+asset.label+suffix+".schema.json", doc); err != nil {
			return err
		}
		if doc["status"] != "approved" {
			return s.Reject("SPEC_BASELINE_SOURCE", "来源资产尚未批准: "+asset.ref)
		}
		if _, err = s.ContextSnapshot(kernel.SemMap(doc["context_snapshot"])); err != nil {
			return err
		}
		documents[asset.label] = doc
	}
	strategy, stage := documents["domain-strategy"], documents["stage-decision-package"]
	if stage != nil {
		if strategy == nil {
			return s.Reject("SPEC_BASELINE_SOURCE", "当前方案决策缺少已登记的领域战略来源")
		}
		binding := kernel.SemMap(stage["domain_strategy_ref"])
		if binding["domain_strategy_id"] != strategy["domain_strategy_id"] || binding["domain_version"] != strategy["domain_version"] || binding["status"] != "approved" || binding["persisted_ref"] != strategyRef || binding["digest"] != dependencies.ContractDigest(strategy) {
			return s.Reject("SPEC_BASELINE_SOURCE", "当前方案决策未绑定本次批准的领域战略")
		}
	}
	return nil
}

func inspectStageSpecBaselineSource(s *kernel.SemanticSession, cpRef string, cp map[string]any) (map[string]any, []string, error) {
	if err := s.ValidateSchema(".template-spec/process/schemas/lifecycle-checkpoint-v2.schema.json", cp); err != nil {
		return nil, nil, err
	}
	specRef := CheckpointAsset(cp, "spec_ref", "artifact.spec")
	for _, gate := range []string{"gate.plan-approved", "gate.spec-baseline-approved"} {
		asset := ""
		if gate == "gate.spec-baseline-approved" {
			asset = specRef
		}
		if err := approval.VerifyCheckpointStage(s, cpRef, gate, asset); err != nil {
			return nil, nil, err
		}
	}
	if specRef == "" {
		return nil, nil, s.Reject("SPEC_BASELINE_CURRENT", "缺少当前 Spec 资产引用")
	}
	reconciliation := kernel.ApMap(cp["context_reconciliation"])
	if reconciliation["status"] != "reconciled" || kernel.Text(reconciliation["ref"]) == "" {
		return nil, nil, s.Reject("SPEC_BASELINE_CURRENT", "阶段退出需要当前 Context 对账")
	}
	if err := s.Verify("context-reconciliation", kernel.Text(reconciliation["ref"]), nil); err != nil {
		return nil, nil, err
	}
	refs := []string{cpRef}
	planRef := CheckpointAsset(cp, "plan_ref", "artifact.plan")
	strategyRef := CheckpointAsset(cp, "domain_strategy_ref", "artifact.domain-strategy")
	stageRef := CheckpointAsset(cp, "stage_decision_package_ref", "artifact.stage-decision-package")
	ticketsRef := CheckpointAsset(cp, "business_ticket_set_ref", "artifact.business-ticket-set")
	for _, ref := range []string{planRef, strategyRef, stageRef} {
		if ref != "" {
			if err := approval.VerifyCheckpointStage(s, cpRef, "gate.plan-approved", ref); err != nil {
				return nil, nil, err
			}
			refs = append(refs, ref)
		}
	}
	if ticketsRef == "" {
		return nil, nil, s.Reject("SPEC_BASELINE", "缺少当前业务票草案")
	}
	business, err := dependencies.ContractBusinessTicketsMode(s, ticketsRef, "draft")
	if err != nil {
		return nil, nil, err
	}
	if kernel.ApMap(business["spec"])["ref"] != specRef {
		return nil, nil, s.Reject("SPEC_BASELINE_SCOPE", "业务票未绑定当前 Spec")
	}
	if err = approval.VerifyCheckpointStage(s, cpRef, "gate.spec-baseline-approved", ticketsRef); err != nil {
		return nil, nil, err
	}
	productDesign := cp["delivery_impacts"] != nil && kernel.ApMap(cp["delivery_impacts"])["ui"] == true
	if stageRef != "" {
		stage, e := s.Doc(stageRef)
		if e != nil {
			return nil, nil, e
		}
		productDesign = kernel.ApMap(stage["impact_assessment"])["ui"] == true
	}
	raw, err := s.Bytes(specRef)
	if err != nil {
		return nil, nil, err
	}
	cpRaw, err := s.Bytes(cpRef)
	if err != nil {
		return nil, nil, err
	}
	metadata, err := s.Doc(domain.MetadataFile)
	if err != nil {
		return nil, nil, err
	}
	source := map[string]any{"profile_id": "harness.spec-template", "feature_id": cp["feature_id"], "checkpoint_ref": cpRef, "checkpoint_digest": "sha256:" + safefs.Digest(cpRaw), "template_commit": metadata["templateCommit"], "spec_ref": specRef, "spec_digest": "sha256:" + safefs.Digest(raw), "plan_ref": planRef, "domain_strategy_ref": strategyRef, "stage_decision_package_ref": stageRef, "business_ticket_set_ref": ticketsRef, "product_design_required": productDesign}
	refs = append(refs, specRef, ticketsRef)
	return source, refs, nil
}
