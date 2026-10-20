package kernel

const StageGatesCapability = "stage-gates-v1"

var StageGateIDs = []string{"gate.plan-approved", "gate.spec-baseline-approved", "gate.implementation-admitted", "gate.delivery-accepted"}

// Selection depends on an explicitly checked policy, never absent legacy role files.
func (s *SemanticSession) StageGatesEnabled() bool {
	return s.GovernanceVersion == 2 && s.Report.Profile == "spec" && Text(ApMap(s.GovernancePolicy["stage_gates"])["capability"]) == StageGatesCapability
}

func (s *SemanticSession) validateStageGates(profile, policy map[string]any) error {
	p := ApMap(policy["stage_gates"])
	if s.Report.Profile != "spec" || !ApContains(profile["required_capabilities"], StageGatesCapability) || ApNumber(p["schema_version"]) != 1 || p["capability"] != StageGatesCapability {
		return s.Unavailable("CAPABILITY", "四阶段门禁要求 Spec Profile 与当前政策同时声明 stage-gates-v1")
	}
	if len(ApRows(p["gates"])) != len(StageGateIDs) || len(ApRows(s.Registry["gates"])) != len(StageGateIDs) {
		return s.Unavailable("GOVERNANCE_POLICY", "四阶段注册表必须恰好包含四个出口")
	}
	seen := map[string]bool{}
	for _, g := range ApRows(p["gates"]) {
		id := Text(g["id"])
		if !SemHas(StageGateIDs, id) || seen[id] || SemanticDefinition(s.Registry, "gates", id) == nil || !ApIsArray(g["checks"]) || !ApUnique(g["work_stages"]) {
			return s.Unavailable("GOVERNANCE_POLICY", "非法或重复的阶段出口: "+id)
		}
		seen[id] = true
		for _, c := range ApStrings(g["checks"]) {
			if SemanticDefinition(s.Registry, "checks", c) == nil {
				return s.Unavailable("GOVERNANCE_POLICY", "阶段引用未登记检查: "+c)
			}
		}
	}
	return nil
}

func (s *SemanticSession) StageGate(id string) map[string]any {
	if !s.StageGatesEnabled() {
		return nil
	}
	return ApFind(ApMap(s.GovernancePolicy["stage_gates"])["gates"], "id", id)
}
