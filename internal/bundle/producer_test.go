package bundle

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func fixtureGit(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		b, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("git %v: %s: %v", args, b, e)
		}
		return string(bytes.TrimSpace(b))
	}
	run("init", "-q")
	run("config", "user.email", "fixture@example.invalid")
	run("config", "user.name", "fixture")
	run("config", "core.autocrlf", "false")
	p := Policy{SchemaVersion: 1, Profile: "backend", Manifest: map[string]any{"allowRootEntries": []string{"scripts"}, "allowRootFiles": []string{"README.md", "yss-project.yaml"}, "renderPaths": []string{"README.md", "yss-project.yaml"}}}
	b, _ := json.Marshal(p)
	os.MkdirAll(filepath.Join(root, ".template-source/distribution"), 0755)
	os.WriteFile(filepath.Join(root, ".template-source/distribution/bundle-profile.json"), b, 0644)
	os.Mkdir(filepath.Join(root, "scripts"), 0755)
	os.WriteFile(filepath.Join(root, "scripts/probe"), []byte("#!/bin/sh\necho stable\n"), 0755)
	os.WriteFile(filepath.Join(root, "README.md"), []byte("source README\n"), 0644)
	os.WriteFile(filepath.Join(root, "yss-project.yaml"), []byte("schema_version: 1\nrepository_mode: template-source\n"), 0644)
	run("add", ".")
	run("update-index", "--chmod=+x", "scripts/probe")
	run("commit", "-qm", "fixture")
	return root, run("rev-parse", "HEAD"), safefs.Digest(b)
}

