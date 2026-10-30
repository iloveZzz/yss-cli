package project

import (
	"bytes"
	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/nativeprofile"
	"github.com/iloveZzz/yss-cli/internal/worklayout"
	"go.yaml.in/yaml/v3"
	"strings"
)

func nativeProfileFile(f bundle.File, profile domain.Profile) (bundle.File, error) {
	return nativeprofile.Generate(f, profile)
}
func renderTracker(data []byte, tracker string) ([]byte, error) {
	switch tracker {
	case "", "local-markdown", "github", "gitlab":
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
	if tracker == "" {
		tracker = old
	}
	record["platform"] = tracker
	oldRoot, _ := record["root"].(string)
	record["root"] = worklayout.DefaultRoot
	record["legacy_roots"] = worklayout.HistoricalRoots
	encoded, e := yaml.Marshal(front)
	if e != nil {
		return nil, e
	}
	body := strings.Replace(string(parts[1]), "| `platform` | `"+old+"` |", "| `platform` | `"+tracker+"` |", 1)
	if oldRoot != ".work" && oldRoot != "" {
		body = strings.ReplaceAll(body, oldRoot, ".work")
	}
	return append(append(append([]byte("---\n"), encoded...), []byte("---\n")...), []byte(body)...), nil
}

// renderSpecTracker preserves the minimal config and binds explicit remote inputs.
func renderSpecTracker(data []byte, vars map[string]string, root string) ([]byte, error) {
	if !bytes.HasPrefix(data, []byte("---\n")) {
		return nil, domain.Fail("SPEC_TRACKER_CONFIG", "tracker frontmatter required")
	}
	parts := bytes.SplitN(data, []byte("\n---\n"), 2)
	if len(parts) != 2 {
		return nil, domain.Fail("SPEC_TRACKER_CONFIG", "tracker frontmatter incomplete")
	}
	var front map[string]any
	if err := yaml.Unmarshal(parts[0][4:], &front); err != nil {
		return nil, err
	}
	tracker, ok := front["tracker"].(map[string]any)
	if !ok {
		return nil, domain.Fail("SPEC_TRACKER_CONFIG", "tracker object required")
	}
	for variable, field := range map[string]string{"issueTracker": "platform", "trackerRepository": "repository", "trackerHost": "host", "trackerProject": "project"} {
		if value := vars[variable]; value != "" {
			tracker[field] = value
		}
	}
	encoded, err := yaml.Marshal(front)
	if err != nil {
		return nil, err
	}
	candidate := append(append(append([]byte("---\n"), encoded...), []byte("---\n")...), parts[1]...)
	if _, err := worklayout.ParseSpecTracker(root, candidate); err != nil {
		return nil, err
	}
	return candidate, nil
}
