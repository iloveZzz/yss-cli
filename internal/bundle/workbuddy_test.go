package bundle

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func workBuddyFiles(t *testing.T) map[string]File {
	t.Helper()
	files := map[string]File{}
	for _, root := range []string{".agents/skills", ".workbuddy/skills"} {
		for _, name := range []string{"a", "b"} {
			files[root+"/"+name+"/SKILL.md"] = encodedFile([]byte("---\nname: "+name+"\n---\n"), 0644, "managed")
			files[root+"/"+name+"/scripts/run.sh"] = encodedFile([]byte("#!/bin/sh\n"), 0755, "managed")
		}
	}
	lock := []byte(`{"version":3,"canonicalRoot":".agents/skills","projectionRoots":[".workbuddy/skills"],"skills":{"shared":{"a":{"skillPath":".agents/skills/a/SKILL.md","targets":[".agents/skills",".workbuddy/skills"]},"b":{"targets":[".agents/skills",".workbuddy/skills"]}},"platform":{}}}`)
	files["skills-lock.json"] = encodedFile(lock, 0644, "generated")
	return files
}

func TestSelectedSourceLockProjectsWorkBuddyRuntime(t *testing.T) {
	lock := []byte(`{"version":3,"sources":{"upstream":{"revision":"fixed"}},"future":{"keep":true},"skills":{"shared":{"a":{"targets":[".agents/skills"]},"b":{}},"platform":{}}}`)
	out, err := SelectedSourceLock(lock, []string{"a"}, []string{"workbuddy"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte(`".workbuddy/skills"`)) || !bytes.Contains(out, []byte(`"future"`)) || !bytes.Contains(out, []byte(`"fixed"`)) {
		t.Fatalf("lost roots or metadata: %s", out)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got["projectionRoots"], []any{".workbuddy/skills"}) {
		t.Fatal(got)
	}
	shared := got["skills"].(map[string]any)["shared"].(map[string]any)
	if len(shared) != 1 || !reflect.DeepEqual(shared["a"].(map[string]any)["targets"], []any{".agents/skills", ".workbuddy/skills"}) {
		t.Fatal(shared)
	}
	for _, bad := range [][]string{{"workbuddy", "workbuddy"}, {"codebuddy"}} {
		if _, err := SelectedSourceLock(lock, []string{"a"}, bad); err == nil {
			t.Fatalf("accepted %v", bad)
		}
	}
}

func TestWorkBuddySelectiveClosure(t *testing.T) {
	for _, runtimes := range []string{"[claude, workbuddy]", "[claude]", "[workbuddy, workbuddy]", "[missing]"} {
		t.Run(runtimes, func(t *testing.T) {
			raw := map[string]sourceFile{
				".template-spec/agents/yss-skill-registry.yaml":  {data: []byte("instance_distribution:\n  initial_skills: [a]\n  runtimes: " + runtimes + "\n"), mode: 0644},
				".template-spec/process/lifecycle-registry.yaml": {data: []byte("stages:\n  - id: stage.entry-triage\n  - id: stage.plan\n"), mode: 0644},
			}
			for _, root := range []string{".agents/skills", ".claude/skills", ".workbuddy/skills"} {
				for _, name := range []string{"a", "b"} {
					raw[root+"/"+name+"/SKILL.md"] = sourceFile{data: []byte("# " + name + "\n"), mode: 0644}
					raw[root+"/"+name+"/payload.txt"] = sourceFile{data: []byte("resource\n"), mode: 0644}
				}
			}
			b := &Bundle{Initial: map[string]File{}, Files: map[string]File{}}
			p := Policy{Manifest: map[string]any{"allowRootEntries": []string{".agents", ".claude"}, "allowFiles": []string{".workbuddy/skills"}}, Stages: map[string]Entries{"stage.entry-triage": {}, "stage.plan": {}}}
			err := buildSelective(b, raw, p, nil)
			if runtimes == "[workbuddy, workbuddy]" || runtimes == "[missing]" {
				if err == nil {
					t.Fatal("accepted invalid runtime")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := b.Initial[".agents/skills/a/SKILL.md"]; !ok {
				t.Fatal("missing canonical")
			}
			for ref := range b.Initial {
				if strings.Contains(ref, "/b/") {
					t.Fatalf("unselected skill leaked: %s", ref)
				}
			}
			_, present := b.Initial[".workbuddy/skills/a/payload.txt"]
			if present != strings.Contains(runtimes, "workbuddy") {
				t.Fatalf("WorkBuddy selection mismatch: %v", b.Initial)
			}
		})
	}
}

func TestFullSkillDistributionWorkBuddy(t *testing.T) {
	files := workBuddyFiles(t)
	got, err := FullSkillDistribution(files)
	if err != nil {
		t.Fatal(err)
	}
	if got["mode"] != "profile-full" || !reflect.DeepEqual(got["runtimes"], []string{"workbuddy"}) || !reflect.DeepEqual(got["installedSkills"], []string{"a", "b"}) {
		t.Fatal(got)
	}
	for _, kind := range []string{"unknown", "duplicate", "missing-resource", "changed-resource", "mode"} {
		t.Run(kind, func(t *testing.T) {
			f := workBuddyFiles(t)
			switch kind {
			case "unknown", "duplicate":
				lock, _ := base64.StdEncoding.DecodeString(f["skills-lock.json"].Data)
				roots := `[".missing/skills"]`
				if kind == "duplicate" {
					roots = `[".workbuddy/skills",".workbuddy/skills"]`
				}
				f["skills-lock.json"] = encodedFile(bytes.Replace(lock, []byte(`[".workbuddy/skills"]`), []byte(roots), 1), 0644, "generated")
			case "missing-resource":
				delete(f, ".workbuddy/skills/a/scripts/run.sh")
			case "changed-resource":
				f[".workbuddy/skills/a/scripts/run.sh"] = encodedFile([]byte("different"), 0755, "managed")
			case "mode":
				x := f[".workbuddy/skills/a/scripts/run.sh"]
				x.Mode = 0644
				f[".workbuddy/skills/a/scripts/run.sh"] = x
			}
			if _, err := FullSkillDistribution(f); err == nil {
				t.Fatalf("accepted %s", kind)
			}
		})
	}
}

func TestWorkBuddySkillTreeValidation(t *testing.T) {
	f := workBuddyFiles(t)
	lock, _ := f["skills-lock.json"].Render(nil)
	if _, err := RenderedSkillLock(lock, f, nil); err != nil {
		t.Fatal(err)
	}
	f[".workbuddy/skills/a/scripts/run.sh"] = encodedFile([]byte("drift"), 0755, "managed")
	if _, err := RenderedSkillLock(lock, f, nil); err == nil {
		t.Fatal("accepted projection resource drift")
	}
}

func TestRemovedWorkBuddyRootIsRejected(t *testing.T) {
	files := map[string]File{}
	for ref, file := range workBuddyFiles(t) {
		if ref == "skills-lock.json" {
			data, _ := file.Render(nil)
			file = encodedFile(bytes.ReplaceAll(data, []byte(".workbuddy/skills"), []byte(".codebuddy/skills")), file.Mode, file.Ownership)
		}
		files[strings.ReplaceAll(ref, ".workbuddy/skills", ".codebuddy/skills")] = file
	}
	lock, _ := files["skills-lock.json"].Render(nil)
	if _, err := RenderedSkillLock(lock, files, nil); err == nil {
		t.Fatal("accepted removed WorkBuddy root in Skill lock")
	}
	if _, err := FullSkillDistribution(files); err == nil {
		t.Fatal("accepted removed WorkBuddy root in full distribution")
	}
}

func TestWorkBuddyProjectionRawBytesAndModes(t *testing.T) {
	for _, kind := range []string{"mode", "crlf", "missing-tree"} {
		t.Run(kind, func(t *testing.T) {
			files := workBuddyFiles(t)
			ref := ".workbuddy/skills/a/scripts/run.sh"
			if kind == "mode" {
				file := files[ref]
				file.Mode = 0644
				files[ref] = file
			} else if kind == "crlf" {
				files[ref] = encodedFile([]byte("#!/bin/sh\r\n"), 0755, "managed")
			} else {
				for ref := range files {
					if strings.HasPrefix(ref, ".workbuddy/skills/a/") {
						delete(files, ref)
					}
				}
			}
			lock, _ := files["skills-lock.json"].Render(nil)
			if _, err := RenderedSkillLock(lock, files, nil); err == nil {
				t.Fatal("accepted WorkBuddy projection divergence", kind)
			}
		})
	}
}
