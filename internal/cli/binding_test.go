package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"os"
	"path/filepath"
	"testing"
)

func TestPluginBindingTravelsWithSavedInitAndRollback(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(base, "project")
	binding := currentBackendBinding(t)
	raw, _ := json.Marshal(map[string]any{"schemaVersion": 1, "path": ".yss-backend-plugin.json", "data": base64.StdEncoding.EncodeToString(binding)})
	input := filepath.Join(base, "binding.json")
	plan := filepath.Join(base, "plan.json")
	_ = os.WriteFile(input, raw, 0644)
	var out, errout bytes.Buffer
	if code := Run(context.Background(), []string{"init", "--profile", "spec", "--root", root, "--binding-file", input, "--plan", "--out", plan, "--json"}, &out, &errout); code != 0 {
		t.Fatalf("plan exit %d: %s %s", code, out.String(), errout.String())
	}
	if _, e := os.Stat(root); !os.IsNotExist(e) {
		t.Fatal("plan wrote target")
	}
	_ = os.Remove(input)
	out.Reset()
	if code := Run(context.Background(), []string{"init", "--profile", "spec", "--root", root, "--apply", "--plan-file", plan, "--json"}, &out, &errout); code != 0 {
		t.Fatalf("apply exit %d: %s", code, out.String())
	}
	got, e := os.ReadFile(filepath.Join(root, ".yss-backend-plugin.json"))
	if e != nil || !bytes.Equal(got, binding) {
		t.Fatalf("binding not applied: %q %v", got, e)
	}
	if _, e = os.Stat(filepath.Join(root, ".yss.json")); e != nil {
		t.Fatal(e)
	}
	out.Reset()
	if code := Run(context.Background(), []string{"rollback", "--root", root, "--profile", "spec", "--json"}, &out, &errout); code != 0 {
		t.Fatalf("rollback preview: %s", out.String())
	}
	if _, e = os.Stat(filepath.Join(root, ".yss-backend-plugin.json")); e != nil {
		t.Fatal("preview removed binding")
	}
	out.Reset()
	if code := Run(context.Background(), []string{"rollback", "--root", root, "--profile", "spec", "--apply", "--json"}, &out, &errout); code != 0 {
		t.Fatalf("rollback: %s", out.String())
	}
	for _, ref := range []string{".yss.json", ".yss-backend-plugin.json"} {
		if _, e = os.Stat(filepath.Join(root, ref)); !os.IsNotExist(e) {
			t.Fatalf("rollback retained %s", ref)
		}
	}
	out.Reset()
	if code := Run(context.Background(), []string{"recover", "--root", root, "--profile", "spec", "--apply", "--json"}, &out, &errout); code != 0 {
		t.Fatalf("repeat recovery after restored init: %s", out.String())
	}
	out.Reset()
	if code := Run(context.Background(), []string{"init", "--root", root, "--profile", "spec", "--json"}, &out, &errout); code != 0 {
		t.Fatalf("retry restored initialization: %s", out.String())
	}
	for _, item := range []struct {
		ref       string
		directory bool
	}{{"user-empty-directory", true}, {".agents/skills/user-data.txt", false}} {
		ref := filepath.Join(root, item.ref)
		if item.directory {
			_ = os.MkdirAll(ref, 0755)
		} else {
			_ = os.WriteFile(ref, []byte("user data"), 0600)
		}
		out.Reset()
		if code := Run(context.Background(), []string{"init", "--root", root, "--profile", "spec", "--json"}, &out, &errout); code == 0 || !bytes.Contains(out.Bytes(), []byte(`"code":"CONFLICT"`)) {
			t.Fatalf("unregistered init content accepted: %s", out.String())
		}
		_ = os.Remove(ref)
	}
}

