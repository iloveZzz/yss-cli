package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func cliGovPut(t *testing.T, root, ref string, raw []byte) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(ref))
	if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, raw, 0644); e != nil {
		t.Fatal(e)
	}
}
func cliGovSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	e := filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		raw, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		r, e := filepath.Rel(root, p)
		if e != nil {
			return e
		}
		out[r] = safefs.Digest(raw)
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	return out
}

func TestGovernanceCLIObservesIdentityForFourProfilesAndBothModes(t *testing.T) {
	t.Setenv("PATH", "")
	for _, profile := range []string{"spec", "design", "backend", "frontend"} {
		t.Run(profile, func(t *testing.T) {
			b, e := bundle.Load(profile)
			if e != nil {
				t.Fatal(e)
			}
			for _, mode := range []string{"project-instance", "template-source"} {
				t.Run(mode, func(t *testing.T) {
					root, e := filepath.EvalSymlinks(t.TempDir())
					if e != nil {
						t.Fatal(e)
					}
					for ref, f := range b.Files {
						if strings.HasPrefix(ref, ".template-spec/process/schemas/") || ref == "CONTEXT.md" || ref == "AGENTS.md" || ref == ".template-spec/process/lifecycle-registry.yaml" || ref == ".template-spec/agents/digital-human-roles.yaml" || ref == ".template-spec/agents/yss-skill-registry.yaml" || ref == ".template-spec/agents/issue-tracker.md" {
							raw, e := f.Render(map[string]string{"projectName": "synthetic-cli", "businessDomain": "test-only", "teamSize": "2"})
							if e != nil {
								t.Fatal(e)
							}
							cliGovPut(t, root, ref, raw)
						}
					}
					raw, _ := json.Marshal(map[string]any{"schema_version": 1, "repository_mode": mode})
					cliGovPut(t, root, "yss-project.yaml", raw)
					raw, _ = json.Marshal(map[string]any{"schema_version": 2, "profile_id": domain.Profiles[profile].ID})
					cliGovPut(t, root, ".template-spec/process/harness-profile.yaml", raw)
					for _, change := range []string{"known", "unknown-native-metadata", "unknown-legacy-metadata"} {
						t.Run(change, func(t *testing.T) {
							_ = os.Remove(filepath.Join(root, domain.MetadataFile))
							_ = os.Remove(filepath.Join(root, domain.Profiles[profile].Metadata))
							if change == "unknown-native-metadata" {
								cliGovPut(t, root, domain.MetadataFile, []byte(`{"schemaVersion":1,"protocolVersion":1,"profile":"`+profile+`","profileId":"`+domain.Profiles[profile].ID+`"}`))
							}
							if change == "unknown-legacy-metadata" {
								cliGovPut(t, root, domain.Profiles[profile].Metadata, []byte(`{"metadataSchemaVersion":99}`))
							}
							before, _ := json.Marshal(cliGovSnapshot(t, root))
							var out, stderr bytes.Buffer
							exit := Run(context.Background(), []string{"project-ci", "verify", "--root", root, "--json"}, &out, &stderr)
							var env map[string]any
							if e := json.Unmarshal(out.Bytes(), &env); e != nil {
								t.Fatal(e)
							}
							result, ok := env["result"].(map[string]any)
							if !ok || result["approval_created"] != false || result["read_only"] != true {
								t.Fatalf("report: %s", out.String())
							}
							expected := 2
							if change == "known" {
								// Project CI retains its project-instance boundary. The
								// template-source identity must still be observed/reported.
								if mode == "project-instance" {
									expected = 0
								}
								if env["profile"] != profile {
									t.Fatalf("profile %v", env["profile"])
								}
							}
							if exit != expected {
								t.Fatalf("exit %d want%d: %s / %s", exit, expected, out.String(), stderr.String())
							}
							after, _ := json.Marshal(cliGovSnapshot(t, root))
							if !bytes.Equal(before, after) {
								t.Fatal("readonly CLI changed files")
							}
						})
					}
				})
			}
		})
	}
}
