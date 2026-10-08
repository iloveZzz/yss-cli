// Package helpview holds digest-bound reading views of fixed Bundle authority.
// It supplies presentation facts, never lifecycle approval or routing policy.
package helpview

import (
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"go.yaml.in/yaml/v3"
)

//go:embed assets
var assets embed.FS

type Stage struct {
	ID   string `json:"id" yaml:"id"`
	Name string `json:"name" yaml:"name"`
	Goal string `json:"goal" yaml:"goal"`
	Exit string `json:"exitCriteria" yaml:"exit_criteria"`
}

func (s *Stage) UnmarshalYAML(node *yaml.Node) error {
	type plain Stage
	var record struct {
		plain      `yaml:",inline"`
		PublicName string `yaml:"public_name"`
		PublicGoal string `yaml:"public_goal"`
		PublicExit string `yaml:"public_exit_criteria"`
	}
	if err := node.Decode(&record); err != nil {
		return err
	}
	*s = Stage(record.plain)
	if record.PublicName != "" {
		s.Name = record.PublicName
	}
	if record.PublicGoal != "" {
		s.Goal = record.PublicGoal
	}
	if record.PublicExit != "" {
		s.Exit = record.PublicExit
	}
	return nil
}

type View struct {
	SchemaVersion   int      `json:"schemaVersion"`
	Profile         string   `json:"profile"`
	TemplateCommit  string   `json:"templateCommit"`
	RegistrySHA256  string   `json:"registrySHA256"`
	ProfileSHA256   string   `json:"profileSHA256,omitempty"`
	PolicySHA256    string   `json:"policySHA256,omitempty"`
	Stages          []Stage  `json:"stages"`
	ReferenceStages []Stage  `json:"referenceStages,omitempty"`
	EntryWorkUnit   string   `json:"entryWorkUnit"`
	DailyEnabled    bool     `json:"dailyEnabled"`
	DailySequence   []string `json:"dailySequence,omitempty"`
}

func Load(profile string) (*View, error) {
	raw, e := assets.ReadFile("assets/" + profile + ".json")
	if e != nil {
		return nil, e
	}
	var v View
	if e = json.Unmarshal(raw, &v); e != nil {
		return nil, e
	}
	if v.SchemaVersion != 1 || v.Profile != profile || len(v.TemplateCommit) != 40 || len(v.RegistrySHA256) != 64 || len(v.Stages) == 0 {
		return nil, fmt.Errorf("离线生命周期视图来源不完整: %s", profile)
	}
	return &v, nil
}

// Derive is used by the build tool and provenance verification. Only the
// already locked, embedded Bundle is consumed, not a user's project directory.
func Derive(profile string) (*View, error) {
	b, e := bundle.Load(profile)
	if e != nil {
		return nil, e
	}
	read := func(ref string) ([]byte, error) {
		f, ok := b.Files[ref]
		if !ok {
			return nil, fmt.Errorf("固定 Bundle 缺少 %s", ref)
		}
		raw, e := base64.StdEncoding.DecodeString(f.Data)
		if e != nil || safefs.Digest(raw) != f.Digest {
			return nil, fmt.Errorf("固定 Bundle 文件摘要失配: %s", ref)
		}
		return raw, nil
	}
	raw, e := read(".template-spec/process/lifecycle-registry.yaml")
	if e != nil {
		return nil, e
	}
	var registry struct {
		Stages    []Stage `yaml:"stages"`
		WorkUnits []struct {
			ID string `yaml:"id"`
		} `yaml:"work_units"`
	}
	if e = yaml.Unmarshal(raw, &registry); e != nil {
		return nil, e
	}
	v := &View{SchemaVersion: 1, Profile: profile, TemplateCommit: b.TemplateCommit, RegistrySHA256: safefs.Digest(raw), Stages: registry.Stages}
	const profileRef = ".template-spec/process/harness-profile.yaml"
	if _, ok := b.Files[profileRef]; ok {
		profileRaw, err := read(profileRef)
		if err != nil {
			return nil, err
		}
		var spec struct {
			Lifecycle struct {
				Stages []string `yaml:"allowed_stages"`
				Entry  string   `yaml:"entry_work_unit"`
			} `yaml:"lifecycle"`
		}
		if err = yaml.Unmarshal(profileRaw, &spec); err != nil {
			return nil, err
		}
		if len(spec.Lifecycle.Stages) == 0 || spec.Lifecycle.Entry == "" {
			return nil, fmt.Errorf("固定 Profile 缺少生命周期路由: %s", profile)
		}
		byID := map[string]Stage{}
		for _, stage := range registry.Stages {
			byID[stage.ID] = stage
		}
		v.Stages = nil
		for _, id := range spec.Lifecycle.Stages {
			stage, ok := byID[id]
			if !ok {
				return nil, fmt.Errorf("固定 Profile 引用未登记阶段: %s", id)
			}
			v.Stages = append(v.Stages, stage)
			delete(byID, id)
		}
		for _, stage := range registry.Stages {
			if _, ok := byID[stage.ID]; ok {
				v.ReferenceStages = append(v.ReferenceStages, stage)
			}
		}
		v.ProfileSHA256 = safefs.Digest(profileRaw)
		v.EntryWorkUnit = spec.Lifecycle.Entry
	} else {
		for _, unit := range registry.WorkUnits {
			if unit.ID == "work-unit.entry-triage" {
				v.EntryWorkUnit = unit.ID
			}
		}
		if v.EntryWorkUnit == "" {
			return nil, fmt.Errorf("固定注册表缺少入口工作单元: %s", profile)
		}
	}
	const policyRef = ".agents/skills/yss-product-lifecycle/references/orchestration-contract.yaml"
	if _, ok := b.Files[policyRef]; ok {
		raw, e = read(policyRef)
		if e != nil {
			return nil, e
		}
		var policy struct {
			RequestTriage struct {
				DeliveryPath struct {
					Version         int      `yaml:"version"`
					EnabledProfiles []string `yaml:"enabled_profiles"`
					DailySequence   []string `yaml:"daily_sequence"`
				} `yaml:"delivery_path"`
			} `yaml:"request_triage"`
		}
		if e = yaml.Unmarshal(raw, &policy); e != nil {
			return nil, e
		}
		v.PolicySHA256 = safefs.Digest(raw)
		p := policy.RequestTriage.DeliveryPath
		for _, name := range p.EnabledProfiles {
			if name == profile && p.Version == 1 {
				v.DailyEnabled = true
				v.DailySequence = p.DailySequence
			}
		}
	}
	return v, nil
}
