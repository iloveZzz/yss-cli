package contracts

import (
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/governance/approval"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"strings"
)

func init() {
	kernel.RegisterRoleIndependentValidator("stage-review-provenance", func(s *kernel.SemanticSession, ref string, _ map[string]string) error {
		record, e := s.Doc(ref)
		if e != nil {
			return e
		}
		taskRef := kernel.Text(kernel.ApMap(record["provenance"])["task_ref"])
		if e = s.Verify("task", taskRef, nil); e != nil {
			return e
		}
		task, e := s.Doc(taskRef)
		if e != nil {
			return e
		}
		if task["workflow_status"] != "resolved" {
			return s.Reject("STAGE_REVIEW", "阶段执行任务尚未完成")
		}
		source := kernel.ApMap(s.Report.Coverage["task_execution"])
		implementation := kernel.ApMap(source["provenance"])
		candidate := kernel.ApMap(source["candidate"])
		scope := source["scope"]
		requiredBindings := append([]any{}, kernel.ApArray(task["input_bindings"])...)
		if kernel.SemHas([]string{"Reviewer", "Verifier"}, kernel.Text(task["execution_state"])) {
			source, e := TrustedTaskImplementation(s, kernel.ApMap(task["review_context"]))
			if e != nil {
				return e
			}
			implementation = kernel.ApMap(source["provenance"])
			candidate = kernel.ApMap(kernel.ApMap(task["review_context"])["candidate"])
			scope = kernel.ApMap(task["review_context"])["approval_scope"]
			implementationTask, e := s.Doc(kernel.Text(source["task_ref"]))
			if e != nil {
				return e
			}
			requiredBindings = append(requiredBindings, kernel.ApArray(implementationTask["input_bindings"])...)
		}
		kernel.SemanticCoverage(s, "stage_review_provenance", map[string]any{"implementation": implementation, "candidate": candidate, "scope": scope, "required_bindings": requiredBindings})
		return nil
	})
	kernel.RegisterRoleIndependentValidator("backend-preparation-inputs", func(s *kernel.SemanticSession, ref string, opts map[string]string) error {
		if !s.StageGatesEnabled() {
			return s.Reject("CAPABILITY", "准备输入需要四阶段政策")
		}
		doc, e := s.Doc(ref)
		if e != nil {
			return e
		}
		return contractBackendPrerequisites(s, kernel.ApMap(doc["design_prerequisites"]), nil, opts)
	})
	kernel.RegisterRoleIndependentValidator("stage-spec-inputs", func(s *kernel.SemanticSession, ref string, opts map[string]string) error {
		record, e := s.Doc(ref)
		if e != nil {
			return e
		}
		sets := 0
		for _, asset := range kernel.ApRows(record["assets"]) {
			name := kernel.Text(asset["ref"])
			doc, e := s.Doc(name)
			if e != nil {
				continue
			}
			if doc["kind"] != "business-ticket-set" {
				continue
			}
			sets++
			spec := kernel.ApMap(doc["spec"])
			specMember := false
			for _, member := range kernel.ApRows(record["assets"]) {
				specMember = specMember || kernel.Text(member["ref"]) == kernel.Text(spec["ref"]) && strings.TrimPrefix(kernel.Text(member["digest"]), "sha256:") == strings.TrimPrefix(kernel.Text(spec["digest"]), "sha256:")
			}
			if !specMember {
				return s.Reject("STAGE_SPEC", "Spec 基线缺少业务票集合绑定的当前 Spec 成果")
			}
			for _, check := range kernel.ApRows(record["checks"]) {
				if check["id"] == "check.artifact-consistency-analyzed" {
					for _, required := range []string{name, kernel.Text(spec["ref"])} {
						found := false
						for _, input := range kernel.ApRows(check["inputs"]) {
							found = found || input["ref"] == required
						}
						if !found {
							return s.Reject("STAGE_CHECK_SCOPE", "一致性检查未覆盖当前 Spec 与业务票集合")
						}
					}
				}
			}
			if _, e = ContractBusinessTicketsMode(s, name, "draft"); e != nil {
				return e
			}
		}
		if sets != 1 {
			return s.Reject("STAGE_SPEC", "Spec 基线需要唯一当前 Spec 与业务票集合")
		}

		return s.Verify("stage-prototype-inputs", ref, nil)
	})
	kernel.RegisterRoleIndependentValidator("stage-delivery-inputs", verifyStageDeliveryInputs)

	kernel.RegisterRoleIndependentValidator("stage-admission-inputs", VerifyStageAdmissionInputs)
}

// The public implementation boundary consumes the current immutable result.
func VerifyStageAdmission(s *kernel.SemanticSession, checkpointRef, sliceRef string) error {
	if !s.StageGatesEnabled() {
		return s.Reject("CAPABILITY", "实施阶段准入需要显式 Spec stage-gates-v1")
	}
	return approval.VerifyCheckpointStage(s, checkpointRef, "gate.implementation-admitted", sliceRef)
}

