package governance

import "strings"

// This selects a policy path only. All current feature and approval facts are
// revalidated by contractLocalFrontendInputs at an actual consumption seam.
func hasLocalImplementationInputs(s *semanticSession) (bool, error) {
	identity, err := s.doc("yss-project.yaml")
	if err != nil {
		return false, err
	}
	if identity["repository_mode"] != "project-instance" {
		return false, nil
	}
	for _, ref := range []string{".yss.json", ".template-spec/process/harness-profile.yaml", guidanceContractRef("spec")} {
		present, err := s.exists(ref)
		if err != nil {
			return false, err
		}
		if !present {
			return false, nil
		}
	}
	profile, err := s.doc(".template-spec/process/harness-profile.yaml")
	if err != nil {
		return false, err
	}
	metadata, err := s.doc(".yss.json")
	if err != nil {
		return false, err
	}
	if metadata["profile"] != "spec" || metadata["profileId"] != "harness.spec-template" {
		if profile["profile_id"] == "harness.spec-template" {
			return false, s.reject("IDENTITY", "本机Spec身份与Harness Profile不一致")
		}
		return false, nil
	}
	if profile["profile_id"] != "harness.spec-template" {
		return false, s.reject("IDENTITY", "本机Spec身份与Harness Profile不一致")
	}
	if err = projectIdentity(s.v); err != nil {
		return false, err
	}
	contract, err := s.doc(guidanceContractRef("spec"))
	if err != nil {
		return false, err
	}
	policy := semMap(contract["progression_target"])
	if policy["local_implementation_inputs"] == nil {
		return false, nil
	}
	if contractN(contract["schema_version"]) != 1 || contractN(policy["schema_version"]) != 1 || policy["config_file"] != progressionFile || !semSameSet(policy["required_capabilities"], []any{"lifecycle-target-v1"}) || !semHas(policy["writer_profiles"], "spec") {
		return false, s.unavailable("CAPABILITY", "已声明本地实现输入，但政策版本或能力不受支持")
	}
	if policy["local_implementation_inputs"] != "native-spec-current-feature-approved-assets" {
		return false, s.unavailable("CAPABILITY", "未知本地实现输入政策")
	}
	return true, nil
}

