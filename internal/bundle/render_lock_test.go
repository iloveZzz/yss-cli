package bundle

import (
	"bytes"
	"encoding/json"
	"testing"
)

const orderedLockFixture = `{"version":3,"generatedBy":"scripts/update-skill-lock","canonicalRoot":".agents/skills","projectionRoots":[".codex/skills",".cursor/skills",".pi/skills"],"sources":{"z/upstream":{"revision":"fixed-z","future":{"z":9007199254740993,"a":"中文"}},"a/upstream":{"revision":"fixed-a"}},"skills":{"shared":{"alpha":{"source":"project","sourceType":"local","skillPath":".agents/skills/alpha/SKILL.md","effectiveHash":"alpha-hash","targets":[".agents/skills",".codex/skills",".cursor/skills",".pi/skills"]},"tdd":{"source":"z/upstream","sourceType":"github","skillPath":"skills/tdd/SKILL.md","effectiveHash":"tdd-hash","sourceRevision":"fixed-z","upstreamHash":"upstream-hash","adaptationRef":"docs/adaptation.md","future":{"flag":true,"number":9007199254740993},"targets":[".agents/skills",".codex/skills",".cursor/skills",".pi/skills"]}},"platform":{".codex/skills":{"codex-only":{"source":"project","targets":[".codex/skills"]}},".cursor/skills":{"cursor-only":{"source":"project","targets":[".cursor/skills"]}},".pi/skills":{"pi-only":{"source":"project","targets":[".pi/skills"]}}},"futureSection":{"z":"retained","a":null}},"futureRoot":{"z":[true,null,"中文"],"a":9007199254740993}}`

func lockGolden(t *testing.T, compact string) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := json.Indent(&b, []byte(compact), "", "  "); err != nil {
		t.Fatal(err)
	}
	return append(b.Bytes(), '\n')
}

func TestSelectedSourceLockCanonicalBytesAndUnknownMetadata(t *testing.T) {
	data := []byte(orderedLockFixture)
	before := append([]byte{}, data...)
	got, err := SelectedSourceLock(data, []string{"tdd", "codex-only"}, []string{"codex"})
	if err != nil {
		t.Fatal(err)
	}
	want := lockGolden(t, `{"version":3,"generatedBy":"scripts/update-skill-lock","canonicalRoot":".agents/skills","projectionRoots":[".codex/skills"],"sources":{"z/upstream":{"revision":"fixed-z","future":{"z":9007199254740993,"a":"中文"}},"a/upstream":{"revision":"fixed-a"}},"skills":{"shared":{"tdd":{"source":"z/upstream","sourceType":"github","skillPath":"skills/tdd/SKILL.md","effectiveHash":"tdd-hash","sourceRevision":"fixed-z","upstreamHash":"upstream-hash","adaptationRef":"docs/adaptation.md","future":{"flag":true,"number":9007199254740993},"targets":[".agents/skills",".codex/skills"]}},"platform":{".codex/skills":{"codex-only":{"source":"project","targets":[".codex/skills"]}}},"futureSection":{"z":"retained","a":null}},"futureRoot":{"z":[true,null,"中文"],"a":9007199254740993}}`)
	if !bytes.Equal(got, want) || !bytes.Equal(data, before) {
		t.Fatalf("canonical bytes, identities or unknown metadata changed:\n%s", got)
	}
	wrapped, err := selectedSourceLock(data, []string{"tdd", "codex-only"})
	if err != nil || !bytes.Equal(wrapped, want) {
		t.Fatalf("producer codex byte contract changed: %v\n%s", err, wrapped)
	}
}

