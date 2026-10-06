package project

import (
	"bytes"
	"encoding/base64"
	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"go.yaml.in/yaml/v3"
	"strings"
)

func nativeProfileFile(f bundle.File, profile domain.Profile) (bundle.File, error) {
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
	inst["metadata_file"] = MetadataFile
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
func renderTracker(data []byte, tracker string) ([]byte, error) {
	if tracker == "" {
		return data, nil
	}
	switch tracker {
	case "local-markdown", "github", "gitlab":
	default:
		return nil, domain.Fail("ARGUMENT", "unknown issueTracker")
	}
	if !bytes.HasPrefix(data, []byte("---\n")) {
		return nil, domain.Fail("BUNDLE", "tracker asset is missing front matter")
	}
	parts := bytes.SplitN(data, []byte("\n---\n"), 2)
	if len(parts) != 2 {
		return nil, domain.Fail("BUNDLE", "tracker asset front matter is incomplete")
	}
	var front map[string]any
	if e := yaml.Unmarshal(parts[0][4:], &front); e != nil {
		return nil, e
	}
	record, _ := front["tracker"].(map[string]any)
	if record == nil {
		return nil, domain.Fail("BUNDLE", "tracker authority is missing")
	}
	old, _ := record["platform"].(string)
	record["platform"] = tracker
	encoded, e := yaml.Marshal(front)
	if e != nil {
		return nil, e
	}
	body := strings.Replace(string(parts[1]), "| `platform` | `"+old+"` |", "| `platform` | `"+tracker+"` |", 1)
	return append(append(append([]byte("---\n"), encoded...), []byte("---\n")...), []byte(body)...), nil
}
