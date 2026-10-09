package transaction_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

const targetMaterialConfig = ".work/example/progression-target.json"

func checkTargetMaterialRead(t *testing.T, root string, want []string) []transaction.IntentMaterial {
	t.Helper()
	if len(want) != 0 {
		want = append(want, ".yss/transactions/.lock")
	}
	before := noOpTree(t, root)
	materials, err := transaction.LifecycleTargetMaterials(root, targetMaterialConfig)
	if err != nil {
		t.Fatal(err)
	}
	refs := []string{}
	for _, material := range materials {
		refs = append(refs, material.Ref)
		actual, err := safefs.Describe(root, material.Ref)
		if err != nil || material.Descriptor != actual || actual.Type != "file" {
			t.Fatalf("material must bind actual raw bytes and mode: %+v %+v %v", material, actual, err)
		}
	}
	sort.Strings(refs)
	sort.Strings(want)
	if !reflect.DeepEqual(refs, want) {
		t.Fatalf("only exact intent archive files may be admitted: got %v want %v", refs, want)
	}
	assertNoOpTree(t, root, before)
	return materials
}

func TestLifecycleTargetMaterialsCreateReplayRollbackReadonly(t *testing.T) {
	root := testRoot(t)
	if err := os.MkdirAll(filepath.Join(root, ".work/example"), 0755); err != nil {
		t.Fatal(err)
	}
	missing := domain.Descriptor{Type: "missing"}
	data := []byte("{\"target\":\"spec-approved\"}\n")
	applied, err := transaction.ApplyWithGuards(root, "lifecycle-target", []transaction.Operation{{Path: targetMaterialConfig, Data: data, Before: &missing}}, map[string]domain.Descriptor{targetMaterialConfig: missing})
	if err != nil {
		t.Fatal(err)
	}
	base := ".yss/transactions/" + applied.TransactionID
	want := []string{base + "/plan.json", base + "/journal.json", base + "/intent.wal", base + "/objects/" + safefs.Digest(data)}
	checkTargetMaterialRead(t, root, want)
	current, err := safefs.Describe(root, targetMaterialConfig)
	if err != nil {
		t.Fatal(err)
	}
	before := noOpTree(t, root)
	replayed, err := transaction.ApplyWithGuards(root, "lifecycle-target", nil, map[string]domain.Descriptor{targetMaterialConfig: current})
	if err != nil || replayed.Status != "unchanged" {
		t.Fatalf("real no-op replay: %+v %v", replayed, err)
	}
	assertNoOpTree(t, root, before)
	checkTargetMaterialRead(t, root, want)
	if _, err := transaction.RollbackKind(root, "lifecycle-target"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, targetMaterialConfig)); !os.IsNotExist(err) {
		t.Fatalf("rollback did not preserve original missing intent: %v", err)
	}
	checkTargetMaterialRead(t, root, want)
}

func targetMaterialFixture(t *testing.T, kind, ref string) (string, transaction.Result, []byte, []byte) {
	t.Helper()
	root := testRoot(t)
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, ref)), 0755); err != nil {
		t.Fatal(err)
	}
	original, candidate := []byte("original intent\n"), []byte("candidate intent\n")
	if err := os.WriteFile(filepath.Join(root, ref), original, 0640); err != nil {
		t.Fatal(err)
	}
	before, err := safefs.Describe(root, ref)
	if err != nil {
		t.Fatal(err)
	}
	applied, err := transaction.ApplyWithGuards(root, kind, []transaction.Operation{{Path: ref, Data: candidate, Before: &before}}, map[string]domain.Descriptor{ref: before})
	if err != nil {
		t.Fatal(err)
	}
	return root, applied, original, candidate
}

func TestLifecycleTargetMaterialsRecoveredAndHistoricalGuards(t *testing.T) {
	root := testRoot(t)
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, targetMaterialConfig)), 0755); err != nil {
		t.Fatal(err)
	}
	original, candidate := []byte("original intent\n"), []byte("candidate intent\n")
	if err := os.WriteFile(filepath.Join(root, targetMaterialConfig), original, 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "checkpoint.json"), []byte("original checkpoint"), 0644); err != nil {
		t.Fatal(err)
	}
	before, _ := safefs.Describe(root, targetMaterialConfig)
	fact, _ := safefs.Describe(root, "checkpoint.json")
	recovered, err := transaction.ApplyContextWithValidation(context.Background(), root, "lifecycle-target", []transaction.Operation{{Path: targetMaterialConfig, Data: candidate, Before: &before}}, map[string]domain.Descriptor{targetMaterialConfig: before, "checkpoint.json": fact, "missing-fact": {Type: "missing"}, ".work": {Type: "directory", Mode: 0755}}, nil, func() error {
		before := noOpTree(t, root)
		if _, err := transaction.LifecycleTargetMaterials(root, targetMaterialConfig); errorCode(err) != "INTERRUPTED" {
			t.Fatalf("actual applying transaction was admitted: %v", err)
		}
		assertNoOpTree(t, root, before)
		return errors.New("synthetic post-write validation failure")
	})
	if err == nil || recovered.Status != "recovered" {
		t.Fatalf("real validation rollback: %+v %v", recovered, err)
	}
	if err := os.WriteFile(filepath.Join(root, "checkpoint.json"), []byte("legitimate later checkpoint"), 0644); err != nil {
		t.Fatal(err)
	}
	base := ".yss/transactions/" + recovered.TransactionID
	checkTargetMaterialRead(t, root, []string{base + "/plan.json", base + "/journal.json", base + "/intent.wal", base + "/objects/" + safefs.Digest(original), base + "/objects/" + safefs.Digest(candidate)})
}