// Local inputs are the same feature's current approved assets. There is no
// receipt, implicit import, persisted qualification or execution authorization.
func contractLocalFrontendInputs(s *semanticSession, c *nativeSlice, opts map[string]string) error {
	phase := first(opts["phase"], "inputs")
	if !semHas([]string{"preflight", "design", "contract", "inputs", "implementation", "verification"}, phase) {
		return s.reject("FRONTEND_PHASE", "未知本地前端输入阶段")
	}
	profile, policy, err := progressionPolicy(s)
	if err != nil {
		return err
	}
	if profile != "spec" || policy["local_implementation_inputs"] != "native-spec-current-feature-approved-assets" {
		return s.unavailable("CAPABILITY", "当前工程未授权本地前端批准资产输入")
	}
	if err = projectIdentity(s.v); err != nil {
		return err
	}
	cpRef := first(opts["checkpoint"], s.checkpointRef)
	configRef, cp, err := progressionLocation(s, cpRef)
	if err != nil {
		return err
	}
	if blockers, ok := cp["blockers"].([]any); !ok || len(blockers) != 0 {
		return s.reject("FRONTEND_INPUTS", "当前功能存在阻断或缺少阻断事实")
	}
	intent := ProgressionTarget{FeatureID: text(cp["feature_id"]), CheckpointRef: cpRef, Consumers: []ProgressionConsumer{}}
	if present, e := s.exists(configRef); e != nil {
		return e
	} else if present {
		raw, e := s.bytes(configRef)
		if e != nil {
			return e
		}
		intent, err = parseProgressionTarget(raw)
		if err != nil {
			return err
		}
		if intent.FeatureID != text(cp["feature_id"]) || intent.CheckpointRef != cpRef {
			return s.reject("FRONTEND_BINDING", "本地前端意图未绑定当前功能")
		}
	}
	for _, consumer := range intent.Consumers {
		if consumer.Profile == "frontend" {
			return s.reject("FRONTEND_BINDING", "显式专职前端承接不得回退本地输入")
		}
	}
	coordination, participants := progressionConsumers(s, profile, cpRef, cp, intent)
	source, _, err := verifySpecBaselineSource(s, cpRef)
	if err != nil {
		return err
	}
	if source["product_design_required"] == true {
		if design := participants["design"]; design != nil {
			if semMap(coordination["design"])["input_status"] != "verified" {
				return s.reject("FRONTEND_INPUTS", "显式Design尚未核验当前来源接收")
			}
			designCP, e := design.doc(design.checkpointRef)
			if e != nil {
				return e
			}
			_, designPolicy, e := progressionPolicy(design)
			if e != nil {
				return e
			}
			status, reason := progressionMilestone(design, design.checkpointRef, designCP, "product-design-completed", semMap(semMap(designPolicy["completion_policy"])["product-design-completed"]), intent)
			if status != "reached" {
				return s.reject("FRONTEND_INPUTS", "当前适用产品设计尚未完成："+reason)
			}
		} else {
			for _, consumer := range intent.Consumers {
				if consumer.Profile == "design" {
					return s.reject("FRONTEND_INPUTS", "等待显式Design当前功能批准资产")
				}
			}
			if err = s.gateChecks(cp, cpRef, "gate.product-design-approved"); err != nil {
				return err
			}
		}
	}
	stage, err := s.doc(text(source["stage_decision_package_ref"]))
	if err != nil {
		return err
	}
	impact := semMap(stage["impact_assessment"])
	for _, key := range []string{"frontend", "backend", "api", "data"} {
		if _, ok := impact[key].(bool); !ok {
			return s.reject("FRONTEND_APPLICABILITY", "当前批准影响评估未明确: "+key)
		}
		if semMap(cp["delivery_impacts"])[key] == true && impact[key] == false {
			return s.reject("FRONTEND_APPLICABILITY", "当前功能实现影响与批准范围冲突: "+key)
		}
	}
	if impact["frontend"] != true {
		return s.reject("FRONTEND_APPLICABILITY", "当前批准范围不包含前端实现")
	}
	sliceRef, sliceID := progressionSliceRef(cp), opts["slice"]
	if c == nil && sliceRef != "" {
		c, err = loadNativeSlice(s, sliceRef)
		if err != nil {
			return err
		}
	}
	if c != nil && opts["unit"] != "" {
		unit := apFind(c.Normalized["work_units"], "id", opts["unit"])
		if unit == nil || unit["role_id"] != "role.frontend-engineer" {
			return s.reject("FRONTEND_BINDING", "前端输入须显式选择批准的前端工作单元")
		}
		c, err = contractSelectLocalUnit(s, c, opts["unit"])
		if err != nil {
			return err
		}
	}
	if c != nil {
		if len(c.Repositories) != 0 || c.Ref != sliceRef {
			return s.reject("FRONTEND_BINDING", "本地输入须为同功能当前登记的Slice")
		}
		if c.Basis["frontend_delivery"] != nil || len(semMap(semMap(c.Normalized["resolution"])["frontend_delivery"])) != 0 || len(semMap(semMap(c.Normalized["frontend"])["delivery"])) != 0 {
			return s.reject("FRONTEND_BINDING", "已有外部前端接收绑定不得回退本地输入")
		}
		if sliceID != "" && sliceID != text(c.Raw["slice_id"]) {
			return s.reject("FRONTEND_BINDING", "显式切片与当前功能批准资产冲突")
		}
		sliceID = text(c.Raw["slice_id"])
		if semMap(c.Normalized["frontend"])["status"] != "required" {
			return s.reject("FRONTEND_APPLICABILITY", "当前Slice未声明本地前端职责")
		}
		spec := c.Basis["spec"]
		if spec["ref"] != source["spec_ref"] || strings.TrimPrefix(text(spec["digest"]), "sha256:") != strings.TrimPrefix(text(source["spec_digest"]), "sha256:") {
			return s.reject("FRONTEND_BINDING", "当前Slice未消费同功能批准Spec")
		}
	} else if !semHas([]string{"preflight", "design", "contract"}, phase) {
		return s.reject("FRONTEND_BINDING", "实现输入缺少当前登记的批准Slice")
	}
	if sliceID == "" && !semHas([]string{"preflight", "design", "contract"}, phase) {
		return s.reject("FRONTEND_BINDING", "本地前端输入需要明确切片身份")
	}
	if !semHas([]string{"preflight", "design", "contract"}, phase) {
		gate := semMap(semMap(cp["gates"])["gate.slice-contract-approved"])
		if gate["subject_ref"] != c.Ref {
			return s.reject("FRONTEND_BINDING", "当前功能门禁未批准同一Slice")
		}
		approval := first(text(gate["approval_ref"]), cpRef)
		if err = contractSliceApproval(s, c, map[string]string{"approval-ref": approval}); err != nil {
			return err
		}
		if err = s.gateChecks(cp, cpRef, "gate.slice-contract-approved"); err != nil {
			return err
		}
		backendRequired := impact["backend"] == true || impact["api"] == true || impact["data"] == true
		if backendRequired {
			if err = contractLocalFrontendBackend(s, cpRef, c, intent, coordination, participants); err != nil {
				return err
			}
		} else {
			backendStatus := semMap(c.Raw["backend"])["status"]
			if contractN(c.Raw["schema_version"]) == 3 {
				backendStatus = semMap(semMap(c.Raw["applicability"])["backend"])["status"]
			}
			apiNotApplicable := semMap(semMap(c.Raw["applicability"])["api"])["status"] == "not-applicable"
			if contractN(c.Raw["schema_version"]) == 2 {
				apiNotApplicable = semMap(c.Raw["contract"])["api_impact"] == false
			}
			if backendStatus != "not-applicable" || !apiNotApplicable {
				return s.reject("FRONTEND_APPLICABILITY", "后端不适用与当前Slice范围冲突")
			}
			na, err := contractBoundDoc(s, c.Basis["no_api_impact_record"])
			if err != nil {
				return err
			}
			if na["status"] != "not-applicable" || na["slice_id"] != c.Raw["slice_id"] {
				return s.reject("FRONTEND_APPLICABILITY", "无API依据未绑定当前Slice")
			}
		}
	}
	var currentSliceID any
	var currentSliceRef any
	if sliceID != "" {
		currentSliceID = sliceID
	}
	if c != nil {
		currentSliceRef = c.Ref
	}
	s.report.Coverage = map[string]any{"result": "inputs-verified", "ready_for_agent": false, "delivery_mode": "local-approved-assets", "root": s.root, "checkpoint_ref": cpRef, "spec_ref": source["spec_ref"], "slice_contract_ref": currentSliceRef, "slice_id": currentSliceID, "phase": phase, "inputs_current": false}
	return nil
}

