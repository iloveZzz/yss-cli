package bundle

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func renderedLockSource(t *testing.T, profile string) Source {
	t.Helper()
	root, _, _ := fixtureGit(t)
	p := Policy{SchemaVersion: 1, Profile: profile, Manifest: map[string]any{"allowRootEntries": []string{".agents", ".codex", ".template-spec"}, "allowRootFiles": []string{"README.md", "yss-project.yaml", "skills-lock.json"}, "renderPaths": []string{"README.md", "yss-project.yaml"}}, Common: Entries{Files: []string{".template-spec/agents/yss-skill-registry.yaml", ".template-spec/process/lifecycle-registry.yaml", "skills-lock.json"}}, Stages: map[string]Entries{"stage.entry-triage": {}, "stage.plan": {}}}
	policy, _ := json.Marshal(p)
	lock := []byte(`{"version":3,"generatedBy":"scripts/update-skill-lock","canonicalRoot":".agents/skills","projectionRoots":[".codex/skills"],"sources":{"fixture":{"revision":"fixed-source","future":{"z":true,"a":null}}},"skills":{"shared":{"demo":{"source":"fixture","sourceType":"github","skillPath":"skills/demo/SKILL.md","effectiveHash":"58cbcb806e10657eb5ae7ea55c987f8f796fba566ed561bcca5f26d839d5489c","sourceRevision":"fixed-source","upstreamHash":"58cbcb806e10657eb5ae7ea55c987f8f796fba566ed561bcca5f26d839d5489c","adaptationRef":"docs/adaptation.md","targets":[".agents/skills",".codex/skills"]}},"platform":{}}}`)
	files := map[string][]byte{".template-source/distribution/bundle-profile.json": policy, "skills-lock.json": lock, ".template-spec/agents/yss-skill-registry.yaml": []byte("instance_distribution:\n  initial_skills: [demo]\n"), ".template-spec/process/lifecycle-registry.yaml": []byte("stages:\n  - id: stage.entry-triage\n  - id: stage.plan\n"), ".agents/skills/demo/SKILL.md": []byte("# demo\n`create-yss-spec doctor`\n"), ".codex/skills/demo/SKILL.md": []byte("# demo\n`create-yss-spec doctor`\n")}
	for ref, data := range files {
		file := filepath.Join(root, filepath.FromSlash(ref))
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-qm", "rendered Skill source"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git fixture: %s %v", out, err)
		}
	}
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = root
	commit, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return Source{Profile: profile, Root: root, Commit: string(bytes.TrimSpace(commit)), PolicyPath: ".template-source/distribution/bundle-profile.json", PolicyHash: safefs.Digest(policy)}
}

