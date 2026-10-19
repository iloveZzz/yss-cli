package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/identitymeta"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func TestRemovedDesignPublicInputsRejected(t *testing.T) {
	tree := newRootCommand(nil, nil, nil)
	for _, path := range [][]string{{"handoff", "export"}, {"handoff", "import"}} {
		if _, rest := lookupCommand(tree, path); len(rest) == 0 {
			t.Fatal("removed command still registered", path)
		}
	}
	prepare, _ := lookupCommand(tree, []string{"profile", "prepare"})
	if prepare.Flags().Lookup("design-root") != nil {
		t.Fatal("removed flag still registered")
	}
	root := t.TempDir()
	target := filepath.Join(root, "must-not-exist")
	out := filepath.Join(root, "plan.json")
	for _, args := range [][]string{
		{"init", "--profile", "design", "--root", target},
		{"profile", "prepare", "--root", root, "--design-root", target, "--plan", "--out", out},
		{"help", "tutorial", "design"},
		{"handoff", "export", "--root", root, "--kind", "spec-baseline", "--checkpoint", "checkpoint.json", "--out", target},
		{"handoff", "import", "--root", root, "--kind", "spec-baseline", "--package", target, "--plan", "--out", out},
	} {
		code, stdout, stderr := captureCommand(t, context.Background(), append(args, "--json"), false)
		if code == 0 {
			t.Fatalf("removed input accepted: %v %s %s", args, stdout, stderr)
		}
		for _, file := range []string{target, out} {
			if _, err := os.Lstat(file); !os.IsNotExist(err) {
				t.Fatalf("removed input wrote %s: %v", file, err)
			}
		}
	}
	b, err := json.Marshal(capabilities())
	if err != nil {
		t.Fatal(err)
	}
	raw := string(b)
	for _, removed := range []string{"spec-baseline-v1", "handoff.spec-baseline-package.v1", "handoff.spec-baseline-source-view.v1", "contract.spec-baseline-binding.v1"} {
		if strings.Contains(raw, removed) {
			t.Fatal("removed capability advertised", removed)
		}
	}
}

func TestRemovedDesignSemanticCommandsRejected(t *testing.T) {
	for _, marker := range []string{".yss-harness-design.json", ".yss-product-design-plugin.json"} {
		root := t.TempDir()
		file := filepath.Join(root, marker)
		if err := os.WriteFile(file, []byte("null\n"), 0600); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{
			{"contract", "verify", "--kind", "task", "--file", "task.yaml"},
			{"evidence", "verify", "--kind", "approval", "--file", "approval.json"},
			{"lifecycle", "verify", "--checkpoint", "checkpoint.json"},
		} {
			code, out, stderr := captureCommand(t, context.Background(), append(args, "--root", root, "--json"), false)
			if code == 0 || !strings.Contains(out+stderr, "Design Profile 已移除") {
				t.Fatalf("semantic command did not reject Design identity: %v %s %s", args, out, stderr)
			}
		}
		raw, err := os.ReadFile(file)
		if err != nil || string(raw) != "null\n" {
			t.Fatal("semantic refusal changed original bytes", err)
		}
		info, err := os.Stat(file)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("semantic refusal changed original mode", err)
		}
		files, err := os.ReadDir(root)
		if err != nil || len(files) != 1 {
			t.Fatal("semantic refusal created files", err)
		}
	}
}

func TestRemovedDesignHistorySemanticCommandsRejected(t *testing.T) {
	for _, badDigest := range []bool{false, true} {
		root := t.TempDir()
		raw := []byte(`{"schema_version":1,"kind":"governance-original-source","source_profile":"design","files":{}}`)
		digest := safefs.Digest(raw)
		want := "Design Profile 已移除"
		if badDigest {
			digest, want = strings.Repeat("e", 64), "HISTORY_SOURCE_DRIFT"
		}
		ref := ".template-spec/compatibility/governance-history/" + digest + "/manifest.json"
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, ref)), 0755); err != nil {
			t.Fatal(err)
		}
		meta := identitymeta.Metadata{SchemaVersion: 4, BundleSchemaVersion: 4, ProtocolVersion: 1, Profile: "spec", ProfileID: "harness.spec-template", CLIVersion: "2.5.0", TemplateVersion: "4.6.1", LegacyCLIVersion: "1.0.0", TemplateCommit: strings.Repeat("a", 40), TemplateSourceState: "committed", CLISourceState: "unknown", SnapshotHash: strings.Repeat("b", 64), ManifestHash: strings.Repeat("c", 64), BundleHash: strings.Repeat("d", 64), Managed: map[string]identitymeta.Managed{}, BaselineDigest: safefs.Digest([]byte("{}")), Variables: map[string]string{}, Distribution: map[string]any{}, GovernanceHistory: &identitymeta.GovernanceHistory{Ref: ref, Digest: digest}}
		metadata, err := json.Marshal(meta)
		if err != nil {
			t.Fatal(err)
		}
		for file, bytes := range map[string][]byte{".yss.json": metadata, ref: raw} {
			if err := os.WriteFile(filepath.Join(root, file), bytes, 0600); err != nil {
				t.Fatal(err)
			}
		}
		for _, args := range [][]string{
			{"contract", "verify", "--kind", "task", "--file", "task.yaml"},
			{"evidence", "verify", "--kind", "approval", "--file", "approval.json"},
			{"lifecycle", "verify", "--checkpoint", "checkpoint.json"},
		} {
			code, out, stderr := captureCommand(t, context.Background(), append(args, "--root", root, "--json"), false)
			if code == 0 || !strings.Contains(out+stderr, want) {
				t.Fatalf("semantic history check: %v %s %s", args, out, stderr)
			}
		}
	}
}
