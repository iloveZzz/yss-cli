package project

import (
	"encoding/base64"
	"slices"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/bundle"
)

func retiredBundle(contract string) *bundle.Bundle {
	file := func(text string) bundle.File {
		return bundle.File{Data: base64.StdEncoding.EncodeToString([]byte(text))}
	}
	b := &bundle.Bundle{
		Files: map[string]bundle.File{
			".agents/skills/tdd/SKILL.md": file("---\nname: tdd\n---\n"),
		},
		SkillRequirements: map[string]bundle.Requirement{"tdd": {}},
	}
	if contract != "" {
		b.Files["scripts/lib/skill-supply-chain.mjs"] = file(contract)
	}
	return b
}

func TestWithoutRetiredSkillsDropsOnlyRetiredMissingSkills(t *testing.T) {
	b := retiredBundle(`export const OBSOLETE = new Set(["grill-me"]);
for (const id of ["yss-ui", "ytable-usage"]) OBSOLETE.add(id);
`)
	got, err := withoutRetiredSkills(b, []string{"tdd", "yss-ui", "ytable-usage", "custom-local"})
	if err != nil {
		t.Fatal(err)
	}
	// custom-local is not retired, so it stays and the caller still reports it as missing.
	if want := []string{"tdd", "custom-local"}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestWithoutRetiredSkillsKeepsSelectionForBundlesWithoutContract(t *testing.T) {
	got, err := withoutRetiredSkills(retiredBundle(""), []string{"tdd", "yss-ui"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"tdd", "yss-ui"}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