// Pure input verification is also used before the result exists. It never
// consumes its own admission and cannot confer implementation authority.
func VerifyStageAdmissionInputs(s *kernel.SemanticSession, recordRef string, opts map[string]string) error {
	if !s.StageGatesEnabled() {
		return s.Reject("CAPABILITY", "阶段输入政策未激活")
	}
	record, err := s.Doc(recordRef)
	if err != nil {
		return err
	}
	if record["gate_id"] != "gate.implementation-admitted" {
		return s.Reject("STAGE_ADMISSION", "输入不是实施准入阶段")
	}
	auth := kernel.ApMap(record["authorization"])
	if auth["kind"] != "stage" {
		return s.Reject("STAGE_ADMISSION", "自动准入必须绑定已批准的 Spec 基线")
	}
	parent, err := s.Doc(kernel.Text(auth["ref"]))
	if err != nil {
		return err
	}
	if cpRef := opts["checkpoint"]; cpRef != "" {
		if err = approval.VerifyCheckpointStage(s, cpRef, "gate.spec-baseline-approved", ""); err != nil {
			return err
		}
		cp, e := s.Doc(cpRef)
		if e != nil {
			return e
		}
		current := kernel.Text(kernel.ApMap(kernel.ApMap(cp["gates"])["gate.spec-baseline-approved"])["record_ref"])
		av, ar, ae := s.ReferenceView(kernel.Text(auth["ref"]))
		cv, cr, ce := s.ReferenceView(current)
		if ae != nil || ce != nil || av != cv || ar != cr {
			return s.Reject("STAGE_SCOPE", "准入未绑定当前 checkpoint 的 Spec 基线")
		}
	}
	found := false
	for _, member := range kernel.ApRows(record["assets"]) {
		ref := kernel.Text(member["ref"])
		raw, e := s.Doc(ref)
		if e != nil {
			continue
		}
		if raw["slice_contract"] != nil {
			raw = kernel.ApMap(raw["slice_contract"])
		}
		if raw["slice_id"] == nil {
			continue
		}
		found = true
		if kernel.ContractN(raw["schema_version"]) != 4 {
			return s.Reject("STAGE_ADMISSION", "新准入只消费 Slice4")
		}
		if e = verifySliceDraft(s, ref, nil); e != nil {
			return e
		}
		c, e := LoadNativeSlice(s, ref)
		if e != nil {
			return e
		}
		spec := c.Basis["spec"]
		covered := false
		for _, asset := range kernel.ApRows(parent["assets"]) {
			av, ar, ae := s.ReferenceView(kernel.Text(asset["ref"]))
			sv, sr, se := s.ReferenceView(kernel.Text(spec["ref"]))
			covered = covered || (ae == nil && se == nil && av == sv && ar == sr && strings.TrimPrefix(kernel.Text(asset["digest"]), "sha256:") == strings.TrimPrefix(kernel.Text(spec["digest"]), "sha256:"))
		}
		if !covered {
			return s.Reject("STAGE_SCOPE", "实施合同不属于当前 Spec 基线")
		}
		// Existing normalization checks the actual API Freeze/no-impact choice,
		// repository bindings, acceptance locators, and every basis byte.
		if e = contractSliceFreshInputs(s, c, nil); e != nil {
			return e
		}
	}
	if !found {
		return s.Reject("STAGE_ADMISSION", "准入成果缺少已经形成的 Slice4")
	}
	return nil
}