func TestBuildFixedCommitDiscoversFutureFiles(t *testing.T) {
	root, commit, policyHash := fixtureGit(t)
	src := Source{Profile: "backend", Root: root, Commit: commit, PolicyHash: policyHash}
	first, e := Build(context.Background(), src)
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(root, "scripts/new-file"), []byte("new bytes\n"), 0644)
	os.WriteFile(filepath.Join(root, "scripts/probe"), []byte("dirty\n"), 0644)
	repeat, e := Build(context.Background(), src)
	if e != nil {
		t.Fatal(e)
	}
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(repeat)
	if !bytes.Equal(a, b) {
		t.Fatal("fixed commit build depends on working tree")
	}
	cmd := exec.Command("git", "add", ".")
	cmd.Dir = root
	if o, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("%s: %v", o, e)
	}
	cmd = exec.Command("git", "commit", "-qm", "future")
	cmd.Dir = root
	if o, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("%s: %v", o, e)
	}
	cmd = exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = root
	next, _ := cmd.Output()
	src.Commit = string(bytes.TrimSpace(next))
	newer, e := Build(context.Background(), src)
	if e != nil {
		t.Fatal(e)
	}
	if _, ok := newer.Files["scripts/new-file"]; !ok {
		t.Fatal("new committed file omitted")
	}
	if newer.SnapshotHash == first.SnapshotHash || newer.BundleHash == first.BundleHash {
		t.Fatal("source change did not update identity")
	}
	if first.TemplateVersion != "git:"+commit || first.SourceState != "committed" || first.SchemaVersion != 2 {
		t.Fatal("source identities not independent")
	}
	if first.Files["scripts/probe"].Mode != 0755 {
		t.Fatal("executable mode lost")
	}
	rendered, _ := first.Files["README.md"].Render(map[string]string{"projectName": "demo", "businessDomain": "domain", "teamSize": "5"})
	if !bytes.Contains(rendered, []byte("# demo")) {
		t.Fatal("render variables lost")
	}
}
func TestBuildRejectsUnlockedPolicyAndSourceSymlink(t *testing.T) {
	root, commit, h := fixtureGit(t)
	src := Source{Profile: "backend", Root: root, Commit: commit, PolicyHash: "bad"}
	if _, e := Build(context.Background(), src); e == nil {
		t.Fatal("policy drift accepted")
	}
	src.PolicyHash = h
	src.Commit = "HEAD"
	if _, e := Build(context.Background(), src); e == nil {
		t.Fatal("floating source accepted")
	}
	if e := os.Symlink("probe", filepath.Join(root, "scripts/link")); e != nil {
		t.Fatal(e)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-qm", "symlink"}} {
		c := exec.Command("git", args...)
		c.Dir = root
		if o, e := c.CombinedOutput(); e != nil {
			t.Fatalf("%s: %v", o, e)
		}
	}
	c := exec.Command("git", "rev-parse", "HEAD")
	c.Dir = root
	o, _ := c.Output()
	src.Commit = string(bytes.TrimSpace(o))
	if _, e := Build(context.Background(), src); e == nil {
		t.Fatal("source symlink accepted")
	}
}
func TestPublicExportPreservesAllAssetsAndRefusesUnsafeTargets(t *testing.T) {
	b, e := Load("backend")
	if e != nil {
		t.Fatal(e)
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(base, "export")
	r, e := Export(context.Background(), "backend", out)
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Files) != len(b.Files) || r.Directory != out {
		t.Fatal("incomplete export receipt")
	}
	for ref, f := range b.Files {
		data, e := os.ReadFile(filepath.Join(out, filepath.FromSlash(ref)))
		if e != nil {
			t.Fatal(e)
		}
		if safefs.Digest(data) != f.Digest {
			t.Fatalf("bytes differ %s", ref)
		}
		st, _ := os.Stat(filepath.Join(out, filepath.FromSlash(ref)))
		if domain.FileMode(uint32(st.Mode().Perm())) != domain.FileMode(f.Mode) {
			t.Fatalf("mode differs %s", ref)
		}
	}
	if _, e = Export(context.Background(), "backend", out); e == nil {
		t.Fatal("existing target accepted")
	}
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "link")
	os.Symlink(t.TempDir(), link)
	if _, e = Export(context.Background(), "backend", filepath.Join(link, "child")); e == nil {
		t.Fatal("symlink ancestor accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	aborted := filepath.Join(parent, "aborted")
	if _, e = Export(ctx, "backend", aborted); e == nil {
		t.Fatal("cancellation ignored")
	}
	if _, e = os.Lstat(aborted); !os.IsNotExist(e) {
		t.Fatal("cancelled export left target")
	}
	i, e := Inspect("backend")
	if e != nil {
		t.Fatal(e)
	}
	if len(i.Files) != len(b.Files) || i.Manifest == nil {
		t.Fatal("inspection loses public manifest")
	}
}

func TestBuildExpandsOnlyCanonicalSkillProjections(t *testing.T) {
	root, commit, h := fixtureGit(t)
	_ = commit
	p := Policy{SchemaVersion: 1, Profile: "backend", Manifest: map[string]any{"allowRootEntries": []string{".agents", ".codex", "scripts"}, "allowRootFiles": []string{"README.md", "yss-project.yaml"}, "renderPaths": []string{"README.md", "yss-project.yaml"}}}
	raw, _ := json.Marshal(p)
	if e := os.WriteFile(filepath.Join(root, ".template-source/distribution/bundle-profile.json"), raw, 0644); e != nil {
		t.Fatal(e)
	}
	h = safefs.Digest(raw)
	os.MkdirAll(filepath.Join(root, ".agents/skills/demo"), 0755)
	os.WriteFile(filepath.Join(root, ".agents/skills/demo/SKILL.md"), []byte("canonical bytes\n"), 0644)
	os.MkdirAll(filepath.Join(root, ".codex/skills"), 0755)
	os.Symlink("../../.agents/skills/demo", filepath.Join(root, ".codex/skills/demo"))
	for _, args := range [][]string{{"add", "."}, {"commit", "-qm", "projection"}} {
		c := exec.Command("git", args...)
		c.Dir = root
		if o, e := c.CombinedOutput(); e != nil {
			t.Fatalf("%s %v", o, e)
		}
	}
	c := exec.Command("git", "rev-parse", "HEAD")
	c.Dir = root
	o, _ := c.Output()
	s := Source{Profile: "backend", Root: root, Commit: string(bytes.TrimSpace(o)), PolicyHash: h}
	b, e := Build(context.Background(), s)
	if e != nil {
		t.Fatal(e)
	}
	if b.Files[".codex/skills/demo/SKILL.md"].Data != b.Files[".agents/skills/demo/SKILL.md"].Data {
		t.Fatal("projection differs from canonical source")
	}
	if _, ok := b.Files[".codex/skills/demo"]; ok {
		t.Fatal("projection symlink exported as a file")
	}
	base, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	a, e := WriteBuilt(filepath.Join(base, "one"), map[string]*Bundle{"backend": b})
	if e != nil {
		t.Fatal(e)
	}
	repeat, e := WriteBuilt(filepath.Join(base, "two"), map[string]*Bundle{"backend": b})
	if e != nil {
		t.Fatal(e)
	}
	if a["backend"] != repeat["backend"] {
		t.Fatal("gzip output nondeterministic")
	}
}

func TestGeneratedEntryGuidanceUsesNativePlanReceipt(t *testing.T) {
	for _, ref := range []string{"AGENTS.md", "README.md", ".template-spec/user-guide/用户手册.md"} {
		data, e := renderSource("spec", ref, []byte("# source\n"), true, []string{})
		if e != nil {
			t.Fatal(e)
		}
		data = nativeGuidance("spec", ref, data)
		if bytes.Contains(data, []byte("create-yss-spec ")) {
			t.Fatalf("retired command in %s", ref)
		}
		if !bytes.Contains(data, []byte("yss skills ensure")) || !bytes.Contains(data, []byte("--plan-file")) {
			t.Fatalf("native plan receipt missing from %s", ref)
		}
	}
}

func TestSpecFullFilesUseSelectedInstanceVariants(t *testing.T) {
	root, _, _ := fixtureGit(t)
	refs := []string{"README.md", ".template-spec/user-guide/用户手册.md", "scripts/lib/skill-supply-chain.mjs"}
	p := Policy{SchemaVersion: 1, Profile: "spec", Manifest: map[string]any{"allowRootEntries": []string{".agents", ".template-spec", "scripts"}, "allowRootFiles": []string{"README.md", "yss-project.yaml", "skills-lock.json"}, "renderPaths": append(append([]string{}, refs...), "yss-project.yaml", "skills-lock.json")}, Common: Entries{Files: []string{".template-spec/agents/yss-skill-registry.yaml", ".template-spec/process/lifecycle-registry.yaml", ".template-spec/user-guide/用户手册.md"}, Scripts: []string{"scripts/lib/skill-supply-chain.mjs"}}, Stages: map[string]Entries{"stage.entry-triage": {}, "stage.plan": {}}}
	policy, _ := json.Marshal(p)
	lock := []byte(`{"version":3,"projectionRoots":[".codex/skills",".cursor/skills",".pi/skills"],"future":{"z":true,"a":null},"sources":{"fixture":{"revision":"fixed","future":{"z":1,"a":2}}},"skills":{"shared":{"demo":{"targets":[],"source":"fixture","upstreamHash":"source-demo","future":{"z":true}},"future":{"targets":[],"source":"fixture","sourceRevision":"fixed","upstreamHash":"source-future"}},"platform":{}}}`)
	files := map[string][]byte{".template-source/distribution/bundle-profile.json": policy, ".template-spec/agents/yss-skill-registry.yaml": []byte("instance_distribution:\n  initial_skills: [demo]\n"), ".template-spec/process/lifecycle-registry.yaml": []byte("stages:\n  - id: stage.entry-triage\n  - id: stage.plan\n"), ".agents/skills/demo/SKILL.md": []byte("# demo\n"), ".agents/skills/future/SKILL.md": []byte("# future\n"), refs[1]: []byte("# Template source guide\n"), refs[2]: []byte("export const PROJECTION_ROOTS = [\".codex/skills\", \".cursor/skills\", \".pi/skills\"];\n"), "skills-lock.json": lock}
	for ref, data := range files {
		file := filepath.Join(root, filepath.FromSlash(ref))
		if e := os.MkdirAll(filepath.Dir(file), 0755); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(file, data, 0644); e != nil {
			t.Fatal(e)
		}
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-qm", "spec source"}} {
		c := exec.Command("git", args...)
		c.Dir = root
		if o, e := c.CombinedOutput(); e != nil {
			t.Fatalf("%s: %v", o, e)
		}
	}
	c := exec.Command("git", "rev-parse", "HEAD")
	c.Dir = root
	commit, _ := c.Output()
	b, e := Build(context.Background(), Source{Profile: "spec", Root: root, Commit: string(bytes.TrimSpace(commit)), PolicyHash: safefs.Digest(policy)})
	if e != nil {
		t.Fatal(e)
	}
	for _, ref := range refs {
		if b.Files[ref].Data != b.Initial[ref].Data {
			t.Errorf("full and initial instance contract diverge: %s", ref)
		}
	}
	data, e := b.Files["skills-lock.json"].Render(nil)
	if e != nil {
		t.Fatal(e)
	}
	var got, want map[string]any
	if e := json.Unmarshal(data, &got); e != nil {
		t.Fatal(e)
	}
	if e := json.Unmarshal(lock, &want); e != nil {
		t.Fatal(e)
	}
	shared := want["skills"].(map[string]any)["shared"].(map[string]any)
	shared["demo"].(map[string]any)["effectiveHash"] = "240b451a46a9f407cf758fd3a154192225dd193651132d04b5d1c71523d5e3ec"
	shared["future"].(map[string]any)["effectiveHash"] = "3a9f40e4e1b42ff8f587e2174b857c5026a7ff3030e858797fb35bc50cba7bc8"
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("complete lock changed fields beyond effective distribution hashes:\ngot: %+v\nwant: %+v", got, want)
	}
	raw, e := readGitFiles(context.Background(), root, string(bytes.TrimSpace(commit)), p.Manifest)
	if e != nil {
		t.Fatal(e)
	}
	snapshot := map[string]File{}
	for ref, f := range raw {
		snapshot[ref] = encodedFile(f.data, f.mode, "")
	}
	snapshotBytes, _ := json.Marshal(snapshot)
	if b.SnapshotHash != safefs.Digest(snapshotBytes) || !bytes.Equal(raw["skills-lock.json"].data, lock) {
		t.Fatal("compiled lock changed raw fixed-source snapshot identity")
	}
	if _, ok := b.Files[".agents/skills/future/SKILL.md"]; !ok {
		t.Fatal("full asset range was reduced to initial skills")
	}
}
