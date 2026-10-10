package governance

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

type nativeSeedAssets struct {
	files  fstest.MapFS
	digest string
}

var specialistNativeAssets = struct {
	sync.Mutex
	entries map[string]nativeSeedAssets
}{entries: map[string]nativeSeedAssets{}}

func specialistNativeSeedsMayShare(name string) bool {
	// Exported fixture roots may be shared by callers outside these two tests.
	if os.Getenv("YSS_NATIVE_FIXTURE_DIR") != "" || os.Getenv("YSS_NATIVE_FIXTURE_ROOT") != "" {
		return false
	}
	switch strings.Split(name, "/")[0] {
	case "TestLocalFrontendSpecialistPublicAnalysisAndApprovedWorker", "TestLocalFrontendSpecialistExternalBackendAndLocalBackendTerminal":
		return true
	}
	return false
}

func runActualNativeSeed(t *testing.T, binary, root string, args []string) string {
	t.Helper()
	if specialistNativeSeedsMayShare(t.Name()) {
		return cachedActualNativeSeed(t, binary, args)
	}
	if raw, err := exec.Command(binary, args...).CombinedOutput(); err != nil {
		t.Fatalf("actual native init: %v %s", err, raw)
	}
	return root
}

func nativeSeedAssetKey(binary string, args, env []string) (string, error) {
	raw, err := os.ReadFile(binary)
	if err != nil {
		return "", err
	}
	nonRoot := make([]string, 0, len(args))
	roots := 0
	for i := 0; i < len(args); i++ {
		if args[i] == "--root" && i+1 < len(args) {
			i++
			roots++
		} else {
			nonRoot = append(nonRoot, args[i])
		}
	}
	if roots != 1 {
		return "", fmt.Errorf("native seed needs exactly one --root")
	}
	env = append([]string(nil), env...)
	sort.Strings(env)
	// The actual binary digest also binds all embedded Profile/source snapshots.
	input, err := json.Marshal([]any{sha256.Sum256(raw), nonRoot, env})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(input)
	return hex.EncodeToString(digest[:]), nil
}

func nativeSeedAssetsDigest(files fstest.MapFS) string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		file := files[name]
		fmt.Fprintf(h, "%s\x00%d\x00%x\x00", name, file.Mode, sha256.Sum256(file.Data))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func snapshotNativeSeedAssets(root string) (nativeSeedAssets, error) {
	files := fstest.MapFS{}
	err := filepath.WalkDir(root, func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		ref, err := filepath.Rel(root, file)
		if err != nil {
			return err
		}
		ref = filepath.ToSlash(ref)
		// Root-bound plans, journals, locks and recovery objects are never assets.
		if ref == ".yss/transactions" {
			if !entry.IsDir() {
				return fmt.Errorf("native transactions is not a directory: %s", ref)
			}
			return filepath.SkipDir
		}
		if strings.HasPrefix(ref, ".yss/") {
			return fmt.Errorf("unexpected native runtime state: %s", ref)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("native asset is not a plain file/directory: %s", ref)
		}
		asset := &fstest.MapFile{Mode: info.Mode()}
		if !info.IsDir() {
			asset.Data, err = os.ReadFile(file)
			if err != nil {
				return err
			}
			if bytes.Contains(asset.Data, []byte(root)) {
				return fmt.Errorf("native asset binds initialization root: %s", ref)
			}
		}
		files[ref] = asset
		return nil
	})
	return nativeSeedAssets{files: files, digest: nativeSeedAssetsDigest(files)}, err
}

func copyNativeSeedAssets(root string, assets nativeSeedAssets) error {
	if nativeSeedAssetsDigest(assets.files) != assets.digest {
		return fmt.Errorf("native asset cache changed")
	}
	if err := os.CopyFS(root, assets.files); err != nil {
		return err
	}
	// CopyFS creates plain files; restore exact asset permissions afterwards.
	for ref, asset := range assets.files {
		if !asset.Mode.IsDir() {
			if err := os.Chmod(filepath.Join(root, filepath.FromSlash(ref)), asset.Mode.Perm()); err != nil {
				return err
			}
		}
	}
	for ref, asset := range assets.files {
		if asset.Mode.IsDir() {
			if err := os.Chmod(filepath.Join(root, filepath.FromSlash(ref)), asset.Mode.Perm()); err != nil {
				return err
			}
		}
	}
	return nil
}

