package bundle

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func TestWorkBuddyCompositionUsesCurrentRoot(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	runGit := func(args ...string) string {
		t.Helper()
		out, err := git(ctx, root, args...)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(ref string, data []byte, mode os.FileMode) {
		t.Helper()
		file := filepath.Join(root, filepath.FromSlash(ref))
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, data, mode); err != nil {
			t.Fatal(err)
		}
	}
	runGit("init", "-q")
	runGit("config", "user.name", "Test")
	runGit("config", "user.email", "test@example.invalid")
	config := []byte(`{"schema_version":1,"profiles":{"backend":{"materialization":"generated","exact":[{"id":"a"}]}}}`)
	configurationPath := ".template-source/profile-skill-sync.json"
	write(configurationPath, config, 0644)
	canonical := map[string]sourceFile{
		".agents/skills/a/SKILL.md":       {data: []byte("# a\n"), mode: 0644},
		".agents/skills/a/scripts/run.sh": {data: []byte("#!/bin/sh\n"), mode: 0755},
	}
	for ref, file := range canonical {
		write(ref, file.data, os.FileMode(file.mode))
	}
	runGit("add", ".")
	runGit("update-index", "--chmod=+x", ".agents/skills/a/scripts/run.sh")
	runGit("commit", "-qm", "shared source")
	sharedCommit := runGit("rev-parse", "HEAD")
	lock, err := json.Marshal(profileSkillSourceLock{SchemaVersion: 1, Profile: "backend", Commit: sharedCommit, State: "committed", ConfigurationPath: configurationPath, ConfigurationHash: safefs.Digest(config), SkillsDigest: compositionDigest(canonical)})
	if err != nil {
		t.Fatal(err)
	}
	write(".template-source/profile-skills-source.json", lock, 0644)
	runGit("add", ".")
	runGit("commit", "-qm", "profile source")
	source := Source{Profile: "backend", Root: root, Commit: runGit("rev-parse", "HEAD"), SkillsSource: &SkillSource{Root: root, Commit: sharedCommit, ConfigurationPath: configurationPath, ConfigurationHash: safefs.Digest(config)}}
	for _, drift := range []string{"", "bytes", "mode", "configuration"} {
		t.Run(drift, func(t *testing.T) {
			raw := map[string]sourceFile{"skills-lock.json": {data: []byte(`{"projectionRoots":[".workbuddy/skills"]}`), mode: 0644}}
			ref := ".workbuddy/skills/a/scripts/run.sh"
			if drift == "bytes" {
				raw[ref] = sourceFile{data: []byte("changed\n"), mode: 0755}
			} else if drift == "mode" {
				raw[ref] = sourceFile{data: canonical[".agents/skills/a/scripts/run.sh"].data, mode: 0644}
			}
			candidate := source
			if drift == "configuration" {
				shared := *source.SkillsSource
				shared.ConfigurationHash = strings.Repeat("0", 64)
				candidate.SkillsSource = &shared
			}
			_, err := composeCommittedSkills(ctx, candidate, raw)
			if drift != "" {
				if err == nil {
					t.Fatal("accepted composition drift")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for ref, file := range canonical {
				projection := ".workbuddy/skills/" + strings.TrimPrefix(ref, ".agents/skills/")
				got, exists := raw[projection]
				if !exists || !bytes.Equal(got.data, file.data) || got.mode != file.mode {
					t.Fatalf("missing or changed current projection: %s", projection)
				}
			}
			for ref := range raw {
				if strings.HasPrefix(ref, ".codebuddy/") {
					t.Fatalf("removed projection generated: %s", ref)
				}
			}
		})
	}
}