func TestSelectedSourceLockPreservesRuntimeAndSourceSkillOrder(t *testing.T) {
	got, err := SelectedSourceLock([]byte(orderedLockFixture), []string{"tdd", "pi-only", "cursor-only", "alpha", "codex-only"}, []string{"pi", "cursor", "codex"})
	if err != nil {
		t.Fatal(err)
	}
	want := lockGolden(t, `{"version":3,"generatedBy":"scripts/update-skill-lock","canonicalRoot":".agents/skills","projectionRoots":[".pi/skills",".cursor/skills",".codex/skills"],"sources":{"z/upstream":{"revision":"fixed-z","future":{"z":9007199254740993,"a":"中文"}},"a/upstream":{"revision":"fixed-a"}},"skills":{"shared":{"alpha":{"source":"project","sourceType":"local","skillPath":".agents/skills/alpha/SKILL.md","effectiveHash":"alpha-hash","targets":[".agents/skills",".pi/skills",".cursor/skills",".codex/skills"]},"tdd":{"source":"z/upstream","sourceType":"github","skillPath":"skills/tdd/SKILL.md","effectiveHash":"tdd-hash","sourceRevision":"fixed-z","upstreamHash":"upstream-hash","adaptationRef":"docs/adaptation.md","future":{"flag":true,"number":9007199254740993},"targets":[".agents/skills",".pi/skills",".cursor/skills",".codex/skills"]}},"platform":{".pi/skills":{"pi-only":{"source":"project","targets":[".pi/skills"]}},".cursor/skills":{"cursor-only":{"source":"project","targets":[".cursor/skills"]}},".codex/skills":{"codex-only":{"source":"project","targets":[".codex/skills"]}}},"futureSection":{"z":"retained","a":null}},"futureRoot":{"z":[true,null,"中文"],"a":9007199254740993}}`)
	if !bytes.Equal(got, want) {
		t.Fatalf("runtime targets or source ordering changed:\n%s", got)
	}
}

func TestSelectedSourceLockRejectsInvalidInputsWithoutOutput(t *testing.T) {
	for _, x := range []struct {
		name, source string
		runtimes     []string
	}{
		{"empty-json", "", []string{"codex"}},
		{"root-array", `[]`, []string{"codex"}},
		{"skills-array", `{"skills":[]}`, []string{"codex"}},
		{"shared-missing", `{"skills":{}}`, []string{"codex"}},
		{"shared-array", `{"skills":{"shared":[]}}`, []string{"codex"}},
		{"shared-scalar-entry", `{"skills":{"shared":{"tdd":null}}}`, []string{"codex"}},
		{"platform-array", `{"skills":{"shared":{},"platform":[]}}`, []string{"codex"}},
		{"platform-group-array", `{"skills":{"shared":{},"platform":{".codex/skills":[]}}}`, []string{"codex"}},
		{"platform-scalar-entry", `{"skills":{"shared":{},"platform":{".codex/skills":{"tdd":false}}}}`, []string{"codex"}},
		{"sources-array", `{"sources":[],"skills":{"shared":{}}}`, []string{"codex"}},
		{"unsupported-version", `{"version":2,"skills":{"shared":{}}}`, []string{"codex"}},
		{"string-version", `{"version":"3","skills":{"shared":{}}}`, []string{"codex"}},
		{"trailing-json", `{"skills":{"shared":{}}} {}`, []string{"codex"}},
		{"duplicate-root", `{"skills":{"shared":{}},"skills":{"shared":{}}}`, []string{"codex"}},
		{"duplicate-skill", `{"skills":{"shared":{"tdd":{},"tdd":{}}}}`, []string{"codex"}},
		{"duplicate-entry-field", `{"skills":{"shared":{"tdd":{"targets":[],"targets":[]}}}}`, []string{"codex"}},
		{"unknown-runtime", orderedLockFixture, []string{"unknown"}},
		{"duplicate-runtime", orderedLockFixture, []string{"codex", "codex"}},
		{"missing-runtime", orderedLockFixture, nil},
	} {
		t.Run(x.name, func(t *testing.T) {
			data := []byte(x.source)
			before := append([]byte{}, data...)
			got, err := SelectedSourceLock(data, []string{"tdd"}, x.runtimes)
			if err == nil || got != nil || !bytes.Equal(data, before) {
				t.Fatalf("invalid selection produced output or changed input: %v %q", err, got)
			}
		})
	}
}
