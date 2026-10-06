package transaction_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The child reports only after its real write. Keeping stdin open pauses that
// exact producer call before Chmod/rename; there is no production env hook.
func retainProducerWrite(f *os.File) error {
	if err := json.NewEncoder(os.Stdout).Encode(f.Name()); err != nil {
		return err
	}
	_, err := io.Copy(io.Discard, os.Stdin)
	return err
}

func killObservedProducer(t *testing.T, root, helper, env string, verify func(string)) string {
	t.Helper()
	child := exec.Command(os.Args[0], "-test.run=^"+helper+"$")
	child.Env = append(os.Environ(), env+"="+root)
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	child.Stderr = &stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		_ = input.Close()
		if !waited {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	})
	type observation struct {
		path string
		err  error
	}
	ready := make(chan observation, 1)
	go func() {
		var path string
		err := json.NewDecoder(stdout).Decode(&path)
		ready <- observation{path, err}
	}()
	var ref string
	select {
	case observed := <-ready:
		if observed.err != nil {
			t.Fatalf("producer did not report a real writing window: %v", observed.err)
		}
		ref = observed.path
	case <-time.After(60 * time.Second):
		t.Fatal("producer did not reach the original 60s observation deadline")
	}
	rel, err := filepath.Rel(root, ref)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		t.Fatalf("producer reported a path outside its real root: %q %v", ref, err)
	}
	verify(ref)
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	err = child.Wait()
	waited = true
	if err == nil || child.ProcessState.Success() {
		t.Fatalf("producer completed instead of being killed: %v %s", err, stderr.String())
	}
	verify(ref)
	t.Logf("actual producer kill retained %s: %s", filepath.Base(ref), fmt.Sprint(child.ProcessState))
	return ref
}
