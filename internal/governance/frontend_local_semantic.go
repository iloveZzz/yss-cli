package governance

import (
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"path/filepath"
	"strings"
)

// This selects a policy path only. All current feature and approval facts are
// revalidated by contractLocalFrontendInputs at an actual consumption seam.
func hasLocalImplementationInputs(s *kernel.SemanticSession) (bool, error) {
	identity, err := s.Doc("yss-project.yaml")
	if err != nil {
		return false, err
	}
	if identity["repository_mode"] != "project-instance" {
		return false, nil
	}
	present, err := s.Exists(".yss.json")
	if err != nil || !present {
		return false, err
	}
	metadata, err := s.Doc(".yss.json")
	if err != nil {
		return false, err
	}
	family := kernel.Text(metadata["profile"])
	if !kernel.SemHas([]string{"spec", "frontend"}, family) {
		return false, nil
	}
	profile, err := s.Doc(".template-spec/process/harness-profile.yaml")
	if err != nil {
		return false, err
	}
	if metadata["profileId"] != domain.Profiles[family].ID || profile["profile_id"] != domain.Profiles[family].ID {
		return false, s.Reject("IDENTITY", "本地业务输入 Profile 身份不一致")
	}
	if err = kernel.ProjectIdentity(s.V); err != nil {
		return false, err
	}
	contract, err := s.Doc(kernel.GuidanceContractRef(family))
	if err != nil {
		return false, err
	}
	policy := kernel.SemMap(contract["progression_target"])
	if policy["local_implementation_inputs"] == nil {
		return false, nil
	}
	if kernel.ContractN(contract["schema_version"]) != 1 || kernel.ContractN(policy["schema_version"]) != 1 || policy["config_file"] != kernel.ProgressionFile || !semSameSet(policy["required_capabilities"], []any{"lifecycle-target-v1"}) || (family == "spec" && !kernel.SemHas(policy["writer_profiles"], "spec")) {
		return false, s.Unavailable("CAPABILITY", "已声明本地实现输入，但政策版本或能力不受支持")
	}
	if !((family == "spec" && policy["local_implementation_inputs"] == "native-spec-current-feature-approved-assets") || (family == "frontend" && policy["local_implementation_inputs"] == "native-profile-current-feature-approved-assets")) {
		return false, s.Unavailable("CAPABILITY", "未知本地实现输入政策")
	}
	return true, nil
}

