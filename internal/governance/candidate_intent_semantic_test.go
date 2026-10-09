package governance

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

func candidateIntentTestRoot(t *testing.T) (string, string, string, string) {
	t.Helper()
	root, cpRef := progressionFixture(t)
	sliceRef := "source/current-slice.yaml"
	apTestPut(t, root, ".yss.json", map[string]any{"schemaVersion": 1, "protocolVersion": 1, "profile": "spec", "profileId": "harness.spec-template", "cliVersion": "1.3.2", "templateVersion": "3.5.10", "legacyCliVersion": "3.5.10", "templateCommit": strings.Repeat("1", 40), "snapshotHash": strings.Repeat("2", 64), "manifestHash": strings.Repeat("3", 64), "templateSourceState": "committed", "variables": map[string]any{}, "distribution": map[string]any{}, "managedFiles": map[string]any{}, "baselineDigest": safefs.Digest([]byte("{}"))})
	cp := semMap(mustParseContract(mustReadSpecBaselineTestFile(t, filepath.Join(root, cpRef))))
	cp["artifacts"] = map[string]any{"artifact.slice-implementation-contract": map[string]any{"ref": sliceRef}}
	apTestPut(t, root, cpRef, cp)
	apTestPut(t, root, sliceRef, "Synthetic registered asset; this test does not approve a Slice.")
	apTestPut(t, root, "apps/frontend/app.js", "export const value = 1;\n")
	apTestPut(t, root, "pom.xml", "<project/>\n")
	apTestPut(t, root, "target-input.json", progressionTestInput(cpRef, "spec-approved"))
	backendTestGit(t, root, "init")
	backendTestGit(t, root, "config", "user.email", "synthetic@example.invalid")
	backendTestGit(t, root, "config", "user.name", "synthetic")
	backendTestGit(t, root, "add", ".")
	backendTestGit(t, root, "commit", "-m", "Synthetic immutable initial candidate")
	head := backendTestGit(t, root, "rev-parse", "HEAD")
	return root, cpRef, sliceRef, head
}
func candidateIntentTestApply(t *testing.T, root, cpRef string) {
	t.Helper()
	plan := filepath.Join(t.TempDir(), "target-plan.json")
	if _, err := progressionTargetRun(context.Background(), root, map[string]string{"checkpoint": cpRef, "input": "target-input.json", "plan": "true", "out": plan}); err != nil {
		t.Fatal(err)
	}
	if _, err := progressionTargetRun(context.Background(), root, map[string]string{"apply": "true", "plan-file": plan}); err != nil {
		t.Fatal(err)
	}
}
func candidateGitReadonlyInventory(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	if err := filepath.WalkDir(filepath.Join(root, ".git"), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		value := fmt.Sprintf("%s:%d", info.Mode(), info.ModTime().UnixNano())
		if !d.IsDir() {
			raw, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			value += ":" + safefs.Digest(raw)
		}
		result[p] = value
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return result
}
func TestImplementationCandidateOnlyVerifiedIntentMayChange(t *testing.T) {
	root, cpRef, sliceRef, head := candidateIntentTestRoot(t)
	tree := backendTestGit(t, root, "rev-parse", head+"^{tree}")
	candidateIntentTestApply(t, root, cpRef)
	verify := func() error {
		s := newSemanticSession(context.Background(), root, nil)
		_, err := candidateCommittedCurrent(s, root, head, tree, sliceRef, "FRONTEND_CANDIDATE")
		if err == nil {
			err = s.finish()
		}
		return err
	}
	beforeGit := candidateGitReadonlyInventory(t, root)
	before := progressionInventory(t, root)
	if err := verify(); err != nil {
		t.Fatalf("target and verified archive invalidated original committed candidate: %v", err)
	}
	if !reflect.DeepEqual(beforeGit, candidateGitReadonlyInventory(t, root)) || !reflect.DeepEqual(before, progressionInventory(t, root)) {
		t.Fatal("readonly candidate query wrote source/index/objects or refreshed mtime")
	}
	for _, ref := range []string{"apps/frontend/app.js", "pom.xml", "shared/build.properties", ".work/other/progression-target.json", ".yss/transactions/unrelated/extra.json"} {
		t.Run(ref, func(t *testing.T) {
			file := filepath.Join(root, ref)
			old, err := os.ReadFile(file)
			present := err == nil
			apTestPut(t, root, ref, "Synthetic unexpected source/material change")
			if err := verify(); err == nil {
				t.Fatal("unknown or code change accepted")
			}
			if present {
				apTestPut(t, root, ref, old)
			} else {
				_ = os.Remove(file)
				if strings.HasPrefix(ref, ".yss/transactions/") {
					_ = os.Remove(filepath.Dir(file))
				}
			}
		})
	}
	t.Run("staged-code-restored-in-worktree", func(t *testing.T) {
		ref := "apps/frontend/app.js"
		original := mustReadSpecBaselineTestFile(t, filepath.Join(root, ref))
		apTestPut(t, root, ref, "staged code\n")
		backendTestGit(t, root, "add", ref)
		apTestPut(t, root, ref, original)
		if err := verify(); err == nil {
			t.Fatal("staged unreviewed code hidden by restored working bytes")
		}
		backendTestGit(t, root, "reset", "--", ref)
	})
	t.Run("material-inventory-unknown-member", func(t *testing.T) {
		s := newSemanticSession(context.Background(), root, nil)
		allowed, err := candidateIntentAllowlist(s, root, sliceRef)
		if err != nil {
			t.Fatal(err)
		}
		var file string
		for ref := range allowed {
			if strings.HasSuffix(ref, "/plan.json") {
				file = path.Join(path.Dir(ref), "unknown-seal.json")
			}
		}
		if file == "" {
			t.Fatal("missing actual transaction material")
		}
		apTestPut(t, root, file, "unexpected")
		defer os.Remove(filepath.Join(root, file))
		if err := s.finish(); err == nil {
			t.Fatal("new material escaped final inventory observation")
		}
	})
	t.Run("governance-only-new-commit", func(t *testing.T) {
		backendTestGit(t, root, "add", ".")
		backendTestGit(t, root, "commit", "-m", "Synthetic verified goal intent only")
		if err := verify(); err != nil {
			t.Fatal(err)
		}
		source, err := candidateBuildSource(newSemanticSession(context.Background(), root, nil), root, map[string]any{"slice_contract_ref": sliceRef, "implementation_candidate_ref": head})
		if err != nil || source != head {
			t.Fatalf("build lost original reviewed commit: %s %v", source, err)
		}
	})
}
func TestImplementationWorktreeFullBinaryCandidateIntentAndRollback(t *testing.T) {
	root, cpRef, sliceRef, head := candidateIntentTestRoot(t)
	// Original full patch contains binary, rename and executable-mode changes.
	apTestPut(t, root, "apps/frontend/old.bin", []byte{0, 1, 2, 3})
	backendTestGit(t, root, "add", ".")
	backendTestGit(t, root, "commit", "-m", "Synthetic binary baseline")
	head = backendTestGit(t, root, "rev-parse", "HEAD")
	if err := os.Rename(filepath.Join(root, "apps/frontend/old.bin"), filepath.Join(root, "apps/frontend/new.bin")); err != nil {
		t.Fatal(err)
	}
	apTestPut(t, root, "apps/frontend/new.bin", []byte{0, 7, 9, 3})
	if err := os.Chmod(filepath.Join(root, "apps/frontend/new.bin"), 0755); err != nil {
		t.Fatal(err)
	}
	backendTestGit(t, root, "add", "apps/frontend")
	original := newSemanticSession(context.Background(), root, nil)
	diff, err := original.git(root, "diff", "--binary", "--full-index", head)
	if err != nil {
		t.Fatal(err)
	}
	extra := []byte("Synthetic original untracked implementation\n")
	apTestPut(t, root, "apps/frontend/untracked.txt", extra)
	var stream bytes.Buffer
	stream.WriteString("YSS-WORKTREE-CANDIDATE-V1\x00")
	stream.WriteByte(0x54)
	_ = binary.Write(&stream, binary.BigEndian, uint64(len(diff)))
	stream.Write(diff)
	stream.WriteByte(0x55)
	_ = binary.Write(&stream, binary.BigEndian, uint64(len("apps/frontend/untracked.txt")))
	stream.WriteString("apps/frontend/untracked.txt")
	_ = binary.Write(&stream, binary.BigEndian, uint32(0100644))
	stream.WriteByte(0x52)
	_ = binary.Write(&stream, binary.BigEndian, uint64(len(extra)))
	stream.Write(extra)
	base := ".template-source/evidence/maintenance/intent-candidate"
	ref := base + "/candidate-manifest.yaml"
	digest := safefs.Digest(stream.Bytes())
	apTestPut(t, root, ref, map[string]any{"schema_version": 1, "candidate_kind": "yss-worktree-candidate-v1", "storage": "packed-stream", "review_mode": "worktree", "review_base_ref": head, "merge_base": head, "implementation_candidate_ref": "working-tree", "candidate_snapshot_ref": ref, "candidate_digest": digest, "tracked_diff_command": "git diff synthetic", "commit_list_command": "git log synthetic", "untracked_inventory_command": "git ls-files synthetic", "untracked_diff_command": "packed in candidate.bin", "untracked_files": []any{"apps/frontend/untracked.txt"}, "untracked_path_bytes": []any{base64.StdEncoding.EncodeToString([]byte("apps/frontend/untracked.txt"))}, "excluded_paths": []any{base}, "snapshot_stream_ref": base + "/candidate.bin", "tracked_diff_ref": base + "/tracked.diff"})
	apTestPut(t, root, base+"/candidate.bin", stream.Bytes())
	apTestPut(t, root, base+"/tracked.diff", diff)
	input := map[string]any{"slice_contract_ref": sliceRef, "candidate_snapshot_ref": ref, "candidate_digest": digest}
	originalNames, err := original.git(root, "diff", "--name-only", head)
	if err != nil {
		t.Fatal(err)
	}
	verify := func() error {
		s := newSemanticSession(context.Background(), root, nil)
		_, err := backendWorktreeCurrent(s, root, input)
		if err == nil {
			coverageInput := map[string]any{"scope_kind": "change", "review_mode": "worktree", "slice_contract_ref": sliceRef, "candidate_snapshot_ref": ref, "candidate_digest": digest}
			intent, e := candidateCoverageIntent(s, root, coverageInput, map[string]any{"source_head": head})
			if e != nil {
				return e
			}
			if intent != nil {
				names, e := candidateCoverageTrackedPaths(s, root, head, intent)
				if e != nil {
					return e
				}
				if !bytes.Equal(names, originalNames) {
					return fmt.Errorf("original packed Coverage path spelling changed: %q != %q", names, originalNames)
				}
			}
			err = s.finish()
		}
		return err
	}
	if err := verify(); err != nil {
		t.Fatalf("original full worktree candidate: %v", err)
	}
	candidateIntentTestApply(t, root, cpRef)
	beforeGit := candidateGitReadonlyInventory(t, root)
	before := progressionInventory(t, root)
	if err := verify(); err != nil {
		t.Fatalf("verified intent invalidated full original packed candidate: %v", err)
	}
	if !reflect.DeepEqual(beforeGit, candidateGitReadonlyInventory(t, root)) || !reflect.DeepEqual(before, progressionInventory(t, root)) {
		t.Fatal("private reconstruction changed source Git/index/object mtimes or worktree bytes")
	}
	for _, variant := range []string{"binary-bytes", "mode", "outside-shared-source", "untracked-bytes", "extra-untracked", "unknown-target-txn"} {
		t.Run(variant, func(t *testing.T) {
			ref := "apps/frontend/new.bin"
			switch variant {
			case "outside-shared-source":
				ref = "pom.xml"
			case "untracked-bytes":
				ref = "apps/frontend/untracked.txt"
			case "extra-untracked":
				ref = "apps/frontend/new-source.js"
			case "unknown-target-txn":
				ref = ".yss/transactions/other-kind/unknown"
			}
			file := filepath.Join(root, ref)
			old, err := os.ReadFile(file)
			present := err == nil
			info, _ := os.Lstat(file)
			if variant == "mode" {
				_ = os.Chmod(file, 0644)
			} else {
				apTestPut(t, root, ref, "Synthetic new uncaptured bytes")
			}
			if err := verify(); err == nil {
				t.Fatal("non-intent full-scope drift accepted")
			}
			if present {
				apTestPut(t, root, ref, old)
				_ = os.Chmod(file, info.Mode())
			} else {
				_ = os.Remove(file)
				if strings.HasPrefix(ref, ".yss/transactions/") {
					_ = os.Remove(filepath.Dir(file))
				}
			}
		})
	}
	if _, err := transaction.Rollback(root); err != nil {
		t.Fatal(err)
	}
	if err := verify(); err != nil {
		t.Fatalf("valid rollback metadata invalidated original candidate: %v", err)
	}
	if !bytes.Equal(diff, mustReadSpecBaselineTestFile(t, filepath.Join(root, base+"/tracked.diff"))) || !bytes.Equal(stream.Bytes(), mustReadSpecBaselineTestFile(t, filepath.Join(root, base+"/candidate.bin"))) {
		t.Fatal("goal changes rewrote original full raw candidate")
	}
}

func TestImplementationCoverageKeepsSignedIdentityAndOnlyOriginalIntentRows(t *testing.T) {
	root, cpRef, sliceRef, head := candidateIntentTestRoot(t)
	candidateIntentTestApply(t, root, cpRef)
	s := newSemanticSession(context.Background(), root, nil)
	input := map[string]any{"scope_kind": "change", "review_mode": "committed", "slice_contract_ref": sliceRef, "implementation_candidate_ref": head}
	recorded := map[string]any{"source_head": head}
	intent, err := candidateCoverageIntent(s, root, input, recorded)
	if err != nil {
		t.Fatal(err)
	}
	configRef := filepath.ToSlash(filepath.Join(filepath.Dir(cpRef), progressionFile))
	code := map[string]any{"path": "apps/frontend/app.js", "digest": "original-code", "mode": 0100644}
	oldIntent := map[string]any{"path": configRef, "digest": "original-intent-before-capture", "mode": 0100644}
	current := map[string]any{"inventory": []any{code, map[string]any{"path": configRef, "digest": "current-intent", "mode": 0100644}}, "changed_paths": []string{"apps/frontend/app.js"}}
	recorded["inventory"] = []any{code, oldIntent}
	recorded["changed_paths"] = []string{configRef, "apps/frontend/app.js"}
	candidateRestoreCoverageMaterials(current, recorded, intent)
	if !reflect.DeepEqual(current["inventory"], recorded["inventory"]) || !reflect.DeepEqual(current["changed_paths"], []string{configRef, "apps/frontend/app.js"}) {
		t.Fatalf("original signed metadata rows or legacy path spelling changed: %#v", current)
	}
	if err = s.finish(); err != nil {
		t.Fatal(err)
	}
	t.Run("caller-head-cannot-replace-original-signed-head", func(t *testing.T) {
		bad := map[string]any{"source_head": strings.Repeat("a", 40)}
		if _, err := candidateCoverageIntent(newSemanticSession(context.Background(), root, nil), root, input, bad); err == nil {
			t.Fatal("unbound source head accepted")
		}
	})
	t.Run("worktree-head-binds-raw-coverage-then-fullrepo-drift", func(t *testing.T) {
		worktree := map[string]any{"scope_kind": "change", "review_mode": "worktree", "slice_contract_ref": sliceRef}
		if _, err := candidateCoverageIntent(newSemanticSession(context.Background(), root, nil), root, worktree, recorded); err != nil {
			t.Fatal(err)
		}
		apTestPut(t, root, "pom.xml", "Synthetic parent build changed\n")
		backendTestGit(t, root, "add", "pom.xml")
		backendTestGit(t, root, "commit", "-m", "Synthetic unreviewed parent build commit")
		if _, err := candidateCoverageIntent(newSemanticSession(context.Background(), root, nil), root, worktree, recorded); err == nil {
			t.Fatal("worktree coverage reused changed parent build source head")
		}
	})
}

func TestImplementationIntentRenameCannotHideSourceDeletion(t *testing.T) {
	root, cpRef, sliceRef, _ := candidateIntentTestRoot(t)
	candidateIntentTestApply(t, root, cpRef)
	configRef := filepath.ToSlash(filepath.Join(filepath.Dir(cpRef), progressionFile))
	raw := mustReadSpecBaselineTestFile(t, filepath.Join(root, configRef))
	sourceRef := "approved-source.json"
	apTestPut(t, root, sourceRef, raw)
	backendTestGit(t, root, "add", sourceRef)
	backendTestGit(t, root, "commit", "-m", "Synthetic immutable source equal to current goal bytes")
	head := backendTestGit(t, root, "rev-parse", "HEAD")
	tree := backendTestGit(t, root, "rev-parse", head+"^{tree}")
	if err := os.Rename(filepath.Join(root, sourceRef), filepath.Join(root, configRef)); err != nil {
		t.Fatal(err)
	}
	backendTestGit(t, root, "add", "--", sourceRef, configRef)
	s := newSemanticSession(context.Background(), root, nil)
	current, err := s.git(root, "diff", "--binary", "--full-index", head)
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := candidateIntentAllowlist(s, root, sliceRef)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("original-full-worktree-patch", func(t *testing.T) {
		if err := candidateTrackedIntentDifference(s, root, head, nil, current, allowed); err == nil {
			t.Fatal("rename detection hid non-intent source deletion in full packed candidate")
		}
	})
	backendTestGit(t, root, "commit", "-m", "Synthetic prohibited source renamed to allowed config")
	t.Run("committed-full-tree", func(t *testing.T) {
		if _, err := candidateCommittedCurrent(newSemanticSession(context.Background(), root, nil), root, head, tree, sliceRef, "FRONTEND_CANDIDATE"); err == nil {
			t.Fatal("rename detection hid committed non-intent source deletion")
		}
	})
}

func TestImplementationEmptyDiffDoesNotRefreshSourceIndexStatCache(t *testing.T) {
	root, cpRef, sliceRef, head := candidateIntentTestRoot(t)
	tree := backendTestGit(t, root, "rev-parse", head+"^{tree}")
	file := filepath.Join(root, "apps/frontend/app.js")
	raw := mustReadSpecBaselineTestFile(t, file)
	for _, stage := range []string{"without-target", "with-target", "after-target-rollback"} {
		t.Run(stage, func(t *testing.T) {
			if stage == "with-target" {
				candidateIntentTestApply(t, root, cpRef)
			}
			if stage == "after-target-rollback" {
				if _, err := transaction.Rollback(root); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(file, raw, 0644); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(file)
			if err != nil {
				t.Fatal(err)
			}
			future := info.ModTime().Add(2 * time.Hour)
			if err = os.Chtimes(file, future, future); err != nil {
				t.Fatal(err)
			}
			before := candidateGitReadonlyInventory(t, root)
			s := newSemanticSession(context.Background(), root, nil)
			patch, err := s.git(root, "diff", "--binary", "--full-index", head, "--", "apps/frontend/app.js")
			if err != nil {
				t.Fatal(err)
			}
			if len(patch) != 0 {
				t.Fatalf("identical source returned a binary patch: %q", patch)
			}
			if _, err = candidateCommittedCurrent(s, root, head, tree, sliceRef, "FRONTEND_CANDIDATE"); err != nil {
				t.Fatal(err)
			}
			intent, err := candidateCoverageIntent(s, root, map[string]any{"scope_kind": "change", "review_mode": "committed", "slice_contract_ref": sliceRef, "implementation_candidate_ref": head}, map[string]any{"source_head": head})
			if err != nil {
				t.Fatal(err)
			}
			if intent != nil {
				names, err := candidateCoverageTrackedPaths(s, root, head, intent)
				if err != nil || len(names) != 0 {
					t.Fatalf("identical source stat cache polluted original Coverage names: %q, %v", names, err)
				}
			}
			if err = s.finish(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, candidateGitReadonlyInventory(t, root)) {
				t.Fatal("readonly identical source query refreshed index bytes/mode/mtime")
			}
		})
	}
}

func TestImplementationMissingIntentCannotDeleteCapturedTrackedConfig(t *testing.T) {
	root, cpRef, sliceRef, _ := candidateIntentTestRoot(t)
	candidateIntentTestApply(t, root, cpRef)
	backendTestGit(t, root, "add", ".")
	backendTestGit(t, root, "commit", "-m", "Synthetic candidate captures existing target config")
	head := backendTestGit(t, root, "rev-parse", "HEAD")
	tree := backendTestGit(t, root, "rev-parse", head+"^{tree}")
	if _, err := transaction.Rollback(root); err != nil {
		t.Fatal(err)
	}
	before := candidateGitReadonlyInventory(t, root)
	s := newSemanticSession(context.Background(), root, nil)
	allowed, err := candidateIntentAllowlist(s, root, sliceRef)
	if err != nil {
		t.Fatal(err)
	}
	configRef := path.Join(path.Dir(cpRef), progressionFile)
	if allowed[configRef] || len(allowed) == 0 {
		t.Fatal("missing config was authorized, or valid transaction archive was lost")
	}
	if _, err = candidateCommittedCurrent(s, root, head, tree, sliceRef, "FRONTEND_CANDIDATE"); err == nil {
		t.Fatal("valid rollback authorized deleting a config present in the original candidate")
	}
	if !reflect.DeepEqual(before, candidateGitReadonlyInventory(t, root)) {
		t.Fatal("readonly missing-config refusal changed source Git/index/objects")
	}
}

// The packed path tuple stays project-relative exactly as the original capture
// protocol emits it; only current inventory comparison uses the fixed prefix.
func TestImplementationProperAppsPackedPathsPreserveOriginalCandidate(t *testing.T) {
	root, cpRef, sliceRef, head := candidateIntentTestRoot(t)
	project := filepath.Join(root, "apps/frontend")
	apTestPut(t, project, "app.js", "export const value = 2;\n")
	original := newSemanticSession(context.Background(), root, nil)
	if err := original.registerExternalRoot(project, sliceRef); err != nil {
		t.Fatal(err)
	}
	patch, err := original.git(project, "diff", "--binary", "--full-index", head)
	if err != nil {
		t.Fatal(err)
	}
	entry := []byte("Synthetic captured project-relative untracked bytes\n")
	apTestPut(t, project, "new.txt", entry)
	var stream bytes.Buffer
	stream.WriteString("YSS-WORKTREE-CANDIDATE-V1\x00")
	stream.WriteByte(0x54)
	_ = binary.Write(&stream, binary.BigEndian, uint64(len(patch)))
	stream.Write(patch)
	stream.WriteByte(0x55)
	_ = binary.Write(&stream, binary.BigEndian, uint64(len("new.txt")))
	stream.WriteString("new.txt")
	_ = binary.Write(&stream, binary.BigEndian, uint32(0100644))
	stream.WriteByte(0x52)
	_ = binary.Write(&stream, binary.BigEndian, uint64(len(entry)))
	stream.Write(entry)
	base := ".template-source/evidence/maintenance/proper-apps"
	ref := base + "/candidate-manifest.yaml"
	digest := safefs.Digest(stream.Bytes())
	apTestPut(t, project, ref, map[string]any{"schema_version": 1, "candidate_kind": "yss-worktree-candidate-v1", "storage": "packed-stream", "review_mode": "worktree", "review_base_ref": head, "merge_base": head, "implementation_candidate_ref": "working-tree", "candidate_snapshot_ref": ref, "candidate_digest": digest, "tracked_diff_command": "git diff synthetic", "commit_list_command": "git log synthetic", "untracked_inventory_command": "git ls-files synthetic", "untracked_diff_command": "packed in candidate.bin", "untracked_files": []any{"new.txt"}, "untracked_path_bytes": []any{base64.StdEncoding.EncodeToString([]byte("new.txt"))}, "excluded_paths": []any{base}, "snapshot_stream_ref": base + "/candidate.bin", "tracked_diff_ref": base + "/tracked.diff"})
	apTestPut(t, project, base+"/candidate.bin", stream.Bytes())
	apTestPut(t, project, base+"/tracked.diff", patch)
	input := map[string]any{"slice_contract_ref": sliceRef, "candidate_snapshot_ref": ref, "candidate_digest": digest}
	verify := func() error {
		s := newSemanticSession(context.Background(), root, nil)
		if err := s.registerExternalRoot(project, sliceRef); err != nil {
			return err
		}
		_, err := backendWorktreeCurrent(s, project, input)
		if err == nil {
			err = s.finish()
		}
		return err
	}
	for _, stage := range []string{"original-project-relative", "with-target", "after-rollback"} {
		t.Run(stage, func(t *testing.T) {
			if stage == "with-target" {
				candidateIntentTestApply(t, root, cpRef)
			}
			if stage == "after-rollback" {
				if _, err := transaction.Rollback(root); err != nil {
					t.Fatal(err)
				}
			}
			before := candidateGitReadonlyInventory(t, root)
			if err := verify(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, candidateGitReadonlyInventory(t, root)) {
				t.Fatal("proper-apps candidate reader changed source Git/index/objects")
			}
		})
	}
	for _, variant := range []string{"project-entry-bytes", "project-entry-mode", "outside-untracked", "shared-tracked"} {
		t.Run(variant, func(t *testing.T) {
			ref := "apps/frontend/new.txt"
			if variant == "outside-untracked" {
				ref = "shared/unknown.txt"
			}
			if variant == "shared-tracked" {
				ref = "pom.xml"
			}
			file := filepath.Join(root, ref)
			raw, err := os.ReadFile(file)
			present := err == nil
			info, _ := os.Stat(file)
			if variant == "project-entry-mode" {
				if err := os.Chmod(file, 0755); err != nil {
					t.Fatal(err)
				}
			} else {
				apTestPut(t, root, ref, "Synthetic unexpected current bytes")
			}
			defer func() {
				if present {
					apTestPut(t, root, ref, raw)
					_ = os.Chmod(file, info.Mode())
				} else {
					_ = os.Remove(file)
				}
			}()
			if err := verify(); err == nil {
				t.Fatal("uncaptured full-repository source change accepted")
			}
		})
	}
	if !bytes.Equal(patch, mustReadSpecBaselineTestFile(t, filepath.Join(project, base, "tracked.diff"))) || !bytes.Equal(stream.Bytes(), mustReadSpecBaselineTestFile(t, filepath.Join(project, base, "candidate.bin"))) {
		t.Fatal("proper-apps prefix handling rewrote original packed tuple or patch")
	}
}