func TestLifecycleTargetMaterialsNeverAdmitsForeignArchives(t *testing.T) {
	for _, item := range []struct{ kind, ref string }{{"sync", targetMaterialConfig}, {"lifecycle-target", ".work/other/progression-target.json"}} {
		t.Run(item.kind+"-"+filepath.Dir(item.ref), func(t *testing.T) {
			root, _, _, _ := targetMaterialFixture(t, item.kind, item.ref)
			checkTargetMaterialRead(t, root, []string{})
		})
	}
	root := testRoot(t)
	checkTargetMaterialRead(t, root, []string{})
}

func TestLifecycleTargetMaterialsRejectsArchiveTamperingReadonly(t *testing.T) {
	for _, variant := range []string{"missing-plan", "missing-journal", "missing-wal", "missing-before", "missing-after", "wrong-plan-digest", "wrong-root", "wrong-identity", "unknown-plan-member", "extra-source", "extra-seal", "extra-object", "wrong-object-digest", "wrong-plan-mode", "wrong-object-mode", "symlink-object", "symlink-archive", "partial-wal", "unknown-wal-member", "duplicate-wal", "pending", "wrong-guard", "directory-before"} {
		t.Run(variant, func(t *testing.T) {
			if runtime.GOOS == "windows" && (variant == "symlink-object" || variant == "symlink-archive" || variant == "wrong-plan-mode" || variant == "wrong-object-mode") {
				t.Skip("POSIX modes/symlink fixture requires its platform")
			}
			root, applied, original, candidate := targetMaterialFixture(t, "lifecycle-target", targetMaterialConfig)
			base := applied.BackupPath
			put := func(ref string, raw []byte) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(base, ref), raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			remove := func(ref string) {
				t.Helper()
				if err := os.Remove(filepath.Join(base, ref)); err != nil {
					t.Fatal(err)
				}
			}
			switch variant {
			case "missing-plan":
				remove("plan.json")
			case "missing-journal":
				remove("journal.json")
			case "missing-wal":
				remove("intent.wal")
			case "missing-before":
				remove("objects/" + safefs.Digest(original))
			case "missing-after":
				remove("objects/" + safefs.Digest(candidate))
			case "wrong-plan-digest":
				raw, _ := os.ReadFile(filepath.Join(base, "plan.json"))
				put("plan.json", append(raw, ' '))
			case "wrong-root", "wrong-identity", "unknown-plan-member", "wrong-guard", "directory-before":
				rewriteArchivedPlan(t, applied, func(plan map[string]any) {
					switch variant {
					case "wrong-root":
						plan["root"] = filepath.Join(root, "another-root")
					case "wrong-identity":
						plan["id"] = "00000000000000000000000000000000"
					case "unknown-plan-member":
						plan["unregistered"] = true
					case "wrong-guard":
						plan["guards"].(map[string]any)[targetMaterialConfig] = domain.Descriptor{Type: "missing"}
					case "directory-before":
						plan["operations"].([]any)[0].(map[string]any)["before"] = domain.Descriptor{Type: "directory", Mode: 0755}
					}
				})
			case "extra-source":
				put("source.mjs", []byte("implementation source"))
			case "extra-seal":
				put("seal.json", []byte("{}"))
			case "extra-object":
				put("objects/"+safefs.Digest([]byte("implementation source")), []byte("implementation source"))
			case "wrong-object-digest":
				put("objects/"+safefs.Digest(candidate), []byte("changed candidate"))
			case "wrong-plan-mode":
				if err := os.Chmod(filepath.Join(base, "plan.json"), 0644); err != nil {
					t.Fatal(err)
				}
			case "wrong-object-mode":
				if err := os.Chmod(filepath.Join(base, "objects/"+safefs.Digest(candidate)), 0644); err != nil {
					t.Fatal(err)
				}
			case "symlink-object":
				remove("objects/" + safefs.Digest(candidate))
				if err := os.Symlink(filepath.Join(root, targetMaterialConfig), filepath.Join(base, "objects/"+safefs.Digest(candidate))); err != nil {
					t.Fatal(err)
				}
			case "symlink-archive":
				moved := filepath.Join(root, "moved-archive")
				if err := os.Rename(base, moved); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(moved, base); err != nil {
					t.Fatal(err)
				}
			case "partial-wal":
				raw, _ := os.ReadFile(filepath.Join(base, "intent.wal"))
				put("intent.wal", append(raw, []byte("partial record")...))
			case "unknown-wal-member":
				put("intent.wal", []byte("{\"schemaVersion\":1,\"index\":0,\"unregistered\":true}\n"))
			case "duplicate-wal":
				raw, _ := os.ReadFile(filepath.Join(base, "intent.wal"))
				put("intent.wal", append(raw, raw...))
			case "pending":
				raw, _ := os.ReadFile(filepath.Join(base, "journal.json"))
				var journal map[string]any
				if err := json.Unmarshal(raw, &journal); err != nil {
					t.Fatal(err)
				}
				journal["phase"] = "applying"
				raw, _ = json.Marshal(journal)
				put("journal.json", raw)
			}
			before := noOpTree(t, root)
			if material, err := transaction.LifecycleTargetMaterials(root, targetMaterialConfig); err == nil || len(material) != 0 {
				t.Fatalf("tampered archive admitted (%s): %+v %v", variant, material, err)
			}
			assertNoOpTree(t, root, before)
		})
	}
}