func TestDesignBindingRefusesContradictoryIdentityWithoutWriting(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(base, "project")
	data := []byte(`{"schema_version":2,"plugin":"yss-product-design","profile":"spec"}`)
	raw, _ := json.Marshal(map[string]any{"schemaVersion": 1, "path": ".yss-product-design-plugin.json", "data": base64.StdEncoding.EncodeToString(data)})
	input := filepath.Join(base, "binding.json")
	_ = os.WriteFile(input, raw, 0600)
	var out, errout bytes.Buffer
	if code := Run(context.Background(), []string{"init", "--profile", "design", "--root", root, "--binding-file", input, "--plan", "--json"}, &out, &errout); code == 0 || !bytes.Contains(out.Bytes(), []byte(`"code":"BINDING"`)) {
		t.Fatalf("contradictory design binding accepted: %s", out.String())
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("rejected binding wrote project")
	}
}

func TestPluginBindingConflictAndLaterUserEditsHaveDistinctCodes(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(base, "project")
	call := func(args ...string) (int, map[string]any) {
		t.Helper()
		var out, errout bytes.Buffer
		code := Run(context.Background(), append(args, "--json"), &out, &errout)
		var env map[string]any
		if e := json.Unmarshal(out.Bytes(), &env); e != nil {
			t.Fatal(e, out.String(), errout.String())
		}
		return code, env
	}
	if code, out := call("init", "--profile", "spec", "--root", root); code != 0 {
		t.Fatal(out)
	}
	original := currentBackendBinding(t)
	ref := filepath.Join(root, ".yss-backend-plugin.json")
	_ = os.WriteFile(ref, original, 0644)
	input := filepath.Join(base, "binding.json")
	plan := filepath.Join(base, "plan.json")
	request := map[string]any{"schemaVersion": 1, "path": ".yss-backend-plugin.json", "data": base64.StdEncoding.EncodeToString(append(original[:len(original)-1], byte('\n')))}
	write := func() { raw, _ := json.Marshal(request); _ = os.WriteFile(input, raw, 0644) }
	write()
	if code, out := call("sync", "--root", root, "--binding-file", input, "--plan", "--out", plan); code == 0 || out["code"] != "BINDING_CONFLICT" {
		t.Fatalf("existing binding refusal: %v", out)
	}
	h := sha256.Sum256(original)
	request["previousDigest"] = hex.EncodeToString(h[:])
	write()
	if code, out := call("sync", "--root", root, "--binding-file", input, "--plan", "--out", plan); code != 0 {
		t.Fatal(out)
	}
	user := []byte("{\"user\":\"edited after preview\"}\n")
	_ = os.WriteFile(ref, user, 0644)
	if code, out := call("sync", "--root", root, "--apply", "--plan-file", plan); code == 0 || out["code"] != "INPUT_DRIFT" {
		t.Fatalf("later user edit refusal: %v", out)
	}
	got, _ := os.ReadFile(ref)
	if !bytes.Equal(got, user) {
		t.Fatal("later binding user edit overwritten")
	}
}

func TestFullSpecInitializationHasStableDoctorAndTracker(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(base, "project")
	var out, errout bytes.Buffer
	if code := Run(context.Background(), []string{"init", "--profile", "spec", "--root", root, "--full", "--issue-tracker", "github", "--json"}, &out, &errout); code != 0 {
		t.Fatalf("init: %s %s", out.String(), errout.String())
	}
	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil || !bytes.Contains(readme, []byte("默认 Issue Tracker：github")) {
		t.Fatalf("tracker read view mismatch: %v", err)
	}
	out.Reset()
	if code := Run(context.Background(), []string{"doctor", "--profile", "spec", "--root", root, "--json"}, &out, &errout); code != 0 {
		t.Fatalf("doctor: %s", out.String())
	}
	var envelope struct {
		Result struct {
			Plan struct {
				Changes   []any
				Conflicts []any
			}
		}
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Result.Plan.Changes) != 0 || len(envelope.Result.Plan.Conflicts) != 0 {
		t.Fatalf("initialization drift: %s", out.String())
	}
}

