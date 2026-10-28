package bundle

import (
	"encoding/json"
	"testing"
)

func TestCloneBundleDetachesInstallationContents(t *testing.T) {
	source := packedInstallationFixture(t)
	item := source.Installations["skills"]
	item.SkillReferences = map[string][]string{"probe": {".template-spec/example.md"}}
	item.ReferenceGroups = map[string][]string{"shared": {".template-spec/group.md"}}
	item.SkillReferenceGroups = map[string][]string{"probe": {"shared"}}
	item.Distribution["runtimes"] = []any{"claude"}
	source.Installations["skills"] = item
	before, _ := json.Marshal(source)
	copy := cloneBundle(source)
	modified := copy.Installations["skills"]
	delete(modified.Initial, "AGENTS.md")
	modified.Distribution["runtimes"].([]any)[0] = "changed"
	modified.SkillReferences["probe"][0] = "changed"
	modified.ReferenceGroups["shared"][0] = "changed"
	modified.SkillReferenceGroups["probe"][0] = "changed"
	copy.Installations["skills"] = modified
	after, _ := json.Marshal(source)
	if string(before) != string(after) {
		t.Fatal("clone mutated original installation containers")
	}
}