func TestLifecycleTargetMaterialsRejectsExtraOperationsAndArtifacts(t *testing.T) {
	for _, variant := range []string{"extra-operation", "artifact", "delete-config"} {
		t.Run(variant, func(t *testing.T) {
			root := testRoot(t)
			ops := []transaction.Operation{{Path: targetMaterialConfig, Data: []byte("intent")}}
			var artifacts []transaction.Artifact
			switch variant {
			case "extra-operation":
				ops = append(ops, transaction.Operation{Path: "source.mjs", Data: []byte("implementation")})
			case "artifact":
				raw := []byte("business evidence")
				artifacts = []transaction.Artifact{{ArtifactRecord: transaction.ArtifactRecord{Path: "source.mjs", Kind: "baseline", Descriptor: domain.Descriptor{Type: "file", Digest: safefs.Digest(raw), Mode: 0644}}, Data: raw}}
			case "delete-config":
				if err := os.MkdirAll(filepath.Dir(filepath.Join(root, targetMaterialConfig)), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, targetMaterialConfig), []byte("intent"), 0644); err != nil {
					t.Fatal(err)
				}
				before, _ := safefs.Describe(root, targetMaterialConfig)
				ops = []transaction.Operation{{Path: targetMaterialConfig, Delete: true, Before: &before}}
			}
			if _, err := transaction.ApplyContextWithValidation(context.Background(), root, "lifecycle-target", ops, nil, artifacts, nil); err != nil {
				t.Fatal(err)
			}
			before := noOpTree(t, root)
			if material, err := transaction.LifecycleTargetMaterials(root, targetMaterialConfig); err == nil || len(material) != 0 {
				t.Fatalf("extra transaction authority admitted (%s): %+v %v", variant, material, err)
			}
			assertNoOpTree(t, root, before)
		})
	}
}

func TestLifecycleTargetMaterialsIncludesOnlyVerifiedEmptyMutex(t *testing.T) {
	for _, variant := range []string{"empty", "nonempty", "symlink", "executable-mode", "no-matching-target"} {
		t.Run(variant, func(t *testing.T) {
			if runtime.GOOS == "windows" && (variant == "symlink" || variant == "executable-mode") {
				t.Skip("POSIX mode/symlink fixture requires its platform")
			}
			kind := "lifecycle-target"
			if variant == "no-matching-target" {
				kind = "sync"
			}
			root, _, _, _ := targetMaterialFixture(t, kind, targetMaterialConfig)
			mutexRef := ".yss/transactions/.lock"
			mutexPath := filepath.Join(root, filepath.FromSlash(mutexRef))
			switch variant {
			case "nonempty":
				if err := os.WriteFile(mutexPath, []byte("not mutex bytes"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Remove(mutexPath); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(root, targetMaterialConfig), mutexPath); err != nil {
					t.Fatal(err)
				}
			case "executable-mode":
				if err := os.Chmod(mutexPath, 0700); err != nil {
					t.Fatal(err)
				}
			}
			before := noOpTree(t, root)
			materials, err := transaction.LifecycleTargetMaterials(root, targetMaterialConfig)
			assertNoOpTree(t, root, before)
			found := false
			for _, material := range materials {
				if material.Ref == mutexRef {
					found = true
					if material.Descriptor != (domain.Descriptor{Type: "file", Digest: safefs.Digest(nil), Mode: domain.FileMode(0600)}) {
						t.Fatalf("mutex bytes or canonical mode not bound: %+v", material)
					}
				}
			}
			switch variant {
			case "empty":
				if err != nil || !found {
					t.Fatalf("real target's permanent empty mutex missing: %+v %v", materials, err)
				}
			case "no-matching-target":
				if err != nil || found || len(materials) != 0 {
					t.Fatalf("foreign transaction mutex was admitted: %+v %v", materials, err)
				}
			default:
				if err == nil || len(materials) != 0 {
					t.Fatalf("invalid mutex admitted: %+v %v", materials, err)
				}
			}
		})
	}
}