// Local inputs are the same feature's current approved assets. There is no
// receipt, implicit import, persisted qualification or execution authorization.
func contractLocalFrontendInputs(s *kernel.SemanticSession, c *nativeSlice, opts map[string]string) error {
	phase := kernel.First(opts["phase"], "inputs")
	if !kernel.SemHas([]string{"preflight", "design", "contract", "inputs", "implementation", "verification"}, phase) {
		return s.Reject("FRONTEND_PHASE", "未知本地前端输入阶段")
	}
	profile, policy, err := kernel.ProgressionPolicy(s)
	if err != nil {
		return err
	}
	if !((profile == "spec" && policy["local_implementation_inputs"] == "native-spec-current-feature-approved-assets") || (profile == "frontend" && policy["local_implementation_inputs"] == "native-profile-current-feature-approved-assets")) {
		return s.Unavailable("CAPABILITY", "当前工程未授权本地前端批准资产输入")
	}
	if err = kernel.ProjectIdentity(s.V); err != nil {
		return err
	}
	cpRef := kernel.First(opts["checkpoint"], s.CheckpointRef)
	configRef, cp, err := kernel.ProgressionLocation(s, cpRef)
	if err != nil {
		return err
	}
	if blockers, ok := cp["blockers"].([]any); !ok || len(blockers) != 0 {
		return s.Reject("FRONTEND_INPUTS", "当前功能存在阻断或缺少阻断事实")
	}
	intent := ProgressionTarget{FeatureID: kernel.Text(cp["feature_id"]), CheckpointRef: cpRef, Consumers: []ProgressionConsumer{}}
	if present, e := s.Exists(configRef); e != nil {
		return e
	} else if present {
		raw, e := s.Bytes(configRef)
		if e != nil {
			return e
		}
		intent, err = parseProgressionTarget(raw)
		if err != nil {
			return err
		}
		if intent.FeatureID != kernel.Text(cp["feature_id"]) || intent.CheckpointRef != cpRef {
			return s.Reject("FRONTEND_BINDING", "本地前端意图未绑定当前功能")
		}
	}
	for _, consumer := range intent.Consumers {
		if consumer.Profile == "frontend" {
			return s.Reject("FRONTEND_BINDING", "显式专职前端承接不得回退本地输入")
		}
	}
	coordination, participants := progressionConsumers(s, profile, cpRef, cp, intent)
	source, _, err := verifySpecBaselineSource(s, cpRef, profile == "frontend")
	if err != nil {
		return err
	}
	if source["product_design_required"] == true {
		if design := participants["design"]; design != nil {
			if kernel.SemMap(coordination["design"])["input_status"] != "verified" {
				return s.Reject("FRONTEND_INPUTS", "显式Design尚未核验当前来源接收")
			}
			designCP, e := design.Doc(design.CheckpointRef)
			if e != nil {
				return e
			}
			_, designPolicy, e := kernel.ProgressionPolicy(design)
			if e != nil {
				return e
			}
			status, reason := progressionMilestone(design, design.CheckpointRef, designCP, "product-design-completed", kernel.SemMap(kernel.SemMap(designPolicy["completion_policy"])["product-design-completed"]), intent)
			if status != "reached" {
				return s.Reject("FRONTEND_INPUTS", "当前适用产品设计尚未完成："+reason)
			}
		} else {
			for _, consumer := range intent.Consumers {
				if consumer.Profile == "design" {
					return s.Reject("FRONTEND_INPUTS", "等待显式Design当前功能批准资产")
				}
			}
			if err = s.GateChecks(cp, cpRef, "gate.product-design-approved"); err != nil {
				return err
			}
		}
	}
	stage, err := s.Doc(kernel.Text(source["stage_decision_package_ref"]))
	if err != nil {
		return err
	}
	impact := kernel.SemMap(stage["impact_assessment"])
	for _, key := range []string{"frontend", "backend", "api", "data"} {
		if _, ok := impact[key].(bool); !ok {
			return s.Reject("FRONTEND_APPLICABILITY", "当前批准影响评估未明确: "+key)
		}
		if kernel.SemMap(cp["delivery_impacts"])[key] == true && impact[key] == false {
			return s.Reject("FRONTEND_APPLICABILITY", "当前功能实现影响与批准范围冲突: "+key)
		}
	}
	if impact["frontend"] != true {
		return s.Reject("FRONTEND_APPLICABILITY", "当前批准范围不包含前端实现")
	}
	sliceRef, sliceID := progressionSliceRef(cp), opts["slice"]
	if c == nil && sliceRef != "" {
		c, err = loadNativeSlice(s, sliceRef)
		if err != nil {
			return err
		}
	}
	if c != nil && opts["unit"] != "" {
		unit := kernel.ApFind(c.Normalized["work_units"], "id", opts["unit"])
		if unit == nil || !kernel.SemHas([]string{"role.frontend-engineer", "role.frontend-agent"}, kernel.Text(unit["role_id"])) {
			return s.Reject("FRONTEND_BINDING", "前端输入须显式选择批准的前端工作单元")
		}
		c, err = contractSelectLocalUnit(s, c, opts["unit"])
		if err != nil {
			return err
		}
	}
	if c != nil {
		if len(c.Repositories) != 0 || c.Ref != sliceRef {
			return s.Reject("FRONTEND_BINDING", "本地输入须为同功能当前登记的Slice")
		}
		if c.Basis["frontend_delivery"] != nil || len(kernel.SemMap(kernel.SemMap(c.Normalized["resolution"])["frontend_delivery"])) != 0 || len(kernel.SemMap(kernel.SemMap(c.Normalized["frontend"])["delivery"])) != 0 {
			return s.Reject("FRONTEND_BINDING", "已有外部前端接收绑定不得回退本地输入")
		}
		if sliceID != "" && sliceID != kernel.Text(c.Raw["slice_id"]) {
			return s.Reject("FRONTEND_BINDING", "显式切片与当前功能批准资产冲突")
		}
		sliceID = kernel.Text(c.Raw["slice_id"])
		if kernel.SemMap(c.Normalized["frontend"])["status"] != "required" {
			return s.Reject("FRONTEND_APPLICABILITY", "当前Slice未声明本地前端职责")
		}
		spec := c.Basis["spec"]
		if spec["ref"] != source["spec_ref"] || strings.TrimPrefix(kernel.Text(spec["digest"]), "sha256:") != strings.TrimPrefix(kernel.Text(source["spec_digest"]), "sha256:") {
			return s.Reject("FRONTEND_BINDING", "当前Slice未消费同功能批准Spec")
		}
	} else if !kernel.SemHas([]string{"preflight", "design", "contract"}, phase) {
		return s.Reject("FRONTEND_BINDING", "实现输入缺少当前登记的批准Slice")
	}
	if sliceID == "" && !kernel.SemHas([]string{"preflight", "design", "contract"}, phase) {
		return s.Reject("FRONTEND_BINDING", "本地前端输入需要明确切片身份")
	}
	if !kernel.SemHas([]string{"preflight", "design", "contract"}, phase) {
		gate := kernel.SemMap(kernel.SemMap(cp["gates"])["gate.slice-contract-approved"])
		if gate["subject_ref"] != c.Ref {
			return s.Reject("FRONTEND_BINDING", "当前功能门禁未批准同一Slice")
		}
		approval := kernel.First(kernel.Text(gate["approval_ref"]), cpRef)
		if err = contractSliceApproval(s, c, map[string]string{"approval-ref": approval}); err != nil {
			return err
		}
		if err = s.GateChecks(cp, cpRef, "gate.slice-contract-approved"); err != nil {
			return err
		}
		backendRequired := impact["backend"] == true || impact["api"] == true || impact["data"] == true
		if backendRequired {
			if err = contractLocalFrontendBackend(s, cpRef, c, intent, coordination, participants); err != nil {
				return err
			}
		} else {
			backendStatus := kernel.SemMap(c.Raw["backend"])["status"]
			if kernel.ContractN(c.Raw["schema_version"]) == 3 {
				backendStatus = kernel.SemMap(kernel.SemMap(c.Raw["applicability"])["backend"])["status"]
			}
			apiNotApplicable := kernel.SemMap(kernel.SemMap(c.Raw["applicability"])["api"])["status"] == "not-applicable"
			if kernel.ContractN(c.Raw["schema_version"]) == 2 {
				apiNotApplicable = kernel.SemMap(c.Raw["contract"])["api_impact"] == false
			}
			if backendStatus != "not-applicable" || !apiNotApplicable {
				return s.Reject("FRONTEND_APPLICABILITY", "后端不适用与当前Slice范围冲突")
			}
			na, err := contractBoundDoc(s, c.Basis["no_api_impact_record"])
			if err != nil {
				return err
			}
			if na["status"] != "not-applicable" || na["slice_id"] != c.Raw["slice_id"] {
				return s.Reject("FRONTEND_APPLICABILITY", "无API依据未绑定当前Slice")
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
	s.Report.Coverage = map[string]any{"result": "inputs-verified", "ready_for_agent": false, "delivery_mode": "local-approved-assets", "root": s.Root, "checkpoint_ref": cpRef, "spec_ref": source["spec_ref"], "slice_contract_ref": currentSliceRef, "slice_id": currentSliceID, "phase": phase, "inputs_current": false}
	return nil
}

// An explicit backend participant is authoritative; unavailable or mismatched
// external evidence cannot be replaced by a local record from the same root.
func contractLocalFrontendBackend(s *kernel.SemanticSession, cpRef string, c *nativeSlice, intent ProgressionTarget, coordination map[string]any, participants map[string]*kernel.SemanticSession) error {
	declared := false
	for _, consumer := range intent.Consumers {
		declared = declared || consumer.Profile == "backend"
	}
	backend := participants["backend"]
	if !declared {
		profile, _, err := kernel.ProgressionPolicy(s)
		if err != nil {
			return err
		}
		if profile != "frontend" {
			return backendBusinessLocalEvidence(s, cpRef)
		}
		binding := kernel.SemMap(kernel.SemMap(kernel.SemMap(c.Raw["extensions"])["frontend"])["backend_dependency"])
		dependency, err := contractBoundDoc(s, binding)
		if err != nil {
			return err
		}
		if kernel.ContractN(dependency["schema_version"]) != 1 || dependency["kind"] != "frontend-backend-dependency" || len(dependency) != 4 {
			return s.Reject("FRONTEND_BINDING", "本地前端实际后端依赖须为明确外部工程与checkpoint")
		}
		root, ref := kernel.Text(dependency["root"]), kernel.Text(dependency["checkpoint_ref"])
		if !filepath.IsAbs(root) || filepath.Clean(root) != root || root == s.Root || ref == "" {
			return s.Reject("FRONTEND_BINDING", "后端依赖不得指向前端本身或猜测工程")
		}
		backend = kernel.NewSemanticSession(s.Ctx, root, map[string]string{"checkpoint": ref})
		s.Children = append(s.Children, backend)
		_, backendCP, err := kernel.ProgressionLocation(backend, ref)
		if err != nil {
			return err
		}
		identity, err := backend.Doc(".template-spec/process/harness-profile.yaml")
		if err != nil {
			return err
		}
		cp, err := s.Doc(cpRef)
		if err != nil {
			return err
		}
		if identity["profile_id"] != "harness.backend-delivery" || backendCP["feature_id"] != cp["feature_id"] {
			return s.Reject("FRONTEND_BINDING", "后端依赖必须是同功能当前专职Backend工程")
		}
		if status, reason := progressionProfileTerminal(backend, "backend", ref, backendCP); status != "reached" {
			return s.Reject("FRONTEND_INPUTS", "明确后端职责终点尚未完成："+reason)
		}
	} else {
		row := kernel.SemMap(coordination["backend"])
		if backend == nil || row["input_status"] != "verified" || row["completion_status"] != "reached" {
			return s.Reject("FRONTEND_INPUTS", "显式后端当前来源、完整交付及本端验收尚未核验")
		}
	}
	a, err := progressionBackendAuthorization(backend, backend.CheckpointRef, "", true)
	if err != nil {
		return err
	}
	terminal, err := backend.Doc(a.TerminalRef)
	if err != nil {
		return err
	}
	delivery, err := backendInspectDeliveryMode(backend, kernel.Text(kernel.SemMap(terminal["delivery"])["ref"]), map[string]string{"checkpoint": backend.CheckpointRef}, terminal["delivery_mode"] == "local-evidence", backend.CheckpointRef)
	if err != nil {
		return err
	}
	current, err := loadNativeSlice(backend, kernel.Text(kernel.SemMap(delivery["slice_contract"])["ref"]))
	if err != nil {
		return err
	}
	if kernel.SemMap(delivery["scope"])["slice_id"] != c.Raw["slice_id"] {
		return s.Reject("FRONTEND_BINDING", "显式后端交付与当前前端切片不同")
	}
	specDigest := strings.TrimPrefix(kernel.Text(c.Basis["spec"]["digest"]), "sha256:")
	if specDigest == "" || strings.TrimPrefix(kernel.Text(current.Basis["spec"]["digest"]), "sha256:") != specDigest {
		return s.Reject("FRONTEND_BINDING", "显式后端当前Slice未消费同一批准Spec")
	}
	if api := c.Basis["openapi_freeze"]; api != nil {
		if strings.TrimPrefix(kernel.Text(api["digest"]), "sha256:") != strings.TrimPrefix(kernel.Text(kernel.SemMap(delivery["openapi"])["digest"]), "sha256:") {
			return s.Reject("FRONTEND_BINDING", "前端当前冻结接口与显式后端交付不同")
		}
	} else {
		na, err := contractBoundDoc(s, c.Basis["no_api_impact_record"])
		if err != nil {
			return err
		}
		if na["status"] != "not-applicable" || na["slice_id"] != c.Raw["slice_id"] || kernel.SemMap(delivery["openapi"])["mode"] != "not-applicable" {
			return s.Reject("FRONTEND_BINDING", "无API依据与当前后端交付冲突")
		}
	}
	return nil
}
