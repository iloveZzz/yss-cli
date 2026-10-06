package project

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func TestSelectedLockFixedBundleMatchesInitialBytes(t *testing.T) {
	b, err := bundle.Load("spec")
	if err != nil {
		t.Fatal(err)
	}
	f, err := selectedLock(b, b.Distribution)
	if err != nil {
		t.Fatal(err)
	}
	got, err := base64.StdEncoding.DecodeString(f.Data)
	if err != nil {
		t.Fatal(err)
	}
	want, err := base64.StdEncoding.DecodeString(b.Initial["skills-lock.json"].Data)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("selected lock must preserve fixed source canonical bytes; got prefix %q want %q", got[:min(120, len(got))], want[:min(120, len(want))])
	}
	if f.Digest != b.Initial["skills-lock.json"].Digest || f.Mode != b.Files["skills-lock.json"].Mode || f.Ownership != b.Files["skills-lock.json"].Ownership {
		t.Fatal("selected lock changed source file policy or digest")
	}
}

func TestSelectedLockStageSkillUnionAndRuntimeCanonicalBytes(t *testing.T) {
	source := []byte(`{"version":3,"generatedBy":"scripts/update-skill-lock","canonicalRoot":".agents/skills","projectionRoots":[".codex/skills",".cursor/skills",".pi/skills"],"sources":{"upstream":{"revision":"fixed","future":{"z":9007199254740993,"a":"中文"}}},"skills":{"shared":{"entry":{"source":"project","effectiveHash":"entry-hash","targets":[".agents/skills",".codex/skills",".cursor/skills",".pi/skills"]},"tdd":{"source":"upstream","sourceRevision":"fixed","effectiveHash":"tdd-hash","targets":[".agents/skills",".codex/skills",".cursor/skills",".pi/skills"]}},"platform":{}},"future":{"z":true,"a":null}}`)
	b := &bundle.Bundle{Files: map[string]bundle.File{"skills-lock.json": {Data: base64.StdEncoding.EncodeToString(source), Digest: safefs.Digest(source), Mode: 0644, Ownership: "generated"}}, StageRequirements: map[string]bundle.Requirement{"stage.next": {Paths: []string{".template-spec/next.yaml"}}}}
	profile, err := domain.GetProfile("spec")
	if err != nil {
		t.Fatal(err)
	}
	id := &Identity{Profile: profile, Native: &Metadata{Distribution: map[string]any{"mode": "selected", "installedSkills": []string{"entry"}, "installedStages": []string{"stage.entry"}, "runtimes": []string{"pi", "codex"}}}}
	d, err := distributionFor(id, b, []string{".agents/skills/tdd/SKILL.md", ".template-spec/next.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stringsOf(d["installedSkills"]), []string{"entry", "tdd"}) || !reflect.DeepEqual(stringsOf(d["installedStages"]), []string{"stage.entry", "stage.next"}) {
		t.Fatalf("stage/Skill union changed: %+v", d)
	}
	f, err := selectedLock(b, d)
	if err != nil {
		t.Fatal(err)
	}
	got, err := base64.StdEncoding.DecodeString(f.Data)
	if err != nil {
		t.Fatal(err)
	}
	compact := []byte(`{"version":3,"generatedBy":"scripts/update-skill-lock","canonicalRoot":".agents/skills","projectionRoots":[".pi/skills",".codex/skills"],"sources":{"upstream":{"revision":"fixed","future":{"z":9007199254740993,"a":"中文"}}},"skills":{"shared":{"entry":{"source":"project","effectiveHash":"entry-hash","targets":[".agents/skills",".pi/skills",".codex/skills"]},"tdd":{"source":"upstream","sourceRevision":"fixed","effectiveHash":"tdd-hash","targets":[".agents/skills",".pi/skills",".codex/skills"]}},"platform":{}},"future":{"z":true,"a":null}}`)
	var want bytes.Buffer
	if err := json.Indent(&want, compact, "", "  "); err != nil {
		t.Fatal(err)
	}
	want.WriteByte('\n')
	if !bytes.Equal(got, want.Bytes()) || f.Digest != safefs.Digest(got) || f.Mode != 0644 || f.Ownership != "generated" {
		t.Fatalf("canonical derived lock contract changed:\n%s", got)
	}
	if b.Files["skills-lock.json"].Data != base64.StdEncoding.EncodeToString(source) || !reflect.DeepEqual(stringsOf(id.Native.Distribution["installedSkills"]), []string{"entry"}) {
		t.Fatal("selection mutated the fixed Bundle or installed baseline")
	}
}
