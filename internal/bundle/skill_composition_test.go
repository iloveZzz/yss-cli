package bundle

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func compositionFixture(t *testing.T) (Source, string) {
	t.Helper()
	root, _, _ := fixtureGit(t)
	shared, _, _ := fixtureGit(t)
	put := func(dir, ref string, data []byte, mode os.FileMode) {
		t.Helper()
		p := filepath.Join(dir, ref)
		if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(p, data, mode); e != nil {
			t.Fatal(e)
		}
	}
	sourceData := []byte("owner: shared\n")
	put(shared, ".agents/skills/shared/SKILL.md", sourceData, 0644)
	put(shared, ".agents/skills/shared/probe", []byte("#!/bin/sh\n"), 0755)
	cfg := []byte(`{"schema_version":1,"profiles":{"backend":{"materialization":"generated","exact":[{"id":"shared"}],"adapted":[],"local_only":["local"]}}}`)
	put(shared, ".template-source/profile-skill-sync.json", cfg, 0644)
	commit := func(dir string) string {
		t.Helper()
		for _, args := range [][]string{{"add", "."}, {"commit", "-qm", "composition fixture"}} {
			c := exec.Command("git", args...)
			c.Dir = dir
			if o, e := c.CombinedOutput(); e != nil {
				t.Fatalf("%s: %v", o, e)
			}
		}
		c := exec.Command("git", "rev-parse", "HEAD")
		c.Dir = dir
		o, e := c.Output()
		if e != nil {
			t.Fatal(e)
		}
		return string(o[:len(o)-1])
	}
	sharedCommit := commit(shared)
	expected := map[string]sourceFile{".agents/skills/shared/SKILL.md": {data: sourceData, mode: 0644}, ".agents/skills/shared/probe": {data: []byte("#!/bin/sh\n"), mode: 0755}}
	sourceLock, _ := json.Marshal(map[string]any{"schemaVersion": 1, "profile": "backend", "sourceCommit": sharedCommit, "sourceState": "committed", "configurationPath": ".template-source/profile-skill-sync.json", "configurationHash": safefs.Digest(cfg), "skillsDigest": compositionDigest(expected)})
	put(root, ".template-source/profile-skills-source.json", sourceLock, 0644)
	put(root, ".agents/skills/local/SKILL.md", []byte("local\n"), 0644)
	policy := Policy{SchemaVersion: 1, Profile: "backend", Manifest: map[string]any{"allowRootEntries": []string{"scripts", ".agents", ".codex", ".cursor", ".pi"}, "allowRootFiles": []string{"README.md", "yss-project.yaml"}, "renderPaths": []string{"README.md", "yss-project.yaml"}}}
	raw, _ := json.Marshal(policy)
	put(root, ".template-source/distribution/bundle-profile.json", raw, 0644)
	targetCommit := commit(root)
	return Source{Profile: "backend", Root: root, Commit: targetCommit, PolicyHash: safefs.Digest(raw), SkillsSource: &SkillSource{Root: shared, Commit: sharedCommit, ConfigurationPath: ".template-source/profile-skill-sync.json", ConfigurationHash: safefs.Digest(cfg)}}, shared
}

func TestBuildComposesCommittedSkillsWithoutTrackedCopies(t *testing.T) {
	src, shared := compositionFixture(t)
	first, e := Build(context.Background(), src)
	if e != nil {
		t.Fatal(e)
	}
	for _, runtime := range []string{".agents", ".codex", ".cursor", ".pi"} {
		f, ok := first.Files[runtime+"/skills/shared/SKILL.md"]
		if !ok {
			t.Fatal("missing generated skill", runtime)
		}
		data, _ := base64.StdEncoding.DecodeString(f.Data)
		if string(data) != "owner: shared\n" {
			t.Fatal("wrong shared content")
		}
	}
	if first.Files[".agents/skills/shared/probe"].Mode != 0755 {
		t.Fatal("mode lost")
	}
	if _, ok := first.Files[".agents/skills/local/SKILL.md"]; !ok {
		t.Fatal("local skill lost")
	}
	if first.Manifest["skillComposition"] == nil {
		t.Fatal("composition provenance missing")
	}
	os.WriteFile(filepath.Join(shared, ".agents/skills/shared/SKILL.md"), []byte("dirty source\n"), 0644)
	os.MkdirAll(filepath.Join(src.Root, ".agents/skills/shared"), 0755)
	os.WriteFile(filepath.Join(src.Root, ".agents/skills/shared/SKILL.md"), []byte("dirty target\n"), 0644)
	repeat, e := Build(context.Background(), src)
	if e != nil {
		t.Fatal(e)
	}
	if repeat.BundleHash != first.BundleHash {
		t.Fatal("build read dirty worktree")
	}
	src.SkillsSource.ConfigurationHash = "bad"
	if _, e := Build(context.Background(), src); e == nil {
		t.Fatal("configuration drift accepted")
	}
}

