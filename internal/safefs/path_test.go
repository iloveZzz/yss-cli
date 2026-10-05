package safefs

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRejectTraversalGitAndSymlinkAncestors(t *testing.T) {
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	for _, ref := range []string{"../x", "a/../../x", ".git/index", "a/./x", "a\\x", "C:/x"} {
		if _, e := Path(root, ref); e == nil {
			t.Fatalf("accepted %q", ref)
		}
	}
	if runtime.GOOS != "windows" {
		outside, e := filepath.EvalSymlinks(t.TempDir())
		if e != nil {
			t.Fatal(e)
		}
		if e = os.Symlink(outside, filepath.Join(root, "link")); e != nil {
			t.Fatal(e)
		}
		if _, e = Path(filepath.Join(root, "link", "new"), "asset.yaml"); e == nil {
			t.Fatal("symlink root ancestor accepted")
		}
	}
}
