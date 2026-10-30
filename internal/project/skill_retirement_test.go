package project

import (
	"encoding/base64"
	"fmt"
	"github.com/iloveZzz/yss-cli/internal/bundle"
	"reflect"
	"strings"
	"testing"
)

func retirementFile(text string) bundle.File {
	return bundle.File{Data: base64.StdEncoding.EncodeToString([]byte(text))}
}

const retirementV2 = `retirement_contract:
  schema_version: 2
  stages: [deprecated, retired]
  deprecated_new_use: forbidden
  cleanup_statuses: [migration-only, remove-ready]
  hard_retirement_tombstone: .template-spec/agents/skill-migrations.md
  retired_id_source: .template-spec/agents/yss-skill-registry.yaml#/retirement_contract/retired_ids
  failure_codes: {deprecated: skill-deprecated, retired: skill-retired}
  retired_ids: [research]
`

func TestRetiredSkillV2WithoutJavaScript(t *testing.T) {
	b := retiredBundle("")
	b.Files[skillRegistryRef] = retirementFile(retirementV2)
	got, present, err := readSkillRetirement(b)
	if err != nil || !present || !got["research"] || len(got) != 1 {
		t.Fatalf("%v %v %v", got, present, err)
	}
}

func TestRetiredSkillMalformedV2DoesNotFallback(t *testing.T) {
	cases := map[string]string{
		"null": "retirement_contract: null\n", "empty": "retirement_contract: {}\n",
		"version":        strings.Replace(retirementV2, "schema_version: 2", "schema_version: 7", 1),
		"string-version": strings.Replace(retirementV2, "schema_version: 2", "schema_version: '2'", 1),
		"missing":        strings.Replace(retirementV2, "  retired_ids: [research]\n", "", 1),
		"unknown":        retirementV2 + "  surprise: true\n",
		"null-ids":       strings.Replace(retirementV2, "[research]", "null", 1),
		"scalar-ids":     strings.Replace(retirementV2, "[research]", "research", 1),
		"non-string":     strings.Replace(retirementV2, "[research]", "[2]", 1),
		"invalid-id":     strings.Replace(retirementV2, "[research]", "[Bad]", 1),
		"duplicate-id":   strings.Replace(retirementV2, "[research]", "[research, research]", 1),
		"duplicate-key":  retirementV2 + "  schema_version: 2\n",
		"source":         strings.Replace(retirementV2, ".template-spec/agents/yss-skill-registry.yaml#/retirement_contract/retired_ids", "scripts/lib/skill-supply-chain.mjs#OBSOLETE", 1),
		"anchor":         strings.Replace(retirementV2, "[research]", "&ids [research]", 1),
		"alias":          "old: &ids [research]\n" + strings.Replace(retirementV2, "[research]", "*ids", 1),
		"tag":            strings.Replace(retirementV2, "[research]", "!!seq [research]", 1),
		"stages":         strings.Replace(retirementV2, "[deprecated, retired]", "[retired]", 1),
		"new-use":        strings.Replace(retirementV2, "deprecated_new_use: forbidden", "deprecated_new_use: allowed", 1),
		"cleanup":        strings.Replace(retirementV2, "[migration-only, remove-ready]", "[]", 1),
		"tombstone":      strings.Replace(retirementV2, "hard_retirement_tombstone: .template-spec/agents/skill-migrations.md", "hard_retirement_tombstone: elsewhere", 1),
		"failure-code":   strings.Replace(retirementV2, "retired: skill-retired", "retired: changed", 1),
	}
	for name, registry := range cases {
		t.Run(name, func(t *testing.T) {
			b := retiredBundle(`export const OBSOLETE = new Set(["old"]);`)
			b.Files[skillRegistryRef] = retirementFile(registry)
			if _, _, err := readSkillRetirement(b); err == nil {
				t.Fatal("malformed v2 accepted or fell back")
			}
		})
	}
}
func TestRetiredSkillLegacyAndAbsent(t *testing.T) {
	js := `export const OBSOLETE = new Set(["old", "to-" + "prd"]); for (const id of ["loop"]) OBSOLETE.add(id); OBSOLETE.add("single");`
	for _, registry := range []string{"", strings.Replace(strings.Replace(strings.Replace(retirementV2, "schema_version: 2", "schema_version: 1", 1), ".template-spec/agents/yss-skill-registry.yaml#/retirement_contract/retired_ids", "scripts/lib/skill-supply-chain.mjs#OBSOLETE", 1), "  retired_ids: [research]\n", "", 1)} {
		b := retiredBundle(js)
		if registry != "" {
			b.Files[skillRegistryRef] = retirementFile(registry)
		}
		got, present, err := readSkillRetirement(b)
		if err != nil || !present || !reflect.DeepEqual(got, map[string]bool{"old": true, "to-prd": true, "loop": true, "single": true}) {
			t.Fatalf("%v %v %v", got, present, err)
		}
	}
	if _, present, err := readSkillRetirement(retiredBundle("")); err != nil || present {
		t.Fatalf("absent: %v %v", present, err)
	}
	b := retiredBundle("")
	b.Files[skillRegistryRef] = retirementFile(strings.Replace(retirementV2, "[research]", "[]", 1))
	if got, present, err := readSkillRetirement(b); err != nil || !present || len(got) != 0 {
		t.Fatalf("empty: %v %v %v", got, present, err)
	}
}
func TestRetiredSkillV2RejectsCanonicalTreeLockAndRequirementConflicts(t *testing.T) {
	for _, kind := range []string{"registry", "platform", "external-local", "tree", "lock", "requirement"} {
		t.Run(kind, func(t *testing.T) {
			b := retiredBundle("")
			registry := retirementV2
			switch kind {
			case "registry":
				registry += "skills: [{id: research}]\n"
			case "platform":
				registry += "platform_skills: [{id: research}]\n"
			case "external-local":
				registry += "external_skills: [{id: research, source: .agents/skills/research}]\n"
			case "tree":
				b.Files[".agents/skills/research/SKILL.md"] = retirementFile("retired")
			case "lock":
				b.Files["skills-lock.json"] = retirementFile(`{"skills":{"shared":{"research":{}}}}`)
			case "requirement":
				b.SkillRequirements["research"] = bundle.Requirement{}
			}
			b.Files[skillRegistryRef] = retirementFile(registry)
			if _, _, err := readSkillRetirement(b); err == nil {
				t.Fatal("retired local skill accepted")
			}
		})
	}
	b := retiredBundle("")
	b.Files[skillRegistryRef] = retirementFile(strings.Replace(retirementV2, "[research]", "[yss-ui, yss-formily, yss-frontend-scaffold-generator]", 1) + "external_skills: [{id: yss-ui, source: cross-repo-reference}, {id: yss-formily, source: cross-repo-reference}, {id: yss-frontend-scaffold-generator, source: cross-repo-reference}]\n")
	if _, _, err := readSkillRetirement(b); err != nil {
		t.Fatal(err)
	}
}

