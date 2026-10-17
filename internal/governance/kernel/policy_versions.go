package kernel

import (
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"strings"
)

const RoleIndependentCapability = "role-independent-governance-v1"

// Policy2 is selected only by the complete identity2/Profile3 tuple, never by a missing role file.
func (s *SemanticSession) loadGovernancePolicy(identity, profile map[string]any) error {
	s.GovernanceVersion, s.GovernancePolicy, s.GovernanceDigest = 1, nil, ""
	newIdentity := ApNumber(identity["schema_version"]) == 2
	if identity["repository_mode"] == "project-instance" && ((!newIdentity && ApNumber(profile["schema_version"]) > 2) || (newIdentity && ApNumber(profile["schema_version"]) != 3)) {
		return s.Unavailable("GOVERNANCE_POLICY", "根身份与Profile混代；先显式迁移治理实例")
	}
	present, err := s.Exists(schema.OrchestrationRef)
	if err != nil {
		return err
	}
	if !present {
		if newIdentity {
			return s.Unavailable("GOVERNANCE_POLICY", "新治理实例缺少固定编排政策；不能回退 Skill 或岗位规则")
		}
		return nil // Historical source snapshots may contain only their original signing authority.
	}
	policy, _, digest, err := schema.LoadOrchestration(s.Bytes, schema.OrchestrationRef)
	if err != nil {
		return s.Unavailable("GOVERNANCE_POLICY", err.Error())
	}
	version := ApNumber(policy["schema_version"])
	if version != 1 && version != 2 {
		return s.Unavailable("GOVERNANCE_POLICY", "未知编排政策版本")
	}
	if identity["repository_mode"] == "template-source" {
		// Source maintenance retains its explicit legacy task contract; it is not a product instance.
		return nil
	}
	if !newIdentity {
		if version != 1 || ApNumber(profile["schema_version"]) > 2 {
			return s.Unavailable("GOVERNANCE_POLICY", "根身份、Profile和编排政策混代；先显式迁移治理实例")
		}
		return nil
	}
	if version != 2 || ApNumber(profile["schema_version"]) != 3 || profile["status"] != "active" || !ApContains(profile["required_capabilities"], RoleIndependentCapability) || !SemHas([]string{"spec", "backend"}, s.Report.Profile) {
		return s.Unavailable("GOVERNANCE_POLICY", "新治理必须使用identity2/Profile3/policy2及明确能力；旧Design须显式迁移到Spec")
	}
	for _, capability := range ApStrings(profile["required_capabilities"]) {
		if capability != RoleIndependentCapability {
			return s.Unavailable("CAPABILITY", "未知Profile必需能力: "+capability)
		}
	}
	if present, err := s.Exists(domain.MetadataFile); err != nil {
		return err
	} else if present {
		metadata, err := s.Doc(domain.MetadataFile)
		if err != nil {
			return err
		}
		if ApNumber(metadata["schemaVersion"]) != 4 || ApNumber(metadata["bundleSchemaVersion"]) != 4 {
			return s.Unavailable("GOVERNANCE_POLICY", "新身份不能混用旧metadata/Bundle")
		}
	}
	for _, legacy := range domain.Profiles {
		if present, err := s.Exists(legacy.Metadata); err != nil {
			return err
		} else if present {
			return s.Unavailable("GOVERNANCE_POLICY", "新身份不能混用旧CLI家族metadata")
		}
	}
	if _, ok := Object(profile["lifecycle"]); !ok {
		return s.Unavailable("GOVERNANCE_POLICY", "Profile缺少lifecycle")
	}
	if _, ok := Object(profile["execution"]); !ok {
		return s.Unavailable("GOVERNANCE_POLICY", "Profile缺少execution")
	}
	versions := ApMap(policy["governance_versions"])
	if versions["required_capability"] != RoleIndependentCapability || versions["activation"] != "native-consumers-and-bundle-closure-required-before-instance-writes" || versions["legacy_policy"] != "read-verify-explicit-migrate-only-no-new-task-or-stage-write" {
		return s.Unavailable("GOVERNANCE_POLICY", "治理版本、激活或历史边界缺失")
	}
	for key, want := range map[string]int{"project_identity": 2, "harness_profile": 3, "task": 3, "approval_record": 3, "review_bundle": 3, "continuation": 2, "delivery_authorization": 2, "daily": 2, "standard": 2, "user_decision": 1, "lifecycle_registry": 1, "native_bundle": 4, "native_metadata": 4, "source_lock": 5, "slice_implementation_contract": 4, "workflow_execution_result": 1} {
		if ApNumber(ApMap(versions[key])["current"]) != want {
			return s.Unavailable("GOVERNANCE_POLICY", "缺失或不支持的治理版本组合: "+key)
		}
	}
	gatePolicy := ApMap(policy["gate_policy"])
	gates := ApMap(ApMap(gatePolicy["profiles"])[s.Report.Profile])
	decisions := ApMap(policy["user_decision_policy"])
	scope := ApMap(ApMap(decisions["profile_scope"])[s.Report.Profile])
	continuation := ApMap(decisions["continuation"])
	if gatePolicy["default_if_unlisted"] != "reject-unlisted" || gates == nil || ApNumber(decisions["schema_version"]) != 2 || scope == nil || !ApIsArray(scope["gates"]) || ApMap(scope["work_units"]) == nil || ApMap(scope["work_unit_gates"]) == nil || len(ApArray(decisions["source_kinds"])) == 0 || ApNumber(continuation["schema_version"]) != 2 || continuation["kind"] != "approved-scope-continuation-v2" || len(ApArray(ApMap(continuation["boundaries_by_profile"])[s.Report.Profile])) == 0 {
		return s.Unavailable("GOVERNANCE_POLICY", "当前Profile的门禁/用户决定/延续政策缺失或未知")
	}
	if ApMap(policy["review_modes"])["default"] != "self-check" {
		return s.Unavailable("GOVERNANCE_POLICY", "复核方式政策缺失")
	}
	for _, state := range []string{"none-known", "known-verifiable", "known-unverifiable"} {
		if Text(ApMap(ApMap(policy["external_rules_policy"])["states"])[state]) == "" {
			return s.Unavailable("GOVERNANCE_POLICY", "外部约束状态政策缺失: "+state)
		}
	}
	checks := map[string]bool{}
	allChecks := map[string]bool{}
	for _, check := range ApRows(policy["technical_checks"]) {
		id := Text(check["id"])
		if id == "" || allChecks[id] || !ApUnique(check["profiles"]) || !ApUnique(check["review_skills"]) {
			return s.Unavailable("GOVERNANCE_POLICY", "专业检查组缺失或重复")
		}
		allChecks[id] = true
		if ApContains(check["profiles"], s.Report.Profile) {
			checks[id] = true
		}
	}
	if len(checks) == 0 {
		return s.Unavailable("GOVERNANCE_POLICY", "当前Profile缺少专业检查组")
	}
	seen := map[string]bool{}
	for _, bucket := range []string{"automatic_checks", "check_reviews", "technical_reviews", "user_decision_gates", "contract_approval_gates", "evidence_only", "work_unit_reviews"} {
		if !ApIsArray(gates[bucket]) {
			return s.Unavailable("GOVERNANCE_POLICY", "当前Profile缺少完整门禁分类: "+bucket)
		}
		for _, entry := range ApArray(gates[bucket]) {
			id := Text(entry)
			if row, ok := Object(entry); ok {
				id = Text(row["gate"])
				if bucket == "work_unit_reviews" {
					id = Text(row["work_unit"])
				}
				if !ApUnique(row["capability_ids"]) {
					return s.Unavailable("GOVERNANCE_POLICY", "门禁缺少专业检查组")
				}
				for _, capability := range ApStrings(row["capability_ids"]) {
					if !checks[capability] {
						return s.Unavailable("GOVERNANCE_POLICY", "门禁引用不适用于当前Profile的专业检查组: "+capability)
					}
				}
			} else if bucket == "check_reviews" || bucket == "technical_reviews" || bucket == "work_unit_reviews" {
				return s.Unavailable("GOVERNANCE_POLICY", "专业门禁必须包含检查组")
			}
			registryBucket := "gates"
			if strings.HasPrefix(id, "check.") {
				registryBucket = "checks"
			}
			if bucket == "work_unit_reviews" {
				registryBucket = "work_units"
			}
			if id == "" || seen[id] || ApFind(s.Registry[registryBucket], "id", id) == nil {
				return s.Unavailable("GOVERNANCE_POLICY", "当前Profile门禁重复或超出注册表: "+id)
			}
			seen[id] = true
		}
	}
	for _, id := range ApStrings(scope["gates"]) {
		if !seen[id] {
			return s.Unavailable("GOVERNANCE_POLICY", "用户决定超出当前Profile门禁: "+id)
		}
	}
	if err := s.validatePolicyCompleteness(policy, scope, continuation, seen); err != nil {
		return err
	}
	s.GovernanceVersion, s.GovernancePolicy, s.GovernanceDigest = 2, policy, digest
	return nil
}

