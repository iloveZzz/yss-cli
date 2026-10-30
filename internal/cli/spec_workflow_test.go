package cli

import (
	"context"
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/identitymeta"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newSpecCommandRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	policy := []byte("{\"specWorkflow\":\"matt-spec-v1\"}\n")
	writeCheckFile(t, root, ".template-spec/process/spec-workflow.json", string(policy))
	writeCheckFile(t, root, "yss-project.yaml", "schema_version: 2\nrepository_mode: project-instance\n")
	writeCheckFile(t, root, ".template-spec/process/harness-profile.yaml", "schema_version: 3\nprofile_id: harness.spec-template\n")
	d, err := safefs.Describe(root, ".template-spec/process/spec-workflow.json")
	if err != nil {
		t.Fatal(err)
	}
	managed := map[string]identitymeta.Managed{".template-spec/process/spec-workflow.json": {Baseline: d, Applied: d, Source: &identitymeta.BaselineSource{Kind: "bundle", TemplateCommit: strings.Repeat("a", 40), SnapshotHash: strings.Repeat("b", 64), BundleHash: strings.Repeat("d", 64), Path: ".template-spec/process/spec-workflow.json", Variant: "initial", TemplateDigest: d.Digest}}}
	mb, _ := json.Marshal(managed)
	m := identitymeta.Metadata{SchemaVersion: 5, BundleSchemaVersion: 5, ProtocolVersion: 1, Profile: "spec", ProfileID: domain.Profiles["spec"].ID, CLIVersion: "2.4.0", TemplateVersion: "4.8.3", LegacyCLIVersion: "3.5.10", TemplateCommit: strings.Repeat("a", 40), TemplateSourceState: "committed", CLISourceState: "unknown", SnapshotHash: strings.Repeat("b", 64), ManifestHash: strings.Repeat("c", 64), BundleHash: strings.Repeat("d", 64), Managed: managed, BaselineDigest: safefs.Digest(mb), Variables: map[string]string{}, Distribution: map[string]any{"installation": "governed", "specWorkflow": "matt-spec-v1"}}
	raw, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(root, ".yss.json"), raw, 0644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestNewSpecDoesNotEnterLegacyLifecycle(t *testing.T) {
	root := newSpecCommandRoot(t)
	for _, args := range [][]string{{"lifecycle", "status"}, {"handoff", "check"}, {"stage", "status"}, {"profile", "prepare"}} {
		full := append(append([]string{}, args...), "--root", root, "--json")
		code, out, _ := captureCommand(t, context.Background(), full, false)
		if code == 0 || commandEnvelope(t, out)["code"] != "SPEC_COMMAND_NOT_APPLICABLE" {
			t.Fatalf("legacy command %v: %d %s", args, code, out)
		}
	}
	code, out, _ := captureCommand(t, context.Background(), []string{"project", "inspect", "--root", root, "--json"}, false)
	if code != 0 || commandEnvelope(t, out)["status"] != "ok" {
		t.Fatalf("inspect must remain available: %d %s", code, out)
	}
}
