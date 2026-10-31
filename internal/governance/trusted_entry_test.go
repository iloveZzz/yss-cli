//go:build !yss_nobundle

package governance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/identitymeta"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func TestStageAndNativeCIRejectUntrustedSource(t *testing.T) {
	b, err := bundle.Load("backend")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	profile, err := domain.GetProfile("backend")
	if err != nil {
		t.Fatal(err)
	}
	meta := identitymeta.Metadata{SchemaVersion: 4, BundleSchemaVersion: 4, ProtocolVersion: domain.ProtocolVersion,
		Profile: "backend", ProfileID: profile.ID, CLIVersion: "3.0.0", LegacyCLIVersion: "3.0.0",
		TemplateVersion: "4.0.0", TemplateCommit: b.TemplateCommit, SnapshotHash: b.SnapshotHash,
		ManifestHash: b.ManifestHash, BundleHash: b.BundleHash, TemplateSourceState: b.SourceState,
		CLISourceState: "unknown", Variables: map[string]string{}, Distribution: map[string]any{},
		Managed: map[string]identitymeta.Managed{}, BaselineDigest: safefs.Digest([]byte("{}"))}
	changed, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, ".yss.json")
	if err = os.WriteFile(file, changed, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		group, action string
		args          map[string]string
	}{
		{"stage", "check", map[string]string{"checkpoint": "missing.json"}},
		{"project-ci", "check", map[string]string{"scope": "native-go"}},
		{"project-ci", "transition", map[string]string{"scope": "native-go", "checkpoint": "missing.json"}},
		{"project-ci", "plan", map[string]string{"scope": "native-go"}},
	} {
		t.Run(tc.group+"/"+tc.action, func(t *testing.T) {
			_, err := Run(tc.group, tc.action, root, tc.args)
			if err == nil || !strings.Contains(err.Error(), "受信") {
				t.Fatalf("应先拒绝不受信来源，实际: %v", err)
			}
		})
	}
	after, err := os.ReadFile(file)
	if err != nil || string(after) != string(changed) {
		t.Fatal("拒绝时修改了身份文件")
	}
}