// Every registered gate/check must have exactly one classification (including legitimate
// empty classes). User decision mappings must agree with continuation and work-unit routes.
func (s *SemanticSession) validatePolicyCompleteness(policy, scope, continuation map[string]any, seen map[string]bool) error {
	for _, bucket := range []string{"gates", "checks"} {
		for _, row := range ApRows(s.Registry[bucket]) {
			if id := Text(row["id"]); !seen[id] {
				return s.Unavailable("GOVERNANCE_POLICY", "当前Profile缺少注册边界的门禁分类: "+id)
			}
		}
	}
	boundaries := ApMap(continuation["boundaries_by_profile"])[s.Report.Profile]
	if !ApUnique(boundaries) || !ApUnique(scope["gates"]) {
		return s.Unavailable("GOVERNANCE_POLICY", "用户决定或延续边界缺失、重复")
	}
	expectedGates := []string{}
	for _, id := range ApStrings(boundaries) {
		if strings.HasPrefix(id, "gate.") {
			if ApFind(s.Registry["gates"], "id", id) == nil {
				return s.Unavailable("GOVERNANCE_POLICY", "延续边界超出当前Profile注册表: "+id)
			}
			expectedGates = append(expectedGates, id)
		} else if id != "implementation-scope" {
			return s.Unavailable("GOVERNANCE_POLICY", "未知延续边界: "+id)
		}
	}
	if !SameStringSet(scope["gates"], expectedGates) {
		return s.Unavailable("GOVERNANCE_POLICY", "用户决定门禁与当前Profile延续边界不完整或不一致")
	}
	// These two protocol boundaries are task/continuation semantics, not actor qualifications.
	expectedUnits := map[string]any{}
	for unit, boundary := range map[string]string{"work-unit.implementation-repository-preparation": "scaffold-choice", "work-unit.slice-implementation": "implementation-scope"} {
		if ApFind(s.Registry["work_units"], "id", unit) != nil {
			expectedUnits[unit] = boundary
		}
	}
	if !ApEqual(scope["work_units"], expectedUnits) || (expectedUnits["work-unit.slice-implementation"] != nil && !ApContains(boundaries, "implementation-scope")) {
		return s.Unavailable("GOVERNANCE_POLICY", "当前Profile工作单元的用户决定映射缺失或不一致")
	}
	expectedUnitGates := map[string]any{}
	for _, unit := range ApRows(s.Registry["work_units"]) {
		id := Text(unit["id"])
		route := ApMap(ApMap(policy["work_unit_routes"])[id])
		required := []string{}
		for _, field := range []string{"entry_user_gate", "human_gate"} {
			value, present := route[field]
			if !present {
				continue
			}
			gate := Text(value)
			if gate == "spec-baseline" {
				gate = "gate.spec-baseline-approved"
			}
			registered := ApFind(s.Registry["gates"], "id", gate)
			if registered == nil {
				return s.Unavailable("GOVERNANCE_POLICY", "工作单元路由引用未知或空门禁: "+id+"/"+field)
			}
			// The existing strategic handoff is an aggregate technical verification
			// of source approvals, not a new user decision. Require the complete
			// explicit relationship; never infer this exception from missing mappings.
			handoff := ApMap(ApMap(policy["progression_target"])["external_strategic_handoff"])
			if field == "human_gate" && handoff["work_unit"] == id && handoff["gate"] == gate {
				classes := ApMap(ApMap(ApMap(policy["gate_policy"])["profiles"])[s.Report.Profile])
				if handoff["source_authority"] != "original-profile-and-current-approved-assets" || handoff["completion_requires"] != "current-source-approvals-and-immutable-package-verification" || ApFind(classes["technical_reviews"], "gate", gate) == nil || !ApUnique(registered["requires_gates"]) {
					return s.Unavailable("GOVERNANCE_POLICY", "技术交接缺少明确的源批准复用关系: "+gate)
				}
				for _, sourceGate := range ApStrings(registered["requires_gates"]) {
					if !SemHas(expectedGates, sourceGate) {
						return s.Unavailable("GOVERNANCE_POLICY", "技术交接缺少必需的源用户决定: "+sourceGate)
					}
				}
				continue
			}
			if !SemHas(expectedGates, gate) {
				return s.Unavailable("GOVERNANCE_POLICY", "工作单元路由要求的用户决定或延续边界缺失: "+gate)
			}
			if !SemHas(required, gate) {
				required = append(required, gate)
			}
		}
		if len(required) > 0 {
			expectedUnitGates[id] = required
		}
	}
	actual := ApMap(scope["work_unit_gates"])
	if len(actual) != len(expectedUnitGates) {
		return s.Unavailable("GOVERNANCE_POLICY", "工作单元的用户决定门禁映射不完整")
	}
	for unit, required := range expectedUnitGates {
		if !ApUnique(actual[unit]) || !SameStringSet(actual[unit], ApStrings(required)) {
			return s.Unavailable("GOVERNANCE_POLICY", "工作单元的用户决定门禁与路由不一致: "+unit)
		}
	}
	return nil
}