func TestBuiltSkillLockUsesRenderedBytesAndKeepsRawProvenance(t *testing.T) {
	for _, profile := range []string{"spec", "design", "backend", "frontend"} {
		t.Run(profile, func(t *testing.T) {
			source := renderedLockSource(t, profile)
			b, err := Build(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			for name, files := range map[string]map[string]File{"full": b.Files, "initial": b.Initial} {
				raw, err := base64.StdEncoding.DecodeString(files["skills-lock.json"].Data)
				if err != nil {
					t.Fatal(err)
				}
				var lock map[string]any
				if err := json.Unmarshal(raw, &lock); err != nil {
					t.Fatal(err)
				}
				entry := lock["skills"].(map[string]any)["shared"].(map[string]any)["demo"].(map[string]any)
				if entry["effectiveHash"] != "2bc82770cf36d3d10730ad5113f5efe733b1c6b290e0db2becb23861c506a8a3" {
					t.Fatalf("%s lock hashes raw source instead of distributed Skill bytes: %+v", name, entry)
				}
				if entry["upstreamHash"] != "58cbcb806e10657eb5ae7ea55c987f8f796fba566ed561bcca5f26d839d5489c" || entry["sourceRevision"] != "fixed-source" {
					t.Fatal("rendered hash replaced upstream/source lineage")
				}
			}
			repeat, err := Build(context.Background(), source)
			if err != nil || !reflect.DeepEqual(b, repeat) {
				t.Fatalf("fixed source build is not deterministic: %v", err)
			}
			policyBytes, err := git(context.Background(), source.Root, "show", source.Commit+":"+source.PolicyPath)
			if err != nil {
				t.Fatal(err)
			}
			var policy Policy
			if err := json.Unmarshal(policyBytes, &policy); err != nil {
				t.Fatal(err)
			}
			raw, err := readGitFiles(context.Background(), source.Root, source.Commit, policy.Manifest)
			if err != nil {
				t.Fatal(err)
			}
			snapshot := map[string]File{}
			for ref, f := range raw {
				snapshot[ref] = encodedFile(f.data, f.mode, "")
			}
			snapshotBytes, _ := json.Marshal(snapshot)
			if b.SnapshotHash != safefs.Digest(snapshotBytes) {
				t.Fatal("effective hash correction changed raw source snapshot identity")
			}
		})
	}
}

func TestSkillTreeHashNormalizedGoldenAndFinalVariables(t *testing.T) {
	files := map[string]File{
		".agents/skills/demo/a.md":  encodedFile([]byte("alpha\r\nbeta\r\n"), 0644, "managed"),
		".agents/skills/demo/b.bin": encodedFile([]byte{0, '\r', '\n'}, 0644, "managed"),
		".agents/skills/demo/c.md":  encodedFile([]byte("last\n"), 0755, "managed"),
	}
	for _, ignored := range []string{".DS_Store", "nested/.DS_Store", "__pycache__/cache", "cache.pyc", "cache.pyo", "project.iml"} {
		files[".agents/skills/demo/"+ignored] = File{Data: "ignored invalid descriptor"}
	}
	got, err := SkillTreeHash(files, ".agents/skills/demo", nil)
	if err != nil || got != "110cccc9b85c23508ee595413dbe7b6ce877030ad6de57016499a0fb0d1d70e2" {
		t.Fatalf("CRLF/binary/exclusion tree contract: %s %v", got, err)
	}
	files = map[string]File{".agents/skills/demo/SKILL.md": encodedFile([]byte("Hello __YSS_PROJECT_NAME__ / __YSS_BUSINESS_DOMAIN__ / __YSS_TEAM_SIZE__\r\n"), 0644, "managed")}
	for _, vars := range []map[string]string{nil, {"projectName": "Acme"}} {
		if _, err := SkillTreeHash(files, ".agents/skills/demo", vars); err == nil {
			t.Fatal("unbound instance token silently erased")
		}
	}
	got, err = SkillTreeHash(files, ".agents/skills/demo", map[string]string{"projectName": "Acme", "businessDomain": "Commerce", "teamSize": "5"})
	if err != nil || got != "04bd8c0da92f9f7577dbb7ba1bdb75cece3622b41ad0a0bae6835ba3dd3420b0" {
		t.Fatalf("final instance variable contract: %s %v", got, err)
	}
}

func TestRenderedSkillLockPreservesOrderedSourceMetadataAndModes(t *testing.T) {
	raw := []byte(`{"version":3,"future":{"z":1,"a":9007199254740993},"skills":{"shared":{"demo":{"source":"fixed","effectiveHash":"old","upstreamHash":"upstream","targets":[".agents/skills",".codex/skills"],"future":{"z":null,"a":true}}},"platform":{}}}`)
	files := map[string]File{".agents/skills/demo/SKILL.md": encodedFile([]byte("# demo\n`yss doctor`\n"), 0755, "managed"), ".codex/skills/demo/SKILL.md": encodedFile([]byte("# demo\n`yss doctor`\n"), 0755, "managed"), "skills-lock.json": encodedFile(raw, 0644, "generated")}
	want := bytes.NewBuffer(nil)
	if err := json.Indent(want, bytes.Replace(raw, []byte(`"effectiveHash":"old"`), []byte(`"effectiveHash":"2bc82770cf36d3d10730ad5113f5efe733b1c6b290e0db2becb23861c506a8a3"`), 1), "", "  "); err != nil {
		t.Fatal(err)
	}
	want.WriteByte('\n')
	if err := bindBuiltSkillLock(files); err != nil {
		t.Fatal(err)
	}
	got, _ := base64.StdEncoding.DecodeString(files["skills-lock.json"].Data)
	if !bytes.Equal(got, want.Bytes()) {
		t.Fatalf("canonical field order/source metadata changed:\n%s", got)
	}
	if f := files["skills-lock.json"]; f.Mode != 0644 || f.Ownership != "generated" || f.Digest != safefs.Digest(got) {
		t.Fatal("effective hash correction changed lock mode/ownership/digest")
	}
}

// Fixed spec a18b89e7: rendered tree hash independently checked with Node treeHash.
func TestRenderedSkillLockCurrentFullSpecGolden(t *testing.T) {
	b, err := Load("spec")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.StdEncoding.DecodeString(b.Files["skills-lock.json"].Data)
	got, err := RenderedSkillLock(raw, b.Files, nil)
	if err != nil {
		t.Fatal(err)
	}
	var lock map[string]any
	if err := json.Unmarshal(got, &lock); err != nil {
		t.Fatal(err)
	}
	skills := lock["skills"].(map[string]any)
	if entry := skills["shared"].(map[string]any)["yss-design-system"].(map[string]any); entry["effectiveHash"] != "3207008f4b7975747cd32352a643b8f35769e7a971ae8b40ba320024e9118546" {
		t.Fatalf("rendered design-system golden mismatch: %+v", entry)
	}
	if entry := skills["platform"].(map[string]any)[".codex/skills"].(map[string]any)["product-design"].(map[string]any); entry["effectiveHash"] != "5e49d34d508ea4aa4af203d45337d43e3137b9bb0c86531be693c62593fc4c08" {
		t.Fatalf("nested platform group golden mismatch: %+v", entry)
	}
}

func TestRenderedSkillLockRejectsInvalidSourceOrProjection(t *testing.T) {
	base := `{"version":3,"skills":{"shared":{"demo":{"effectiveHash":"old","targets":[".agents/skills",".codex/skills"]}},"platform":{}}}`
	for _, name := range []string{"version", "duplicate", "trailing", "entry", "targets", "root", "platform", "missing-tree", "projection", "descriptor", "path"} {
		t.Run(name, func(t *testing.T) {
			raw := base
			files := map[string]File{".agents/skills/demo/SKILL.md": encodedFile([]byte("demo\n"), 0644, "managed"), ".codex/skills/demo/SKILL.md": encodedFile([]byte("demo\n"), 0644, "managed")}
			switch name {
			case "version":
				raw = strings.Replace(raw, `"version":3`, `"version":4`, 1)
			case "duplicate":
				raw = strings.Replace(raw, `"version":3`, `"version":3,"version":3`, 1)
			case "trailing":
				raw += `{}`
			case "entry":
				raw = `{"version":3,"skills":{"shared":{"demo":null},"platform":{}}}`
			case "targets":
				raw = strings.Replace(raw, `[".agents/skills",".codex/skills"]`, `null`, 1)
			case "root":
				raw = strings.Replace(raw, `.codex/skills`, `.unknown/skills`, 1)
			case "platform":
				raw = strings.Replace(raw, `"platform":{}`, `"platform":{".codex/skills":{"missing":{}}}`, 1)
			case "missing-tree":
				delete(files, ".agents/skills/demo/SKILL.md")
			case "projection":
				files[".codex/skills/demo/SKILL.md"] = encodedFile([]byte("different\n"), 0644, "managed")
			case "descriptor":
				files[".agents/skills/demo/SKILL.md"] = File{Data: "broken", Digest: "broken"}
			case "path":
				files[".agents/skills/demo/../bad"] = encodedFile([]byte("bad"), 0644, "managed")
			}
			if _, err := RenderedSkillLock([]byte(raw), files, nil); err == nil {
				t.Fatal("invalid fixed source/Skill projection accepted")
			}
		})
	}
}