func researchSelectionBundle(t *testing.T, v2 bool) *bundle.Bundle {
	t.Helper()
	b := selectionFixture(t)
	raw, err := b.Files[skillRegistryRef].Render(nil)
	if err != nil {
		t.Fatal(err)
	}
	registry := strings.ReplaceAll(strings.ReplaceAll(string(raw), "id: root,", "id: yss-research,"), "[r]", "[research]")
	if v2 {
		registry += "\n" + retirementV2
		delete(b.Files, retirementJavaScript)
	} else {
		b.Files[retirementJavaScript] = retirementFile(`export const OBSOLETE = new Set(["research"]);`)
	}
	b.Files[skillRegistryRef] = retirementFile(registry)
	b.Files[".agents/skills/yss-research/SKILL.md"] = retirementFile("---\nname: yss-research\ndescription: Fixture\n---\n")
	return b
}
func TestRetiredSkillResearchAliasSelectionAndMigration(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		t.Run(fmt.Sprint(v2), func(t *testing.T) {
			b := researchSelectionBundle(t, v2)
			selected, err := resolveSkillSelection(b, []string{"research"}, "")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(selected.CanonicalIDs, []string{"yss-research"}) {
				t.Fatal(selected)
			}
			got, err := withoutRetiredSkills(b, []string{"research", "custom-local"})
			if err != nil || !reflect.DeepEqual(got, []string{"yss-research", "custom-local"}) {
				t.Fatalf("%v %v", got, err)
			}
		})
	}
}
func TestRetiredSkillAliasRefusesInvalidTargets(t *testing.T) {
	for _, kind := range []string{"ambiguous", "chain", "deprecated", "retired", "missing-tree"} {
		t.Run(kind, func(t *testing.T) {
			b := researchSelectionBundle(t, false)
			raw, _ := b.Files[skillRegistryRef].Render(nil)
			registry := string(raw)
			switch kind {
			case "ambiguous":
				registry = strings.Replace(registry, "id: required, layer: core, maturity: supported, aliases: []", "id: required, layer: core, maturity: supported, aliases: [research]", 1)
			case "chain":
				registry = strings.Replace(registry, "id: required, layer: core, maturity: supported, aliases: []", "id: required, layer: core, maturity: supported, aliases: [yss-research]", 1)
			case "deprecated":
				registry = strings.Replace(registry, "id: yss-research, layer: core, maturity: supported", "id: yss-research, layer: core, maturity: deprecated", 1)
			case "retired":
				b.Files[retirementJavaScript] = retirementFile(`export const OBSOLETE = new Set(["yss-research"]);`)
			case "missing-tree":
				delete(b.Files, ".agents/skills/yss-research/SKILL.md")
			}
			b.Files[skillRegistryRef] = retirementFile(registry)
			if _, err := resolveSkillSelection(b, []string{"research"}, ""); err == nil {
				t.Fatal("invalid alias accepted")
			}
		})
	}
}
func TestRetiredSkillMigrationValidatesBeforeExistingTreeShortCircuit(t *testing.T) {
	b := retiredBundle("")
	b.Files[skillRegistryRef] = retirementFile("retirement_contract: null\n")
	if _, err := withoutRetiredSkills(b, []string{"tdd"}); err == nil {
		t.Fatal("invalid contract hidden by existing tree")
	}
}

