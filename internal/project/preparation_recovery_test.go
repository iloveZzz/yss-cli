package project_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/project"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func TestProjectPreparingProcessHelper(t *testing.T) {
	root := os.Getenv("YSS_PROJECT_PREPARING_ROOT")
	if root == "" {
		return
	}
	b, err := bundle.Load("spec")
	if err != nil {
		t.Fatal(err)
	}
	refs := make([]string, 0, len(b.Files))
	for ref := range b.Files {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	if len(refs) > 100 {
		refs = refs[:100]
	}
	command := os.Getenv("YSS_PROJECT_PREPARING_COMMAND")
	if command == "" {
		command = "init"
	}
	p, err := project.Build(root, "spec", command, nil, refs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = project.Apply(p); err != nil {
		t.Fatal(err)
	}
}

func preparingProject(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "project")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	var err error
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	interruptPreparation(t, root, "init")
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != ".yss" {
		t.Fatalf("preparation wrote target assets: %v %v", entries, err)
	}
	return root
}
func interruptPreparation(t *testing.T, root, command string) {
	t.Helper()
	child := exec.Command(os.Args[0], "-test.run=^TestProjectPreparingProcessHelper$")
	child.Env = append(os.Environ(), "YSS_PROJECT_PREPARING_ROOT="+root, "YSS_PROJECT_PREPARING_COMMAND="+command)
	var output bytes.Buffer
	child.Stdout, child.Stderr = &output, &output
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	})
	deadline := time.Now().Add(120 * time.Second)
	for {
		refs, _ := filepath.Glob(filepath.Join(root, ".yss/transactions/.preparing-*"))
		if len(refs) > 0 {
			break
		}
		if time.Now().After(deadline) {
			_ = child.Process.Kill()
			_ = child.Wait()
			waited = true
			t.Fatalf("process did not enter preparation: %s", output.String())
		}
		time.Sleep(time.Millisecond)
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err == nil {
		t.Fatalf("process completed before SIGKILL: %s", output.String())
	}
	waited = true
}
func preparationTree(t *testing.T, root string) map[string]domain.Descriptor {
	t.Helper()
	files := map[string]domain.Descriptor{}
	err := filepath.WalkDir(root, func(p string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			return nil
		}
		ref, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			files[filepath.ToSlash(ref)] = domain.Descriptor{Type: "symlink", Digest: safefs.Digest([]byte(target)), Mode: uint32(info.Mode().Perm())}
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		d := domain.Descriptor{Type: "file", Digest: safefs.Digest(data), Mode: uint32(info.Mode().Perm())}
		files[filepath.ToSlash(ref)] = d
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestInterruptedPreparationPreviewSealRepeatAndRetryInitialization(t *testing.T) {
	root := preparingProject(t)
	before := preparationTree(t, root)
	preview, handled, err := project.RecoverPreparation(context.Background(), root, "spec", false)
	if err != nil || !handled || len(preview.Preparations) == 0 {
		t.Fatalf("preparation preview: %+v %v %v", preview, handled, err)
	}
	if !reflect.DeepEqual(before, preparationTree(t, root)) {
		t.Fatal("preview mutated preparation evidence")
	}
	sealed, handled, err := project.RecoverPreparation(context.Background(), root, "spec", true)
	if err != nil || !handled || sealed.Status != "sealed" || len(sealed.SealedPreparations) != 1 {
		t.Fatalf("preparation recovery: %+v %v %v", sealed, handled, err)
	}
	sealedTree := preparationTree(t, root)
	repeated, handled, err := project.RecoverPreparation(context.Background(), root, "spec", true)
	if err != nil || !handled || repeated.Status != "unchanged" || !reflect.DeepEqual(sealedTree, preparationTree(t, root)) {
		t.Fatalf("repeat seal changed evidence: %+v %v %v", repeated, handled, err)
	}
	p, err := project.Build(root, "spec", "init", nil, nil)
	if err != nil {
		t.Fatalf("retry init remained blocked after seal: %v", err)
	}
	if _, err = project.Apply(p); err != nil {
		t.Fatalf("retry init apply: %v", err)
	}
	id, err := project.Detect(root, "spec", false)
	if err != nil || id.Native == nil {
		t.Fatalf("retry installed identity: %+v %v", id, err)
	}
}

func preparationCode(err error) string {
	var e *domain.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}
func assertPreparationRefusal(t *testing.T, root, profile, want string) {
	t.Helper()
	before := preparationTree(t, root)
	_, _, err := project.RecoverPreparation(context.Background(), root, profile, true)
	if preparationCode(err) != want {
		t.Fatalf("wanted %s, got %v", want, err)
	}
	if !reflect.DeepEqual(before, preparationTree(t, root)) {
		t.Fatal("refusal changed original evidence/target bytes or mode")
	}
}
func TestPreparationRefusesUserAssetsUnknownEvidenceAndForeignProfile(t *testing.T) {
	root := preparingProject(t)
	assertPreparationRefusal(t, root, "backend", "IDENTITY")
	assertPreparationRefusal(t, root, "", "IDENTITY")
	user := filepath.Join(root, "business.bin")
	if err := os.WriteFile(user, []byte{0, 255, 10}, 0600); err != nil {
		t.Fatal(err)
	}
	assertPreparationRefusal(t, root, "spec", "CONCURRENT")
	if err := os.Remove(user); err != nil {
		t.Fatal(err)
	}
	refs, _ := filepath.Glob(filepath.Join(root, ".yss/transactions/.preparing-*"))
	unknown := filepath.Join(refs[0], "user.txt")
	if err := os.WriteFile(unknown, []byte("never discard"), 0600); err != nil {
		t.Fatal(err)
	}
	assertPreparationRefusal(t, root, "spec", "STATE")
	if err := os.Remove(unknown); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(refs[0], "linked-evidence")
	outside := filepath.Join(t.TempDir(), "evidence")
	if err := os.WriteFile(outside, []byte("preserve outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	assertPreparationRefusal(t, root, "spec", "STATE")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	receipt := filepath.Join(refs[0], "seal.json")
	if err := os.WriteFile(receipt, []byte("not a receipt"), 0600); err != nil {
		t.Fatal(err)
	}
	assertPreparationRefusal(t, root, "spec", "STATE")
	if err := os.Remove(receipt); err != nil {
		t.Fatal(err)
	}
	wal := filepath.Join(refs[0], "intent.wal")
	if err := os.WriteFile(wal, []byte("unexpected target intent"), 0600); err != nil {
		t.Fatal(err)
	}
	assertPreparationRefusal(t, root, "spec", "STATE")
	if err := os.Remove(wal); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	before := preparationTree(t, root)
	if _, _, err := project.RecoverPreparation(ctx, root, "spec", true); preparationCode(err) != "CANCELLED" {
		t.Fatalf("cancelled recovery: %v", err)
	}
	if !reflect.DeepEqual(before, preparationTree(t, root)) {
		t.Fatal("cancelled recovery wrote state")
	}
}
func TestInstalledPreparationPreservesAllTargetsAndRechecksGuards(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := project.Build(root, "spec", "init", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = project.Apply(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git/index"), []byte("index bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "business.bin"), []byte{0, 254, 10}, 0751); err != nil {
		t.Fatal(err)
	}
	interruptPreparation(t, root, "skills")
	before := preparationTree(t, root)
	preview, handled, err := project.RecoverPreparation(context.Background(), root, "spec", false)
	if err != nil || !handled || preview.Status != "preparing" {
		t.Fatalf("installed preview: %+v %v", preview, err)
	}
	if !reflect.DeepEqual(before, preparationTree(t, root)) {
		t.Fatal("installed preview wrote state")
	}
	path := filepath.Join(root, "CONTEXT.md")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(append([]byte{}, original...), []byte("\nuser edit\n")...), info.Mode()); err != nil {
		t.Fatal(err)
	}
	assertPreparationRefusal(t, root, "spec", "INPUT_DRIFT")
	if err := os.WriteFile(path, original, info.Mode()); err != nil {
		t.Fatal(err)
	}
	modeDrift := os.FileMode(0600)
	if runtime.GOOS == "windows" {
		modeDrift = 0444 // A read-only attribute is the representable permission change.
	}
	if err := os.Chmod(path, modeDrift); err != nil {
		t.Fatal(err)
	}
	assertPreparationRefusal(t, root, "spec", "INPUT_DRIFT")
	if err := os.Chmod(path, info.Mode()); err != nil {
		t.Fatal(err)
	}
	sealed, handled, err := project.RecoverPreparation(context.Background(), root, "spec", true)
	if err != nil || !handled || sealed.Status != "sealed" {
		t.Fatalf("installed seal: %+v %v", sealed, err)
	}
	after := preparationTree(t, root)
	for ref, d := range before {
		if len(ref) < 5 || ref[:5] != ".yss/" {
			if after[ref] != d {
				t.Fatalf("target %s changed", ref)
			}
		}
	}
	receipts, _ := filepath.Glob(filepath.Join(root, ".yss/transactions/.sealed-preparation-*/seal.json"))
	var receipt struct {
		NoTargetWrites   bool `json:"noTargetWrites"`
		BaselineVerified bool `json:"baselineVerified"`
	}
	b, err := os.ReadFile(receipts[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &receipt); err != nil || !receipt.NoTargetWrites || !receipt.BaselineVerified {
		t.Fatalf("receipt lacks proof: %s %v", b, err)
	}
	if _, handled, err := project.RecoverPreparation(context.Background(), root, "spec", true); err != nil || handled {
		t.Fatalf("installed sealed history hijacked normal recovery: %v %v", handled, err)
	}
	p, err = project.Build(root, "spec", "skills", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = project.Apply(p); err != nil {
		t.Fatalf("future apply remained blocked: %v", err)
	}
}