// Delivery requires actual validated execution reports and current admission;
// the original user's acceptance remains checked by stage-result itself.
func verifyStageDeliveryInputs(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	r, err := s.Doc(ref)
	if err != nil {
		return err
	}
	admitted, verified := false, false
	admittedSlices, verifiedSlices := map[string]bool{}, map[string]bool{}
	key := func(ref string) string {
		v, local, e := s.ReferenceView(ref)
		if e != nil {
			return ""
		}
		return v.Root + "\x00" + local
	}
	for _, asset := range kernel.ApRows(r["assets"]) {
		name := kernel.Text(asset["ref"])
		doc, e := s.Doc(name)
		if e != nil {
			continue
		}
		if doc["kind"] == "stage-result" && doc["gate_id"] == "gate.implementation-admitted" {
			if doc["feature_id"] != r["feature_id"] || !kernel.ApSetEqual(doc["scope"], r["scope"]) {
				return s.Reject("STAGE_SCOPE", "交付与实施范围不一致")
			}
			if cpRef := opts["checkpoint"]; cpRef != "" {
				if e = approval.VerifyCheckpointStage(s, cpRef, "gate.implementation-admitted", ""); e != nil {
					return e
				}
				cp, e := s.Doc(cpRef)
				if e != nil {
					return e
				}
				current := kernel.Text(kernel.ApMap(kernel.ApMap(cp["gates"])["gate.implementation-admitted"])["record_ref"])
				av, ar, ae := s.ReferenceView(name)
				cv, cr, ce := s.ReferenceView(current)
				if ae != nil || ce != nil || av != cv || ar != cr {
					return s.Reject("STAGE_SCOPE", "交付未绑定当前实施准入")
				}
			}
			if e = s.Verify("stage-result", name, map[string]string{"gate": "gate.implementation-admitted"}); e != nil {
				return e
			}
			for _, member := range kernel.ApRows(doc["assets"]) {
				memberRef := kernel.Text(member["ref"])
				candidate, e := s.Doc(memberRef)
				if e != nil {
					return e
				}
				if nested := kernel.ApMap(candidate["slice_contract"]); nested != nil {
					candidate = nested
				}
				if candidate["slice_id"] != nil {
					admittedSlices[key(memberRef)] = true
				}
			}
			admitted = true
		}
		report := doc
		if nested := kernel.ApMap(doc["execution_result"]); nested != nil {
			report = nested
		}
		if report["consumed_contract"] != nil || report["kind"] == "backend-contract" || report["kind"] == "backend-deployment" {
			bound := map[string]string{}
			for k, v := range opts {
				bound[k] = v
			}
			bound["task"] = kernel.First(kernel.Text(doc["task_ref"]), kernel.Text(kernel.ApMap(r["provenance"])["task_ref"]))
			if bound["task"] != "" {
				task, err := s.Doc(bound["task"])
				if err != nil {
					return err
				}
				bound["contract"] = kernel.Text(kernel.ApMap(task["contract"])["slice_contract_ref"])
			}
			if e = s.Verify("verification", name, bound); e != nil {
				return e
			}
			if report["consumed_contract"] != nil && bound["contract"] != "" {
				verifiedSlices[key(bound["contract"])] = true
				verified = true
			}
		}
	}
	if len(admittedSlices) == 0 {
		return s.Reject("STAGE_DELIVERY", "交付缺少当前已准入切片")
	}
	for slice := range admittedSlices {
		if !verifiedSlices[slice] {
			return s.Reject("STAGE_DELIVERY", "仍有已准入切片缺少当前验收执行报告")
		}
	}
	for slice := range verifiedSlices {
		if !admittedSlices[slice] {
			return s.Reject("STAGE_SCOPE", "验收执行报告属于其他准入范围")
		}
	}
	if !admitted || !verified {
		return s.Reject("STAGE_DELIVERY", "交付需要当前实施准入和实际通过的验收执行报告")
	}
	return nil
}

// Preparation consumes existing stage authority or a verified technical-detail
// continuation. It never consumes future implementation admission.
func verifyStagePreparationAuthorization(s *kernel.SemanticSession, ref, subject, checkpoint string) error {
	if ref == "" || subject == "" {
		return s.Reject("STAGE_PREPARATION", "准备工作缺少已有阶段授权或有效延续")
	}
	authority, e := s.Doc(ref)
	if e != nil {
		return e
	}
	if authority["kind"] == "stage-result" {
		gate := kernel.Text(authority["gate_id"])
		if gate != "gate.plan-approved" && gate != "gate.spec-baseline-approved" || checkpoint == "" {
			return s.Reject("STAGE_PREPARATION", "准备授权需要当前 Plan/Spec 阶段及 checkpoint")
		}
		if e = approval.VerifyCheckpointStage(s, checkpoint, gate, subject); e != nil {
			return e
		}
		cp, e := s.Doc(checkpoint)
		if e != nil {
			return e
		}
		current := kernel.Text(kernel.ApMap(kernel.ApMap(cp["gates"])[gate])["record_ref"])
		av, ar, ae := s.ReferenceView(ref)
		cv, cr, ce := s.ReferenceView(current)
		if ae != nil || ce != nil || av != cv || ar != cr {
			return s.Reject("STAGE_PREPARATION", "准备授权不是当前阶段来源")
		}
		return nil
	}
	if authority["kind"] == "approved-scope-continuation-v2" {
		if authority["boundary"] != "gate.spec-baseline-approved" {
			return s.Reject("STAGE_PREPARATION", "准备延续必须属于 Spec 技术细化范围")
		}
		_, e = approval.AssertUserDecisionRequirementSemantic(s, map[string]any{"boundary": "gate.spec-baseline-approved", "subject_ref": subject, "scope": authority["scope"], "continuation_ref": ref})
		return e
	}
	return s.Reject("STAGE_PREPARATION", "准备来源不是可复用的阶段或延续授权")
}

// Normalizing a preparation snapshot must not mutate cached current documents.
func stageSnapshotCopy(doc map[string]any) (map[string]any, error) {
	raw, e := json.Marshal(doc)
	if e != nil {
		return nil, e
	}
	copy := map[string]any{}
	e = json.Unmarshal(raw, &copy)
	return copy, e
}