func TestRetiredSkillLegacyRefusesConflictingPhysicalTreeBeforeMigration(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		b := retiredBundle(`export const OBSOLETE = new Set(["tdd"]);`)
		if explicit {
			text := strings.ReplaceAll(retirementV2, "schema_version: 2", "schema_version: 1")
			text = strings.ReplaceAll(text, retirementSource, legacyRetirementSource)
			text = strings.ReplaceAll(text, "  retired_ids: [research]\n", "")
			b.Files[skillRegistryRef] = retirementFile(text)
		}
		if _, err := withoutRetiredSkills(b, []string{"tdd"}); err == nil {
			t.Fatalf("explicit=%v: accepted conflicting legacy tree", explicit)
		}
	}
}

func TestRetiredSkillAliasCannotShadowExternalOrPlatformIdentity(t *testing.T) {
	for _, group := range []string{"external_skills", "platform_skills"} {
		for _, id := range []string{"yss-ui", "yss-formily", "yss-frontend-scaffold-generator"} {
			b := selectionFixture(t)
			raw, _ := b.Files[skillRegistryRef].Render(nil)
			text := strings.Replace(string(raw), "aliases: [r]", "aliases: ["+id+"]", 1)
			text += "\n" + group + ":\n  - id: " + id + "\n    source: cross-repo-reference\n"
			b.Files[skillRegistryRef] = retirementFile(text)
			if _, err := resolveSkillSelection(b, []string{id}, ""); err == nil {
				t.Fatalf("accepted %s alias collision %s", group, id)
			}
		}
	}
}
