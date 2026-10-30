package project

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func TestBackendPluginBindingRetainsNativeProfile(t *testing.T) {
	b, err := bundle.Load("backend")
	if err != nil {
		t.Fatal(err)
	}
	binary, err := domain.ExecutableDigest()
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"schema_version": 3, "plugin": "yss-backend-delivery", "profile": "backend", "template_commit": b.TemplateCommit, "bundle_hash": b.BundleHash, "binary_sha256": binary, "execution_scope": "backend-profile", "execution_owner": "project-local-harness-orchestrator", "business_execution_ready": false}
	makeBinding := func(value map[string]any) *Binding {
		raw, _ := json.Marshal(value)
		return &Binding{SchemaVersion: 1, Path: ".yss-backend-plugin.json", Data: base64.StdEncoding.EncodeToString(raw)}
	}
	plan := func() *Plan {
		return &Plan{Root: filepath.Join(t.TempDir(), "target"), Profile: "backend", Command: "init", TemplateCommit: b.TemplateCommit, Inputs: map[string]domain.Descriptor{}}
	}
	p := plan()
	if err := addBinding(p, makeBinding(payload)); err != nil {
		t.Fatal(err)
	}
	for _, change := range p.Changes {
		if change.Path == ".yss-execution-scope.yaml" {
			t.Fatal("backend must not acquire Spec execution scope")
		}
	}
	for _, ref := range []string{".yss-plugin.json", ".yss-execution-scope.yaml"} {
		p := plan()
		if err := os.MkdirAll(p.Root, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p.Root, ref), []byte("old"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := addBinding(p, makeBinding(payload)); err == nil {
			t.Fatalf("accepted legacy marker %s", ref)
		}
		if raw, _ := os.ReadFile(filepath.Join(p.Root, ref)); string(raw) != "old" {
			t.Fatalf("modified %s", ref)
		}
	}
	for _, key := range []string{"profile", "execution_scope"} {
		other := map[string]any{}
		for k, v := range payload {
			other[k] = v
		}
		other[key] = "spec"
		if err := addBinding(plan(), makeBinding(other)); err == nil {
			t.Fatalf("wrong %s accepted", key)
		}
	}
	root := t.TempDir()
	old := []byte(`{"schema_version":2,"plugin":"yss-backend-delivery","profile":"spec"}`)
	if err := os.WriteFile(filepath.Join(root, ".yss-backend-plugin.json"), old, 0600); err != nil {
		t.Fatal(err)
	}
	replacement := makeBinding(payload)
	replacement.PreviousDigest = safefs.Digest(old)
	p = plan()
	p.Root = root
	if err := addBinding(p, replacement); err == nil {
		t.Fatal("legacy Spec binding must not become Backend")
	}
	after, err := os.ReadFile(filepath.Join(root, ".yss-backend-plugin.json"))
	if err != nil || string(after) != string(old) {
		t.Fatal("legacy binding changed")
	}

}
