package transaction_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/cli"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

func TestValidationFailureRestoresDeleteRenameMetadataAndBindingWithModes(t *testing.T) {
	root := testRoot(t)
	for _, ref := range []string{"old", ".yss.json", ".yss-backend-plugin.json"} {
		if e := os.WriteFile(filepath.Join(root, ref), []byte("before:"+ref), 0751); e != nil {
			t.Fatal(e)
		}
	}
	ops := []transaction.Operation{{Path: "new", Data: []byte("candidate"), Mode: 0644}}
	before := map[string]domain.Descriptor{}
	for _, ref := range []string{"old", ".yss.json", ".yss-backend-plugin.json"} {
		d, _ := safefs.Describe(root, ref)
		before[ref] = d
		ops = append(ops, transaction.Operation{Path: ref, Before: &d, Delete: ref == "old", Data: []byte("after"), Mode: 0644})
	}
	// Delete never carries candidate data.
	ops[1].Data = nil
	base := []byte("canonical base")
	descriptor := domain.Descriptor{Type: "file", Digest: safefs.Digest(base), Mode: 0644}
	artifacts := []transaction.Artifact{{ArtifactRecord: transaction.ArtifactRecord{Path: "entry", Kind: "baseline", Descriptor: descriptor}, Data: base}}
	r, e := transaction.ApplyContextWithValidation(context.Background(), root, "sync", ops, nil, artifacts, func() error {
		if _, e := os.Stat(filepath.Join(root, "old")); !os.IsNotExist(e) {
			t.Fatal("validation did not observe post-write state")
		}
		return domain.Fail("VERIFY", "deliberate failed postcondition")
	})
	if errorCode(e) != "VERIFY" || r.FileApplication != "restored" || r.Verification != "failed" {
		t.Fatalf("failure did not restore: %+v %v", r, e)
	}
	for ref, want := range before {
		got, _ := safefs.Describe(root, ref)
		if got != want {
			t.Fatalf("lost bytes/mode: %s %+v %+v", ref, got, want)
		}
	}
	if _, e := os.Stat(filepath.Join(root, "new")); !os.IsNotExist(e) {
		t.Fatal("rollback kept renamed target")
	}
	raw, _, ok, e := transaction.BaselineMaterial(root, "entry", descriptor)
	if e != nil || !ok || string(raw) != string(base) {
		t.Fatalf("canonical material unavailable: %q %v", raw, e)
	}
	if _, e = transaction.Apply(root, "sync", []transaction.Operation{{Path: ".yss/ordinary-write", Data: base}}); errorCode(e) != "PROTECTED" {
		t.Fatalf("material support relaxed state protection: %v", e)
	}
}

func TestPublicAttachCancellationReceiptAndPreparationRecovery(t *testing.T) {
	publicAttachPreparationRecovery(t, "objects")
}

func TestPublicAttachKilledPartialHeaderAndPreparationRecovery(t *testing.T) {
	publicAttachPreparationRecovery(t, "header")
}

func TestPublicAttachHeaderCrashHelper(t *testing.T) {
	root := os.Getenv("YSS_ATTACH_HEADER_CRASH_ROOT")
	if root == "" {
		return
	}
	defer transaction.SetTemporaryWriterForTest(func(file *os.File, raw []byte) (int, error) {
		if !strings.Contains(filepath.ToSlash(file.Name()), "/transactions/.preparation-") {
			return file.Write(raw)
		}
		n, err := file.Write(raw[:min(4096, len(raw)/2)])
		if err != nil {
			return n, err
		}
		if err = retainProducerWrite(file); err != nil {
			return n, err
		}
		remaining, err := file.Write(raw[n:])
		return n + remaining, err
	})()
	plan := filepath.Join(filepath.Dir(root), filepath.Base(root)+"-plan.json")
	var out, stderr bytes.Buffer
	if code := cli.Run(context.Background(), []string{"attach", "--root", root, "--apply", "--plan-file", plan, "--json"}, &out, &stderr); code != 0 {
		t.Fatal(out.String(), stderr.String())
	}
}

func publicAttachPreparationRecovery(t *testing.T, stage string) {
	t.Helper()
	root := testRoot(t)
	if e := os.WriteFile(filepath.Join(root, "business"), []byte("original business"), 0751); e != nil {
		t.Fatal(e)
	}
	before, _ := safefs.Describe(root, "business")
	plan := filepath.Join(filepath.Dir(root), filepath.Base(root)+"-plan.json")
	var out, stderr bytes.Buffer
	if code := cli.Run(context.Background(), []string{"attach", "--root", root, "--profile", "spec", "--plan", "--out", plan, "--json"}, &out, &stderr); code != 0 {
		t.Fatal(out.String(), stderr.String())
	}
	var partial []byte
	if stage == "header" {
		killObservedProducer(t, root, "TestPublicAttachHeaderCrashHelper", "YSS_ATTACH_HEADER_CRASH_ROOT", func(ref string) {
			if !strings.Contains(filepath.ToSlash(ref), "/transactions/.preparation-") {
				t.Fatal("actual partial header missing")
			}
			partial, _ = os.ReadFile(ref)
			if len(partial) != 4096 || json.Valid(partial) {
				t.Fatal("observed bytes are not a partial producer header")
			}
		})
	} else {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		restore := transaction.SetTemporaryWriterForTest(func(file *os.File, raw []byte) (int, error) {
			n, e := file.Write(raw)
			if strings.Contains(filepath.ToSlash(file.Name()), "/objects/") {
				cancel() // material archive, before any project write
			}
			return n, e
		})
		out.Reset()
		code := cli.Run(ctx, []string{"attach", "--root", root, "--apply", "--plan-file", plan, "--json"}, &out, &stderr)
		restore()
		var env map[string]any
		if e := json.Unmarshal(out.Bytes(), &env); e != nil {
			t.Fatal(e)
		}
		if code == 0 || env["code"] != "CANCELLED" || env["result"].(map[string]any)["fileApplication"] != "not-applied" || env["result"].(map[string]any)["verification"] != "not-run" {
			t.Fatalf("preparation failure receipt missing: %s", out.String())
		}
	}
	for _, apply := range []bool{false, true, true} {
		args := []string{"recover", "--root", root, "--profile", "spec", "--json"}
		if apply {
			args = append(args, "--apply")
		}
		out.Reset()
		if code := cli.Run(context.Background(), args, &out, &stderr); code != 0 {
			t.Fatal(out.String(), stderr.String())
		}
	}
	after, _ := safefs.Describe(root, "business")
	if before != after {
		t.Fatal("preparation recovery changed business")
	}
	if stage == "header" {
		files, _ := filepath.Glob(filepath.Join(root, ".yss/transactions/.sealed-preparation-*/header-partial-*"))
		if len(files) != 1 {
			t.Fatal("partial header recovery evidence missing")
		}
		raw, _ := os.ReadFile(files[0])
		info, _ := os.Stat(files[0])
		if !bytes.Equal(raw, partial) || info.Mode().Perm() != 0600 {
			t.Fatal("sealed partial header lost original bytes or mode")
		}
	}
	out.Reset()
	if code := cli.Run(context.Background(), []string{"attach", "--root", root, "--apply", "--plan-file", plan, "--json"}, &out, &stderr); code != 0 {
		t.Fatal("recovered first attach cannot retry", out.String(), stderr.String())
	}
}
