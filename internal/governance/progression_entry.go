package governance

import (
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/worklayout"
	"path"
)

// This readonly restriction is shared by native task admission and target status.
// It does not issue execution permission or change a recorded route.
func progressionTaskEntry(s *kernel.SemanticSession, task map[string]any) error {
	contract := kernel.SemMap(task["contract"])
	if len(kernel.SemStrings(task["allowed_write_paths"])) == 0 || !kernel.SemHas([]string{"Drafter", "Worker"}, kernel.Text(task["execution_state"])) || kernel.SemHas([]string{"resolved", "failed"}, kernel.Text(task["workflow_status"])) || kernel.SemHas([]string{"template-maintenance", "read-only-intake"}, kernel.Text(contract["kind"])) {
		return nil
	}
	identity, err := s.Doc("yss-project.yaml")
	if err != nil {
		return err
	}
	if identity["repository_mode"] == "template-source" {
		return nil
	}
	profileDoc, err := s.Doc(".template-spec/process/harness-profile.yaml")
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
		return s.Unavailable("IDENTITY", "正式任务缺少合法 Profile 身份")
	}
	current, err := s.Doc(kernel.GuidanceContractRef(profile))
	if err != nil {
		return err
	}
	if len(kernel.SemMap(current["progression_target"])) == 0 {
		return nil // Legacy instances keep their existing admission rules.
	}
	cpRef := kernel.First(kernel.Text(task["checkpoint_ref"]), kernel.Text(kernel.SemMap(task["result"])["checkpoint_ref"]))
	assetRef := kernel.First(kernel.Text(contract["slice_contract_ref"]), kernel.Text(contract["lifecycle_ref"]))
	if cpRef == "" && assetRef != "" {
		cpRef, err = progressionTaskAssetCheckpoint(s, assetRef)
		if err != nil {
			return err
		}
	}
	if cpRef == "" {
		if profile == "spec" {
			return s.Reject("PROGRESSION_BINDING", "正式任务缺少明确功能 checkpoint 或已登记资产绑定")
		}
		return nil // Unbound historical specialist tasks do not invent a feature.
	}
	child := kernel.NewSemanticSession(s.Ctx, s.Root, map[string]string{"checkpoint": cpRef})
	s.Children = append(s.Children, child)
	result, err := progressionReadSession(child)
	if err != nil {
		return err
	}
	projection := kernel.SemMap(result["progression"])
	if projection["inputs_current"] != true || projection["status"] != "pending" {
		return s.Reject("PROGRESSION_TARGET_BLOCKED", "当前功能目标已达到或不可验证；保留真实 next，修改意图并复验后再派发："+kernel.Text(projection["reason"]))
	}
	return nil
}

// An explicit asset can select only its single active map registration. A
// directory, neighboring checkout or task's feature label never supplies a CP.
func progressionTaskAssetCheckpoint(s *kernel.SemanticSession, assetRef string) (string, error) {
	if !kernel.ContractPath(assetRef) {
		return "", s.Reject("PROGRESSION_BINDING", "任务资产引用不是项目内路径")
	}
	present, err := s.Exists(worklayout.TrackerRef)
	if err != nil || !present {
		return "", err
	}
	raw, err := s.Bytes(worklayout.TrackerRef)
	if err != nil {
		return "", err
	}
	layout, err := worklayout.FromDocument(s.Root, raw)
	if err != nil {
		return "", err
	}
	files, err := s.Scan(layout.Root)
	if err != nil {
		return "", err
	}
	found := ""
	for _, mapRef := range files {
		if path.Base(mapRef) != "map.md" || path.Dir(path.Dir(mapRef)) != layout.Root {
			continue
		}
		registration, err := kernel.ContractTicketMetadata(s, mapRef)
		if err != nil {
			return "", err
		}
		ref := kernel.Text(registration["checkpoint_ref"])
		if ref == "" {
			continue
		}
		cp, err := s.Doc(ref)
		if err != nil {
			return "", err
		}
		refs := []string{}
		for _, item := range kernel.SemMap(cp["artifacts"]) {
			refs = append(refs, kernel.Text(kernel.SemMap(item)["ref"]))
		}
		for _, item := range kernel.SemMap(cp["gates"]) {
			refs = append(refs, kernel.Text(kernel.SemMap(item)["subject_ref"]))
		}
		implementation := kernel.SemMap(kernel.SemMap(cp["human_review"])["implementation"])
		for _, key := range []string{"slice_contract_ref", "vertical_slice_ticket_ref"} {
			refs = append(refs, kernel.Text(implementation[key]))
		}
		for _, key := range []string{"slice_contract_ref", "vertical_slice_ticket_ref", "spec_ref", "business_ticket_set_ref", "stage_decision_package_ref"} {
			refs = append(refs, kernel.Text(cp[key]))
		}
		if !kernel.SemHas(refs, assetRef) {
			continue
		}
		if found != "" {
			return "", s.Reject("PROGRESSION_BINDING", "任务资产存在多重功能登记")
		}
		found = ref
	}
	return found, nil
}