func TestCanceledInitUsesThePublicCancellationCode(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(base, "project")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, errout bytes.Buffer
	code := Run(ctx, []string{"init", "--profile", "spec", "--root", root, "--json"}, &out, &errout)
	var env map[string]any
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if code == 0 || env["code"] != "CANCELLED" {
		t.Fatalf("wrong public cancellation: %s", out.String())
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("cancelled init created its target")
	}
}

func currentBackendBinding(t *testing.T) []byte {
	t.Helper()
	source, err := bundle.Load("spec")
	if err != nil {
		t.Fatal(err)
	}
	binaryDigest, err := domain.ExecutableDigest()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"schema_version": 2, "plugin": "yss-backend-delivery", "execution_scope": "plan-to-backend", "profile": "spec", "template_commit": source.TemplateCommit, "bundle_hash": source.BundleHash, "binary_sha256": binaryDigest})
	if err != nil {
		t.Fatal(err)
	}
	return append(raw, '\n')
}

func TestBoundProjectDirectSyncGuardsReceiptAndRequiresUpgradeForSourceChange(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(base, "project")
	binding := currentBackendBinding(t)
	request, _ := json.Marshal(map[string]any{"schemaVersion": 1, "path": ".yss-backend-plugin.json", "data": base64.StdEncoding.EncodeToString(binding)})
	input := filepath.Join(base, "binding.json")
	_ = os.WriteFile(input, request, 0600)
	call := func(args ...string) (int, map[string]any) {
		t.Helper()
		var out, errout bytes.Buffer
		code := Run(context.Background(), append(args, "--json"), &out, &errout)
		var env map[string]any
		if err := json.Unmarshal(out.Bytes(), &env); err != nil {
			t.Fatal(err, out.String())
		}
		return code, env
	}
	if code, out := call("init", "--root", root, "--profile", "spec", "--binding-file", input); code != 0 {
		t.Fatal(out)
	}
	plan := filepath.Join(base, "sync.json")
	if code, out := call("sync", "--root", root, "--plan", "--out", plan); code != 0 {
		t.Fatal(out)
	}
	receipt := filepath.Join(root, ".yss-backend-plugin.json")
	edited := append(append([]byte{}, binding...), ' ')
	_ = os.WriteFile(receipt, edited, 0600)
	if code, out := call("sync", "--root", root, "--apply", "--plan-file", plan); code == 0 || out["code"] != "INPUT_DRIFT" {
		t.Fatalf("unguarded receipt edit: %v", out)
	}
	got, _ := os.ReadFile(receipt)
	if !bytes.Equal(got, edited) {
		t.Fatal("receipt overwritten")
	}
	_ = os.WriteFile(receipt, binding, 0644)
	_ = os.Chmod(receipt, 0644)
	// A previous executable can have equal version/commit and still need a new
	// public plugin binding. This is a legitimate historical receipt shape.
	var old map[string]any
	_ = json.Unmarshal(binding, &old)
	old["binary_sha256"] = string(bytes.Repeat([]byte("a"), 64))
	historical, _ := json.Marshal(old)
	_ = os.WriteFile(receipt, historical, 0644)
	if code, out := call("sync", "--root", root, "--plan"); code == 0 || out["code"] != "BINDING_REQUIRED" {
		t.Fatalf("source changed without plugin upgrade: %v", out)
	}
	got, _ = os.ReadFile(receipt)
	if !bytes.Equal(got, historical) {
		t.Fatal("refused upgrade wrote binding")
	}
	old["template_commit"] = string(bytes.Repeat([]byte("b"), 40))
	inconsistent, _ := json.Marshal(old)
	_ = os.WriteFile(receipt, inconsistent, 0644)
	if code, out := call("sync", "--root", root, "--plan"); code == 0 || out["code"] != "BINDING_CONFLICT" {
		t.Fatalf("inconsistent identity accepted: %v", out)
	}
}