func cachedActualNativeSeed(t *testing.T, binary string, args []string) string {
	t.Helper()
	key, err := nativeSeedAssetKey(binary, args, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	specialistNativeAssets.Lock()
	defer specialistNativeAssets.Unlock()
	assets, hit := specialistNativeAssets.entries[key]
	if !hit {
		start := time.Now()
		if raw, err := exec.Command(binary, args...).CombinedOutput(); err != nil {
			t.Fatalf("actual native cache initialization: %v %s", err, raw)
		}
		root := ""
		for i := range args {
			if args[i] == "--root" && i+1 < len(args) {
				root = args[i+1]
			}
		}
		assets, err = snapshotNativeSeedAssets(root)
		if err != nil {
			t.Fatal(err)
		}
		current, err := nativeSeedAssetKey(binary, args, os.Environ())
		if err != nil || current != key {
			t.Fatalf("native binary/inputs changed during initialization: %v", err)
		}
		specialistNativeAssets.entries[key] = assets
		t.Logf("native seed assets cold: %.3fs key=%s assets=%d", time.Since(start).Seconds(), key, len(assets.files))
	}
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "native-assets")
	start := time.Now()
	if err := copyNativeSeedAssets(root, assets); err != nil {
		t.Fatal(err)
	}
	t.Logf("native seed private copy: %.3fs hit=%t key=%s", time.Since(start).Seconds(), hit, key)
	return root
}

