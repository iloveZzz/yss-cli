package transaction_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

const preparationObjectSize = 64 * 1024 * 1024

func TestPreparationObjectCrashHelper(t *testing.T) {
	root := os.Getenv("YSS_PREPARATION_OBJECT_CRASH_ROOT")
	if root == "" {
		return
	}
	defer transaction.SetTemporaryWriterForTest(func(f *os.File, b []byte) (int, error) {
		if len(b) != preparationObjectSize || !strings.Contains(filepath.ToSlash(f.Name()), "/objects/") {
			return f.Write(b)
		}
		// A real prefix write leaves the digest-named object's unpublished
		// .writing-ID evidence; Apply has already persisted its trusted plan.
		n, err := f.Write(b[:4096])
		if err != nil {
			return n, err
		}
		if n != 4096 {
			return n, io.ErrShortWrite
		}
		if err := retainProducerWrite(f); err != nil {
			return n, err
		}
		remaining, err := f.Write(b[n:])
		return n + remaining, err
	})()
	data := bytes.Repeat([]byte("x"), preparationObjectSize)
	guard, err := safefs.Describe(root, "identity.txt")
	if err != nil {
		t.Fatal(err)
	}
	before, err := safefs.Describe(root, "existing")
	if err != nil {
		t.Fatal(err)
	}
	_, err = transaction.ApplyWithGuards(root, "sync", []transaction.Operation{{Path: "existing", Data: data, Mode: 0644, Before: &before}, {Path: "payload", Data: []byte("second target"), Mode: 0644}}, map[string]domain.Descriptor{"identity.txt": guard})
	if err != nil {
		t.Fatal(err)
	}
}

func killedPreparationObject(t *testing.T) (string, string) {
	t.Helper()
	root := testRoot(t)
	for _, f := range []struct {
		ref, data string
		mode      os.FileMode
	}{{"existing", "original business bytes", 0751}, {"identity.txt", "fixed caller identity", 0600}, {".git/index", "original index bytes", 0600}} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, f.ref)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, f.ref), []byte(f.data), f.mode); err != nil {
			t.Fatal(err)
		}
	}
	expectedName := safefs.Digest(bytes.Repeat([]byte("x"), preparationObjectSize)) + ".writing-"
	ref := killObservedProducer(t, root, "TestPreparationObjectCrashHelper", "YSS_PREPARATION_OBJECT_CRASH_ROOT", func(ref string) {
		rel, _ := filepath.Rel(root, ref)
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) != 5 || parts[0] != ".yss" || parts[1] != "transactions" || !strings.HasPrefix(parts[2], ".preparing-") || parts[3] != "objects" || !strings.HasPrefix(parts[4], expectedName) {
			t.Fatalf("not an actual unpublished producer object: %s", ref)
		}
		b, err := os.ReadFile(ref)
		info, e := os.Lstat(ref)
		if err != nil || e != nil || !info.Mode().IsRegular() || info.Size() != 4096 || !bytes.Equal(b, bytes.Repeat([]byte("x"), 4096)) || domain.FileMode(uint32(info.Mode().Perm())) != domain.FileMode(0600) {
			t.Fatalf("actual partial object bytes/mode changed: read=%v stat=%v", err, e)
		}
	})
	return root, ref
}

func objectPreparationTree(t *testing.T, root string) map[string]domain.Descriptor {
	t.Helper()
	out := map[string]domain.Descriptor{}
	if err := filepath.Walk(root, func(p string, info os.FileInfo, e error) error {
		if e != nil {
			return e
		}
		if p == root || info.IsDir() {
			return nil
		}
		ref, _ := filepath.Rel(root, p)
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		out[filepath.ToSlash(ref)] = domain.Descriptor{Type: "file", Digest: safefs.Digest(b), Mode: domain.FileMode(uint32(info.Mode().Perm()))}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func objectPreparationScope(s transaction.Summary, paths []string) error {
	if s.Kind != "sync" || len(paths) != 2 || paths[0] != "existing" || paths[1] != "payload" {
		return fmt.Errorf("unexpected caller scope: %+v %v", s, paths)
	}
	return nil
}

func TestInterruptedPreparationObjectSealsExactEvidence(t *testing.T) {
	root, ref := killedPreparationObject(t)
	before := objectPreparationTree(t, root)
	preview, handled, err := transaction.RecoverPreparations(context.Background(), root, "spec", false, objectPreparationScope)
	if err != nil || !handled || preview.Status != "preparing" {
		t.Fatalf("partial object preview: %+v %v %v", preview, handled, err)
	}
	if !reflect.DeepEqual(before, objectPreparationTree(t, root)) {
		t.Fatal("preview changed evidence")
	}
	sealed, handled, err := transaction.RecoverPreparations(context.Background(), root, "spec", true, objectPreparationScope)
	if err != nil || !handled || sealed.Status != "sealed" {
		t.Fatalf("partial object seal: %+v %v %v", sealed, handled, err)
	}
	oldRef, _ := filepath.Rel(root, ref)
	newRef := strings.Replace(filepath.ToSlash(oldRef), "/.preparing-", "/.sealed-preparation-", 1)
	actual, err := safefs.Describe(root, newRef)
	if err != nil || actual != before[filepath.ToSlash(oldRef)] {
		t.Fatalf("partial control bytes/mode changed: %+v %v", actual, err)
	}
	for _, path := range []string{"existing", "identity.txt", ".git/index"} {
		actual := objectPreparationTree(t, root)[path]
		if actual != before[path] {
			t.Fatalf("protected target %s changed: %+v", path, actual)
		}
	}
	sealedTree := objectPreparationTree(t, root)
	again, _, err := transaction.RecoverPreparations(context.Background(), root, "spec", true, objectPreparationScope)
	if err != nil || again.Status != "unchanged" || !reflect.DeepEqual(sealedTree, objectPreparationTree(t, root)) {
		t.Fatalf("seal not idempotent: %+v %v", again, err)
	}
	current, err := safefs.Describe(root, "existing")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Apply(root, "sync", []transaction.Operation{{Path: "existing", Data: []byte("safe retry"), Mode: 0644, Before: &current}}); err != nil {
		t.Fatalf("sealed preparation blocked retry: %v", err)
	}
}

func TestInterruptedPreparationObjectRejectsUnknownSourceAndTargetDrift(t *testing.T) {
	for _, what := range []string{"unknown-source", "target", "identity-guard"} {
		t.Run(what, func(t *testing.T) {
			root, ref := killedPreparationObject(t)
			switch what {
			case "unknown-source":
				if err := os.WriteFile(filepath.Join(filepath.Dir(ref), strings.Repeat("f", 64)), []byte("unknown evidence"), 0600); err != nil {
					t.Fatal(err)
				}
			case "target":
				if err := os.WriteFile(filepath.Join(root, "existing"), []byte("user changed business bytes"), 0751); err != nil {
					t.Fatal(err)
				}
			case "identity-guard":
				if err := os.WriteFile(filepath.Join(root, "identity.txt"), []byte("user changed identity"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before := objectPreparationTree(t, root)
			_, _, err := transaction.RecoverPreparations(context.Background(), root, "spec", true, objectPreparationScope)
			want := "CONCURRENT"
			if what == "unknown-source" {
				want = "STATE"
			} else if what == "identity-guard" {
				want = "INPUT_DRIFT"
			}
			if errorCode(err) != want {
				t.Fatalf("unsafe evidence accepted: %v want %s", err, want)
			}
			if !reflect.DeepEqual(before, objectPreparationTree(t, root)) {
				t.Fatal("refusal changed evidence or targets")
			}
		})
	}
}