func TestDistributedGitignoreKeepsManagedSkillsVisible(t *testing.T) {
	raw := []byte("user-rule\n\n# Generated shared skills; authority: Spec profile-skill-sync.json\n/.agents/skills/shared/\n# End generated shared skills\n")
	for _, profile := range []string{"backend", "frontend"} {
		got, err := prepareSource(profile, ".gitignore", raw)
		if err != nil || string(got) != "user-rule\n" {
			t.Fatalf("source-only ignore leaked into %s instance: %q", profile, got)
		}
	}
}

func TestSkillPatchPreservesResourcesModesAndRejectsEscape(t *testing.T) {
	files := map[string]sourceFile{"SKILL.md": {data: []byte("shared\n"), mode: 0644}, "probe": {data: []byte("#!/bin/sh\n"), mode: 0755}}
	patch := []byte("diff --git a/SKILL.md b/SKILL.md\n--- a/SKILL.md\n+++ b/SKILL.md\n@@ -1 +1 @@\n-shared\n+adapted\n")
	got, e := applySkillPatch(context.Background(), files, patch)
	if e != nil {
		t.Fatal(e)
	}
	if string(got["SKILL.md"].data) != "adapted\n" || got["probe"].mode != 0755 || string(got["probe"].data) != "#!/bin/sh\n" {
		t.Fatal("patch changed untouched resource or mode")
	}
	unsafe := []byte("diff --git a/../../escape b/../../escape\n--- a/../../escape\n+++ b/../../escape\n@@ -1 +1 @@\n-shared\n+adapted\n")
	if _, e := applySkillPatch(context.Background(), files, unsafe); e == nil {
		t.Fatal("unsafe patch accepted")
	}
}

func TestNewCommittedSharedSourceChangesCompositionIdentity(t *testing.T) {
	src, shared := compositionFixture(t)
	before, e := Build(context.Background(), src)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(shared, ".agents/skills/shared/SKILL.md"), []byte("new committed source\n"), 0644); e != nil {
		t.Fatal(e)
	}
	commit := func(root string) string {
		t.Helper()
		for _, args := range [][]string{{"add", "."}, {"commit", "-qm", "new fixed source"}} {
			c := exec.Command("git", args...)
			c.Dir = root
			if o, e := c.CombinedOutput(); e != nil {
				t.Fatalf("%s %v", o, e)
			}
		}
		c := exec.Command("git", "rev-parse", "HEAD")
		c.Dir = root
		o, e := c.Output()
		if e != nil {
			t.Fatal(e)
		}
		return strings.TrimSpace(string(o))
	}
	src.SkillsSource.Commit = commit(shared)
	lockRef := filepath.Join(src.Root, ".template-source/profile-skills-source.json")
	data, e := os.ReadFile(lockRef)
	if e != nil {
		t.Fatal(e)
	}
	var lock map[string]any
	if e = json.Unmarshal(data, &lock); e != nil {
		t.Fatal(e)
	}
	lock["sourceCommit"] = src.SkillsSource.Commit
	lock["skillsDigest"] = compositionDigest(map[string]sourceFile{".agents/skills/shared/SKILL.md": {data: []byte("new committed source\n"), mode: 0644}, ".agents/skills/shared/probe": {data: []byte("#!/bin/sh\n"), mode: 0755}})
	data, _ = json.Marshal(lock)
	if e = os.WriteFile(lockRef, data, 0644); e != nil {
		t.Fatal(e)
	}
	src.Commit = commit(src.Root)
	after, e := Build(context.Background(), src)
	if e != nil {
		t.Fatal(e)
	}
	a, b := before.Manifest["skillComposition"].(map[string]any), after.Manifest["skillComposition"].(map[string]any)
	if a["sourceTemplateCommit"] == b["sourceTemplateCommit"] || a["skillsDigest"] == b["skillsDigest"] || before.ManifestHash == after.ManifestHash || before.BundleHash == after.BundleHash {
		t.Fatal("committed source/content change did not bind new identity")
	}
}
