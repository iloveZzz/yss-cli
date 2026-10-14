package kernel

import (
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func (s *SemanticSession) ExecutionScope() (map[string]any, error) {
	plugin, err := s.BackendPluginBinding()
	if err != nil {
		return nil, err
	}
	present, err := s.Exists(".yss-execution-scope.yaml")
	if err != nil {
		return nil, err
	}
	if !present {
		if SemHas([]string{"yss-plan-to-backend", "yss-backend-delivery"}, Text(plugin["plugin"])) {
			return nil, s.Reject("EXECUTION_SCOPE", "插件项目缺少显式执行范围")
		}
		return nil, nil
	}
	marker, err := s.Doc(".yss-execution-scope.yaml")
	if err != nil {
		return nil, err
	}
	identity, err := s.Doc("yss-project.yaml")
	if err != nil {
		return nil, err
	}
	version, valid := Integer(marker["schema_version"])
	if !valid || version != 1 || len(marker) != 2 || marker["scope_id"] != "plan-to-backend" || identity["repository_mode"] != "project-instance" {
		return nil, s.Reject("EXECUTION_SCOPE", "执行范围版本、身份或字段非法")
	}
	if plugin != nil && (!SemHas([]string{"yss-plan-to-backend", "yss-backend-delivery"}, Text(plugin["plugin"])) || plugin["execution_scope"] != marker["scope_id"]) {
		return nil, s.Reject("EXECUTION_SCOPE", "插件与执行范围冲突")
	}
	contract, err := s.Doc(".agents/skills/yss-product-lifecycle/references/orchestration-contract.yaml")
	if err != nil {
		return nil, err
	}
	policy := SemMap(SemMap(contract["execution_scopes"])["plan-to-backend"])
	if policy["terminal_work_unit"] != "work-unit.backend-delivery" || len(SemStrings(policy["allowed_work_units"])) == 0 {
		return nil, s.Unavailable("CAPABILITY", "本地主控合同缺少执行范围策略")
	}
	return policy, nil
}

func (s *SemanticSession) TrackingTransition(cp map[string]any, ref, current, next string) error {
	identity, err := s.Doc("yss-project.yaml")
	if err != nil {
		return err
	}
	if identity["repository_mode"] == "template-source" {
		return nil
	}
	if TrackedUnits[current] == "" && TrackedUnits[next] == "" {
		return nil
	}
	if _, err = CheckStage(s.V, cp, ref, s); err != nil {
		return s.Reject("TRACKING", err.Error())
	}
	t, err := AsTracking(cp)
	if err != nil {
		return err
	}
	if t == nil {
		return nil
	}
	currentStage, nextStage := TrackedUnits[current], TrackedUnits[next]
	if Text(cp["profile_id"]) == "harness.business-ddd-strategy-handoff" {
		if current == "work-unit.business-ticket-formalization" || current == "work-unit.strategic-design-handoff" {
			currentStage = "stage.ticket-formalization"
		}
		if next == "work-unit.business-ticket-formalization" || next == "work-unit.strategic-design-handoff" {
			nextStage = "stage.ticket-formalization"
		}
	}
	for _, item := range t.Items {
		exiting := item.WorkUnit == current || currentStage != "" && currentStage != nextStage && item.Stage == currentStage
		if exiting && !SemHas([]string{"completed", "cancelled", "deferred"}, item.Progress) {
			return s.Reject("TRACKING", "阶段流转仍有未完成工作项: "+item.ID)
		}
		if exiting && item.RecheckRequired {
			return s.Reject("TRACKING", "阶段工作项需要重新核验: "+item.ID)
		}
	}
	return nil
}

func (s *SemanticSession) BackendPluginBinding() (map[string]any, error) {
	present, e := s.Exists(".yss-backend-plugin.json")
	if e != nil {
		return nil, e
	}
	ref := ".yss-backend-plugin.json"
	if !present {
		ref = ".yss-plugin.json"
		present, e = s.Exists(ref)
		if e != nil {
			return nil, e
		}
		if !present {
			return nil, nil
		}
	}
	plugin, e := s.Doc(ref)
	if e != nil {
		return nil, e
	}
	if ref == ".yss-backend-plugin.json" && (ApNumber(plugin["schema_version"]) != 2 || plugin["plugin"] != "yss-backend-delivery" || plugin["execution_scope"] != "plan-to-backend") {
		return nil, s.Reject("EXECUTION_SCOPE", "native插件职责绑定无效")
	}
	if ref == ".yss-backend-plugin.json" {
		legacyPresent, err := s.Exists(".yss-plugin.json")
		if err != nil {
			return nil, err
		}
		lineage := SemMap(plugin["legacy_binding"])
		if legacyPresent {
			legacy, err := s.Doc(".yss-plugin.json")
			if err != nil {
				return nil, err
			}
			if !SemHas([]string{"yss-plan-to-backend", "yss-backend-delivery"}, Text(legacy["plugin"])) || legacy["execution_scope"] != plugin["execution_scope"] {
				return nil, s.Reject("EXECUTION_SCOPE", "历史插件与native职责绑定不一致")
			}
			if len(lineage) != 0 {
				data, err := s.Bytes(".yss-plugin.json")
				if err != nil {
					return nil, err
				}
				if lineage["path"] != ".yss-plugin.json" || lineage["sha256"] != safefs.Digest(data) {
					return nil, s.Reject("EXECUTION_SCOPE", "历史插件绑定原字节已漂移")
				}
			}
		} else if len(lineage) != 0 {
			return nil, s.Reject("EXECUTION_SCOPE", "历史插件绑定缺失")
		}
	}
	return plugin, nil
}
