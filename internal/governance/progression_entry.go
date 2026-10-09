package governance

import (
	"path"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/worklayout"
)

// This readonly restriction is shared by native task admission and target status.
// It does not issue execution permission or change a recorded route.
func progressionTaskEntry(s *semanticSession, task map[string]any) error {
	contract := semMap(task["contract"])
	if len(semStrings(task["allowed_write_paths"])) == 0 || !semHas([]string{"Drafter", "Worker"}, text(task["execution_state"])) || semHas([]string{"resolved", "failed"}, text(task["workflow_status"])) || semHas([]string{"template-maintenance", "read-only-intake"}, text(contract["kind"])) {
		return nil
	}
	identity, err := s.doc("yss-project.yaml")
	if err != nil {
		return err
	}
	if identity["repository_mode"] == "template-source" {
		return nil
	}
	profileDoc, err := s.doc(".template-spec/process/harness-profile.yaml")
	if err != nil {
		return err
	}
	profile := ""
	for name, known := range domain.Profiles {
		if profileDoc["profile_id"] == known.ID {
			profile = name
		}
	}
	if profile == "" {
		return s.unavailable("IDENTITY", "正式任务缺少合法 Profile 身份")
	}
	current, err := s.doc(guidanceContractRef(profile))
	if err != nil {
		return err
	}
	if len(semMap(current["progression_target"])) == 0 {
		return nil // Legacy instances keep their existing admission rules.
	}
	cpRef := first(text(task["checkpoint_ref"]), text(semMap(task["result"])["checkpoint_ref"]))
	assetRef := first(text(contract["slice_contract_ref"]), text(contract["lifecycle_ref"]))
	if cpRef == "" && assetRef != "" {
		cpRef, err = progressionTaskAssetCheckpoint(s, assetRef)
		if err != nil {
			return err
		}
	}
	if cpRef == "" {
		if profile == "spec" {
			return s.reject("PROGRESSION_BINDING", "正式任务缺少明确功能 checkpoint 或已登记资产绑定")
		}
		return nil // Unbound historical specialist tasks do not invent a feature.
	}
	child := newSemanticSession(s.ctx, s.root, map[string]string{"checkpoint": cpRef})
	s.children = append(s.children, child)
	result, err := progressionReadSession(child)
	if err != nil {
		return err
	}
	projection := semMap(result["progression"])
	if projection["inputs_current"] != true || projection["status"] != "pending" {
		return s.reject("PROGRESSION_TARGET_BLOCKED", "当前功能目标已达到或不可验证；保留真实 next，修改意图并复验后再派发："+text(projection["reason"]))
	}
	return nil
}

// An explicit asset can select only its single active map registration. A
// directory, neighboring checkout or task's feature label never supplies a CP.
func progressionTaskAssetCheckpoint(s *semanticSession, assetRef string) (string, error) {
	if !contractPath(assetRef) {
		return "", s.reject("PROGRESSION_BINDING", "任务资产引用不是项目内路径")
	}
	present, err := s.exists(worklayout.TrackerRef)
	if err != nil || !present {
		return "", err
	}
	raw, err := s.bytes(worklayout.TrackerRef)
	if err != nil {
		return "", err
	}
	layout, err := worklayout.FromDocument(s.root, raw)
	if err != nil {
		return "", err
	}
	files, err := s.scan(layout.Root)
	if err != nil {
		return "", err
	}
	found := ""
	for _, mapRef := range files {
		if path.Base(mapRef) != "map.md" || path.Dir(path.Dir(mapRef)) != layout.Root {
			continue
		}
		registration, err := contractTicketMetadata(s, mapRef)
		if err != nil {
			return "", err
		}
		ref := text(registration["checkpoint_ref"])
		if ref == "" {
			continue
		}
		cp, err := s.doc(ref)
		if err != nil {
			return "", err
		}
		refs := []string{}
		for _, item := range semMap(cp["artifacts"]) {
			refs = append(refs, text(semMap(item)["ref"]))
		}
		for _, item := range semMap(cp["gates"]) {
			refs = append(refs, text(semMap(item)["subject_ref"]))
		}
		implementation := semMap(semMap(cp["human_review"])["implementation"])
		for _, key := range []string{"slice_contract_ref", "vertical_slice_ticket_ref"} {
			refs = append(refs, text(implementation[key]))
		}
		for _, key := range []string{"slice_contract_ref", "vertical_slice_ticket_ref", "spec_ref", "business_ticket_set_ref", "stage_decision_package_ref"} {
			refs = append(refs, text(cp[key]))
		}
		if !semHas(refs, assetRef) {
			continue
		}
		if found != "" {
			return "", s.reject("PROGRESSION_BINDING", "任务资产存在多重功能登记")
		}
		found = ref
	}
	return found, nil
}