// GatePolicy and UserDecisionPolicy do not adapt new policies into legacy roles.
func (s *SemanticSession) GatePolicy() map[string]any {
	if s.GovernanceVersion == 2 {
		return ApMap(ApMap(ApMap(s.GovernancePolicy["gate_policy"])["profiles"])[s.Report.Profile])
	}
	return ApMap(s.Roles["gate_policy"])
}
func (s *SemanticSession) UserDecisionPolicy() map[string]any {
	if s.GovernanceVersion != 2 {
		return ApMap(s.Roles["user_decision_policy"])
	}
	policy := ApCopy(ApMap(s.GovernancePolicy["user_decision_policy"]))
	for key, value := range ApMap(ApMap(policy["profile_scope"])[s.Report.Profile]) {
		policy[key] = value
	}
	continuation := ApCopy(ApMap(policy["continuation"]))
	continuation["boundaries"] = ApMap(continuation["boundaries_by_profile"])[s.Report.Profile]
	policy["continuation"] = continuation
	return policy
}

// Call this from both plan construction and apply's fresh reconstruction. Read/verify never calls it.
func (s *SemanticSession) RequireGovernanceWrite() error {
	identity, err := s.Doc("yss-project.yaml")
	if err != nil {
		return err
	}
	if identity["repository_mode"] != "project-instance" {
		return s.Reject("IDENTITY", "治理实例写入要求project-instance")
	}
	if ApNumber(identity["schema_version"]) == 1 {
		return s.Reject("GOVERNANCE_MIGRATION_REQUIRED", "旧Spec/Backend/Design仅允许原义读取与核验；任务或阶段写入前须先显式迁移到identity2/Profile3/policy2；保留原批准与历史记录")
	}
	if err = s.Authorities(); err != nil {
		return err
	}
	if s.GovernanceVersion != 2 {
		return s.Reject("GOVERNANCE_MIGRATION_REQUIRED", "任务或阶段写入需要完整的新治理合同；先显式迁移")
	}
	// Registration is the executable capability check; no flag can enable absent consumers.
	for _, kind := range []string{"task", "task-baseline", "task-candidate", "task-selection", "slice", "slice-draft", "slice-preflight", "slice-execution-result", "approval", "user-decision", "verification", "daily-record", "standard-record", "checkpoint", "checkpoint-boundary", "tracking-entry", "business-checkpoint", "plan-checkpoint", "plan-review-task", "plan-entry", "plan-aggregate", "checkpoint-user-decisions", "checkpoint-approvals", "gate-checks", "execution-scope", "next-route", "context-reconciliation", "reading-transition", "backend-terminal", "backend-review", "backend-delivery", "handoff-package", "handoff-source-approval"} {
		if RoleIndependentValidators[kind] == nil {
			return s.Unavailable("CAPABILITY", "新治理写入尚未激活，缺少原生消费者: "+kind)
		}
	}
	metadata, err := s.Doc(domain.MetadataFile)
	if err != nil {
		return s.Unavailable("CAPABILITY", "新治理写入需要已验证的metadata4/Bundle4装配闭包")
	}
	if ApNumber(metadata["schemaVersion"]) != 4 || ApNumber(metadata["bundleSchemaVersion"]) != 4 {
		return s.Unavailable("CAPABILITY", "新治理写入需要已验证的metadata4/Bundle4装配闭包")
	}
	return nil
}

func (s *SemanticSession) ValidateGovernancePolicyBinding(binding map[string]any) error {
	if s.GovernanceVersion != 2 || binding["ref"] != schema.OrchestrationRef || ApNumber(binding["version"]) != 2 || Text(binding["digest"]) != strings.TrimPrefix(s.GovernanceDigest, "sha256:") {
		return s.Reject("GOVERNANCE_POLICY_BINDING", "记录未绑定当前完整编排政策及版本")
	}
	return nil
}