func TestNativeSeedAssetKeyAndRefusal(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "binary")
	if err := os.WriteFile(binary, []byte("fixed source one"), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"init", "--profile", "spec", "--root", "/first", "--project-name", "synthetic"}
	key, err := nativeSeedAssetKey(binary, args, []string{"A=1", "B=2"})
	if err != nil {
		t.Fatal(err)
	}
	otherRoot := append([]string(nil), args...)
	otherRoot[4] = "/second"
	same, err := nativeSeedAssetKey(binary, otherRoot, []string{"B=2", "A=1"})
	if err != nil || same != key {
		t.Fatal("private roots/environment order changed immutable asset identity")
	}
	for _, changed := range [][]string{append(append([]string(nil), args...), "--full"), {"init", "--profile", "backend", "--root", "/first", "--project-name", "synthetic"}, {"init", "--profile", "spec", "--root", "/first", "--project-name", "other"}} {
		got, err := nativeSeedAssetKey(binary, changed, []string{"A=1", "B=2"})
		if err != nil || got == key {
			t.Fatal("Profile/full/render parameters did not invalidate assets")
		}
	}
	changedEnv, err := nativeSeedAssetKey(binary, args, []string{"A=changed", "B=2"})
	if err != nil || changedEnv == key {
		t.Fatal("environment did not invalidate assets")
	}
	if err := os.WriteFile(binary, []byte("fixed source two"), 0600); err != nil {
		t.Fatal(err)
	}
	changedBinary, err := nativeSeedAssetKey(binary, args, []string{"A=1", "B=2"})
	if err != nil || changedBinary == key {
		t.Fatal("binary/embedded source identity did not invalidate assets")
	}
	for _, name := range []string{"TestSpecBaselineNativeSeedInitialPolicyClosure", "TestBackendProfileTerminalNativeBundleFixedSource", "TestFourProfilesInitRepeatSyncAndWholeRollback"} {
		if specialistNativeSeedsMayShare(name) {
			t.Fatal("initialization/recovery boundary was cached")
		}
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "asset"), []byte("original"), 0750); err != nil {
		t.Fatal(err)
	}
	assets, err := snapshotNativeSeedAssets(root)
	if err != nil {
		t.Fatal(err)
	}
	assets.files["asset"].Data[0] = 'X'
	if copyNativeSeedAssets(filepath.Join(t.TempDir(), "copy"), assets) == nil {
		t.Fatal("changed cache was copied")
	}
	if err := os.Symlink(filepath.Join(root, "asset"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshotNativeSeedAssets(root); err == nil {
		t.Fatal("shared symlink entered native asset cache")
	}
	for _, kind := range []string{"file", "symlink", "directory-with-unknown-sibling"} {
		t.Run("transactions-"+kind, func(t *testing.T) {
			root := t.TempDir()
			state := filepath.Join(root, ".yss")
			if err := os.Mkdir(state, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(state, "z-state"), []byte("unknown"), 0600); err != nil {
				t.Fatal(err)
			}
			transactions := filepath.Join(state, "transactions")
			var err error
			switch kind {
			case "file":
				err = os.WriteFile(transactions, []byte("invalid"), 0600)
			case "symlink":
				err = os.Symlink(t.TempDir(), transactions)
			default:
				err = os.Mkdir(transactions, 0755)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := snapshotNativeSeedAssets(root); err == nil {
				t.Fatal("invalid transactions or unknown sibling state entered assets")
			}
		})
	}
}

func TestActualNativeSeedAssetsPrivateCopies(t *testing.T) {
	binary := os.Getenv("YSS_NATIVE_BINARY")
	if binary == "" {
		t.Skip("actual fixed native CLI binary not configured")
	}
	makeSeed := func(t *testing.T) string {
		parent, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return cachedActualNativeSeed(t, binary, []string{"init", "--profile", "spec", "--root", filepath.Join(parent, "spec"), "--project-name", "synthetic-progression-spec", "--business-domain", "test-only", "--team-size", "2", "--json"})
	}
	first := ""
	var before nativeSeedAssets
	t.Run("first-caller-cleanup", func(t *testing.T) {
		first = makeSeed(t)
		var err error
		before, err = snapshotNativeSeedAssets(first)
		if err != nil {
			t.Fatal(err)
		}
	})
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatal("first caller private root survived cleanup")
	}
	a, b := makeSeed(t), makeSeed(t)
	for _, root := range []string{a, b} {
		assets, err := snapshotNativeSeedAssets(root)
		if err != nil || assets.digest != before.digest {
			t.Fatalf("private copy bytes/modes changed: %v", err)
		}
		if _, err := os.Stat(filepath.Join(root, ".yss/transactions")); !os.IsNotExist(err) {
			t.Fatal("root-bound native initialization journal was transplanted")
		}
	}
	infoA, err := os.Stat(filepath.Join(a, ".yss.json"))
	if err != nil {
		t.Fatal(err)
	}
	infoB, err := os.Stat(filepath.Join(b, ".yss.json"))
	if err != nil || os.SameFile(infoA, infoB) {
		t.Fatal("private copies share an inode")
	}
	plan := filepath.Join(t.TempDir(), "compiler-plan.json")
	for _, args := range [][]string{{"skills", "ensure", "yss-implementation-contract-compiler", "--root", a, "--plan", "--out", plan, "--json"}, {"skills", "--root", a, "--apply", "--plan-file", plan, "--json"}, {"doctor", "--root", b, "--json"}} {
		if raw, err := exec.Command(binary, args...).CombinedOutput(); err != nil {
			t.Fatalf("actual private-root public operation: %v %s", err, raw)
		}
	}
	fresh, err := snapshotNativeSeedAssets(b)
	if err != nil || fresh.digest != before.digest {
		t.Fatal("another caller's skills apply changed assets/private copy")
	}
	plans, err := filepath.Glob(filepath.Join(a, ".yss/transactions/*/plan.json"))
	if err != nil || len(plans) != 1 {
		t.Fatalf("private skills apply did not create its own transaction: %v %v", plans, err)
	}
	var applied struct{ Root, Kind string }
	if err := json.Unmarshal(mustReadSpecBaselineTestFile(t, plans[0]), &applied); err != nil || applied.Root != a || applied.Kind != "skills" {
		t.Fatalf("private transaction is not bound to its actual new root: %+v %v", applied, err)
	}
}