// An explicit backend participant is authoritative; unavailable or mismatched
// external evidence cannot be replaced by a local record from the same root.
func contractLocalFrontendBackend(s *semanticSession, cpRef string, c *nativeSlice, intent ProgressionTarget, coordination map[string]any, participants map[string]*semanticSession) error {
	declared := false
	for _, consumer := range intent.Consumers {
		declared = declared || consumer.Profile == "backend"
	}
	if !declared {
		return backendBusinessLocalEvidence(s, cpRef)
	}
	backend := participants["backend"]
	row := semMap(coordination["backend"])
	if backend == nil || row["input_status"] != "verified" || row["completion_status"] != "reached" {
		return s.reject("FRONTEND_INPUTS", "显式后端当前来源、完整交付及本端验收尚未核验")
	}
	a, err := progressionBackendAuthorization(backend, backend.checkpointRef, "", true)
	if err != nil {
		return err
	}
	terminal, err := backend.doc(a.TerminalRef)
	if err != nil {
		return err
	}
	delivery, err := backendInspectDelivery(backend, text(semMap(terminal["delivery"])["ref"]), map[string]string{"checkpoint": backend.checkpointRef})
	if err != nil {
		return err
	}
	current, err := loadNativeSlice(backend, text(semMap(delivery["slice_contract"])["ref"]))
	if err != nil {
		return err
	}
	if semMap(delivery["scope"])["slice_id"] != c.Raw["slice_id"] {
		return s.reject("FRONTEND_BINDING", "显式后端交付与当前前端切片不同")
	}
	specDigest := strings.TrimPrefix(text(c.Basis["spec"]["digest"]), "sha256:")
	if specDigest == "" || strings.TrimPrefix(text(current.Basis["spec"]["digest"]), "sha256:") != specDigest {
		return s.reject("FRONTEND_BINDING", "显式后端当前Slice未消费同一批准Spec")
	}
	if api := c.Basis["openapi_freeze"]; api != nil {
		if strings.TrimPrefix(text(api["digest"]), "sha256:") != strings.TrimPrefix(text(semMap(delivery["openapi"])["digest"]), "sha256:") {
			return s.reject("FRONTEND_BINDING", "前端当前冻结接口与显式后端交付不同")
		}
	} else {
		na, err := contractBoundDoc(s, c.Basis["no_api_impact_record"])
		if err != nil {
			return err
		}
		if na["status"] != "not-applicable" || na["slice_id"] != c.Raw["slice_id"] || semMap(delivery["openapi"])["mode"] != "not-applicable" {
			return s.reject("FRONTEND_BINDING", "无API依据与当前后端交付冲突")
		}
	}
	return nil
}
