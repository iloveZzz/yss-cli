package nativeprofile

import (
	"encoding/base64"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"go.yaml.in/yaml/v3"
)

func Generate(f bundle.File, profile domain.Profile) (bundle.File, error) {
	data, e := base64.StdEncoding.DecodeString(f.Data)
	if e != nil {
		return f, e
	}
	var value map[string]any
	if e = yaml.Unmarshal(data, &value); e != nil {
		return f, e
	}
	if value == nil {
		value = map[string]any{"schema_version": 2, "profile_id": profile.ID}
	}
	inst, _ := value["instantiation"].(map[string]any)
	if inst == nil {
		inst = map[string]any{}
	}
	inst["cli_package"] = "yss"
	inst["metadata_file"] = domain.MetadataFile
	inst["template_source"] = profile.TemplateSource
	inst["native_profile"] = profile.Name
	delete(inst, "npm_create")
	value["instantiation"] = inst
	data, e = yaml.Marshal(value)
	if e != nil {
		return f, e
	}
	f.Data = base64.StdEncoding.EncodeToString(data)
	f.Digest = safefs.Digest(data)
	f.Mode = 0644
	return f, nil
}
