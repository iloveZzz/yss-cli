package kernel

import (
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/worklayout"
	"path"
	"strings"
)

const ProgressionFile = "progression-target.json"

func ProgressionPolicy(s *SemanticSession) (string, map[string]any, error) {
	if err := s.Authorities(); err != nil {
		return "", nil, err
	}
	profileDoc, err := s.Doc(".template-spec/process/harness-profile.yaml")
	if err != nil {
		return "", nil, err
	}
	profile := ""
	for name, known := range domain.Profiles {
		if profileDoc["profile_id"] == known.ID {
			profile = name
		}
	}
	if profile == "" {
		return "", nil, s.Unavailable("IDENTITY", "未知推进目标 Profile")
	}
	contract, err := s.Doc(GuidanceContractRef(profile))
	if err != nil {
		return profile, nil, err
	}
	policy := SemMap(contract["progression_target"])
	if ContractN(policy["schema_version"]) != 1 || policy["config_file"] != ProgressionFile || !SemHas(policy["required_capabilities"], "lifecycle-target-v1") {
		return profile, nil, s.Unavailable("CAPABILITY", "当前实例不支持推进目标；先显式同步模板")
	}
	for _, capability := range SemStrings(policy["required_capabilities"]) {
		if capability != "lifecycle-target-v1" {
			return profile, nil, s.Unavailable("CAPABILITY", "未知推进目标能力："+capability)
		}
	}
	return profile, policy, nil
}

// The registered checkpoint selects the feature directory; a slug never does.
func ProgressionLocation(s *SemanticSession, cpRef string) (string, map[string]any, error) {
	if !ContractPath(cpRef) {
		return "", nil, s.Reject("PATH", "推进目标需要项目内显式 checkpoint")
	}
	cp, err := s.Doc(cpRef)
	if err != nil {
		return "", nil, err
	}
	checkpointVersion := 1
	if s.StageGatesEnabled() {
		checkpointVersion = 2
	}
	if ContractN(cp["schema_version"]) != checkpointVersion || cp["repository_mode"] != "project-instance" || !strings.HasPrefix(Text(cp["feature_id"]), "feature.") || len(Text(cp["feature_id"])) <= len("feature.") {
		return "", nil, s.Reject("IDENTITY", "checkpoint 缺少合法功能身份")
	}
	trackerBytes, err := s.Bytes(worklayout.TrackerRef)
	if err != nil {
		return "", nil, err
	}
	layout, err := worklayout.FromDocument(s.Root, trackerBytes)
	if err != nil {
		return "", nil, err
	}
	files, err := s.Scan(layout.Root)
	if err != nil {
		return "", nil, err
	}
	found := ""
	for _, mapRef := range files {
		if path.Base(mapRef) != "map.md" || path.Dir(path.Dir(mapRef)) != layout.Root {
			continue
		}
		meta, e := ContractTicketMetadata(s, mapRef)
		if e != nil {
			return "", nil, e
		}
		registered := Text(meta["checkpoint_ref"])
		if registered == "" {
			continue
		}
		other, e := s.Doc(registered)
		if e != nil {
			return "", nil, e
		}
		if other["feature_id"] != cp["feature_id"] {
			continue
		}
		if registered != cpRef || found != "" {
			return "", nil, s.Reject("PROFILE_INPUT_AMBIGUOUS", "同一功能未唯一登记当前 checkpoint")
		}
		found = path.Join(path.Dir(mapRef), ProgressionFile)
	}
	if found == "" {
		return "", nil, s.Reject("PROGRESSION_BINDING", "显式 checkpoint 尚未登记到 Tracker 功能包")
	}
	return found, cp, nil
}
