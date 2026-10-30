//go:build !yss_nobundle

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

func TestTrustedInstanceRejectsRemovedDesignHistory(t *testing.T) {
	for _, badDigest := range []bool{false, true} {
		t.Run(map[bool]string{false: "removed-profile", true: "digest-drift"}[badDigest], func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "spec")
			commandOK(t, root, "init", "--profile", "spec")
			history := []byte(`{"schema_version":1,"kind":"governance-original-source","source_profile":"design","files":{}}`)
			digest := safefs.Digest(history)
			want := "Design Profile 已移除"
			if badDigest {
				digest, want = strings.Repeat("e", 64), "HISTORY_SOURCE_DRIFT"
			}
			ref := ".template-spec/compatibility/governance-history/" + digest + "/manifest.json"
			historyFile := filepath.Join(root, ref)
			if err := os.MkdirAll(filepath.Dir(historyFile), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(historyFile, history, 0600); err != nil {
				t.Fatal(err)
			}
			metaFile := filepath.Join(root, ".yss.json")
			raw, err := os.ReadFile(metaFile)
			if err != nil {
				t.Fatal(err)
			}
			var meta identitymeta.Metadata
			if err = json.Unmarshal(raw, &meta); err != nil {
				t.Fatal(err)
			}
			meta.GovernanceHistory = &identitymeta.GovernanceHistory{Ref: ref, Digest: digest}
			metadata, err := json.Marshal(meta)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(metaFile, metadata, 0644); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{
				{"contract", "verify", "--kind", "task", "--file", "task.yaml"},
				{"evidence", "verify", "--kind", "approval", "--file", "approval.json"},
				{"lifecycle", "verify", "--checkpoint", "checkpoint.json"},
			} {
				code, out, stderr := captureCommand(t, context.Background(), append(args, "--root", root, "--json"), false)
				if code == 0 || !strings.Contains(out+stderr, want) {
					t.Fatalf("history rejection: %v %s %s", args, out, stderr)
				}
			}
			for file, expected := range map[string][]byte{metaFile: metadata, historyFile: history} {
				actual, err := os.ReadFile(file)
				if err != nil || string(actual) != string(expected) {
					t.Fatalf("refusal changed %s: %v", file, err)
				}
			}
		})
	}
}
