package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func nativeFixture(t *testing.T) (string, []byte) {
	t.Helper()
	root := freshRoot(t)
	if e := os.MkdirAll(filepath.Join(root, ".template-spec/process"), 0755); e != nil {
		t.Fatal(e)
	}
	profile := domain.Profiles["spec"]
	m := Metadata{TemplateSourceState: "committed", SchemaVersion: 1, Profile: "spec", ProfileID: profile.ID, ProtocolVersion: 1, CLIVersion: domain.Version, TemplateVersion: profile.LegacyVersion, LegacyCLIVersion: profile.LegacyVersion, TemplateCommit: strings.Repeat("a", 40), SnapshotHash: strings.Repeat("b", 64), ManifestHash: strings.Repeat("c", 64), Managed: map[string]Managed{}, Variables: map[string]string{"projectName": "fixture", "businessDomain": "fixture", "teamSize": "1"}, Distribution: map[string]any{}}
	b, _ := json.Marshal(m.Managed)
	m.BaselineDigest = safefs.Digest(b)
	b, _ = json.Marshal(m)
	for ref, data := range map[string][]byte{MetadataFile: b, "yss-project.yaml": []byte("schema_version: 1\nrepository_mode: project-instance\n"), ".template-spec/process/harness-profile.yaml": []byte("schema_version: 2\nprofile_id: " + profile.ID + "\n")} {
		if e := os.WriteFile(filepath.Join(root, filepath.FromSlash(ref)), data, 0644); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := Detect(root, "", false); e != nil {
		t.Fatal(e)
	}
	return root, b
}

func TestNativeIdentityRejectsDuplicateKeysAndMissingSourceFields(t *testing.T) {
	root, original := nativeFixture(t)
	cases := map[string][]byte{"duplicate": append([]byte(`{"schemaVersion":999,`), original[1:]...)}
	for _, field := range []string{"templateCommit", "templateSourceState", "snapshotHash", "manifestHash", "templateVersion", "legacyCliVersion"} {
		var m map[string]any
		_ = json.Unmarshal(original, &m)
		delete(m, field)
		b, _ := json.Marshal(m)
		cases[field] = b
	}
	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			if e := os.WriteFile(filepath.Join(root, MetadataFile), b, 0644); e != nil {
				t.Fatal(e)
			}
			if _, e := Detect(root, "", false); e == nil {
				t.Fatal("invalid native identity accepted")
			}
		})
	}
}

func TestSavePlanRejectsGitBusinessPathsAndOutsideAliases(t *testing.T) {
	root := freshRoot(t)
	if e := os.MkdirAll(filepath.Join(root, ".git"), 0755); e != nil {
		t.Fatal(e)
	}
	p := &Plan{Root: root}
	for _, ref := range []string{".git/index", "business.json"} {
		file := filepath.Join(root, ref)
		if e := SavePlan(p, file); e == nil {
			t.Fatal("protected output accepted: " + ref)
		}
		if _, e := os.Stat(file); !os.IsNotExist(e) {
			t.Fatal("protected output created")
		}
	}
	if runtime.GOOS != "windows" {
		base := filepath.Dir(root)
		alias := filepath.Join(base, "alias")
		if e := os.Symlink(filepath.Join(root, ".git"), alias); e != nil {
			t.Fatal(e)
		}
		if e := SavePlan(p, filepath.Join(alias, "index")); e == nil {
			t.Fatal("outside Git alias accepted")
		}
		if e := os.Remove(alias); e != nil {
			t.Fatal(e)
		}
		if e := os.Symlink(root, alias); e != nil {
			t.Fatal(e)
		}
		if e := SavePlan(p, filepath.Join(alias, "business.json")); e == nil {
			t.Fatal("outside business alias accepted")
		}
	}
	outside := filepath.Join(filepath.Dir(root), "review-plan.json")
	if e := SavePlan(p, outside); e != nil {
		t.Fatal(e)
	}
	if e := SavePlan(p, outside); e == nil {
		t.Fatal("existing plan was overwritten")
	}
}

func TestReadPlanRejectsDuplicateKeysAndTrailingDocuments(t *testing.T) {
	root := freshRoot(t)
	if e := os.MkdirAll(root, 0755); e != nil {
		t.Fatal(e)
	}
	p := &Plan{SchemaVersion: 1, ProtocolVersion: 1, Root: root}
	p.Digest = planDigest(p)
	b, _ := json.Marshal(p)
	file := filepath.Join(root, "plan.json")
	for _, invalid := range [][]byte{append(append([]byte{}, b...), []byte(` {}`)...), append([]byte(`{"schemaVersion":999,`), b[1:]...)} {
		if e := os.WriteFile(file, invalid, 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := ReadPlan(file); e == nil {
			t.Fatal("ambiguous plan accepted")
		}
	}
}
