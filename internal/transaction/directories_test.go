package transaction

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

func TestDirectoriesAreJournaledAndRollbackPreflightsChildren(t *testing.T) {
	root := t.TempDir()
	root, e := filepath.EvalSymlinks(root)
	if e != nil {
		t.Fatal(e)
	}
	missing := domain.Descriptor{Type: "missing"}
	ops := []Operation{
		{Path: ".work", Directory: true, Mode: 0700, Before: &missing},
		{Path: ".work/report", Directory: true, Mode: 0750, Before: &missing},
		{Path: ".work/report/spec.md", Data: []byte("draft"), Mode: 0640, Before: &missing},
	}
	_, e = Apply(root, "migrate", ops)
	if e != nil {
		t.Fatal(e)
	}
	for ref, want := range map[string]os.FileMode{".work": 0700, ".work/report": 0750, ".work/report/spec.md": 0640} {
		st, e := os.Stat(filepath.Join(root, ref))
		if e != nil || st.Mode().Perm() != want {
			t.Fatalf("permission %s: %v %v", ref, st, e)
		}
	}
	extra := filepath.Join(root, ".work/report/new.md")
	if e := os.WriteFile(extra, []byte("human"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := Rollback(root); e == nil {
		t.Fatal("rollback accepted unplanned child")
	}
	if b, e := os.ReadFile(filepath.Join(root, ".work/report/spec.md")); e != nil || string(b) != "draft" {
		t.Fatal("failed preflight changed files")
	}
	if e := os.Remove(extra); e != nil {
		t.Fatal(e)
	}
	if _, e := Rollback(root); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Lstat(filepath.Join(root, ".work")); !os.IsNotExist(e) {
		t.Fatal("created root not rolled back")
	}
}

func TestDirectoriesRejectImplicitUnjournaledChild(t *testing.T) {
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	missing := domain.Descriptor{Type: "missing"}
	if _, e := Apply(root, "migrate", []Operation{{Path: ".work", Directory: true, Before: &missing}, {Path: ".work/missing/spec.md", Data: []byte("draft"), Before: &missing}}); e == nil {
		t.Fatal("implicit directory beneath journaled parent accepted")
	}
	if _, e := os.Lstat(filepath.Join(root, ".work")); !os.IsNotExist(e) {
		t.Fatal("invalid plan created directory")
	}
}

func TestDirectoryCrashRecovery(t *testing.T) {
	if root := os.Getenv("YSS_TEST_DIRECTORY_CRASH"); root != "" {
		missing := domain.Descriptor{Type: "missing"}
		_, e := ApplyContextWithValidation(context.Background(), root, "migrate", []Operation{
			{Path: ".work", Directory: true, Mode: 0700, Before: &missing},
			{Path: ".work/report", Directory: true, Mode: 0750, Before: &missing},
			{Path: ".work/report/spec.md", Data: []byte("candidate"), Mode: 0640, Before: &missing},
		}, nil, nil, func() error {
			if _, e := os.Stat(filepath.Join(root, ".work/report/spec.md")); e == nil {
				os.Stdout.WriteString("directory-targets-written\n")
				_, e = bufio.NewReader(os.Stdin).ReadString('\n')
				return e
			}
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
		return
	}
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestDirectoryCrashRecovery$")
	child.Env = append(os.Environ(), "YSS_TEST_DIRECTORY_CRASH="+root)
	output, e := child.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	input, e := child.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	defer input.Close()
	if e = child.Start(); e != nil {
		t.Fatal(e)
	}
	defer child.Process.Kill()
	reached := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(output)
		ok := false
		for scanner.Scan() {
			if scanner.Text() == "directory-targets-written" {
				ok = true
				break
			}
		}
		reached <- ok
	}()
	select {
	case ok := <-reached:
		if !ok {
			t.Fatal("child did not reach persisted targets")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("directory apply did not reach crash point")
	}
	child.Process.Kill()
	child.Wait()
	file := filepath.Join(root, ".work/report/spec.md")
	if e = os.WriteFile(file, []byte("human newer bytes"), 0640); e != nil {
		t.Fatal(e)
	}
	if _, e = RecoverKind(root, "migrate"); e == nil {
		t.Fatal("recovery overwrote later human edit")
	}
	if b, e := os.ReadFile(file); e != nil || string(b) != "human newer bytes" {
		t.Fatal("failed recovery changed target")
	}
	if e = os.WriteFile(file, []byte("candidate"), 0640); e != nil {
		t.Fatal(e)
	}
	if _, e = RecoverKind(root, "migrate"); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Lstat(filepath.Join(root, ".work")); !os.IsNotExist(e) {
		t.Fatal("recovery left created directories")
	}
}
