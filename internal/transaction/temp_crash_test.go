package transaction_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

func TestProducerTemporaryCrashHelper(t *testing.T) {
	root := os.Getenv("YSS_PRODUCER_TEMP_CRASH_ROOT")
	if root == "" {
		return
	}
	data := bytes.Repeat([]byte("actual native producer data\n"), 160000)
	ops := []transaction.Operation{}
	for i := 0; i < 150; i++ {
		ops = append(ops, transaction.Operation{Path: fmt.Sprintf("payload/%03d", i), Data: data, Mode: 0644})
	}
	if _, err := transaction.Apply(root, "sync", ops); err != nil {
		t.Fatal(err)
	}
}
func producerPreChmodCrash(t *testing.T) (string, string) {
	t.Helper()
	data := bytes.Repeat([]byte("actual native producer data\n"), 160000)
	expected := safefs.Digest(data)
	for attempt := 0; attempt < 40; attempt++ {
		root := testRoot(t)
		if err := os.WriteFile(filepath.Join(root, "business.bin"), []byte{0, 255, 13, 10}, 0751); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(root, ".git"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ".git/index"), []byte("untouched index"), 0600); err != nil {
			t.Fatal(err)
		}
		child := exec.Command(os.Args[0], "-test.run=^TestProducerTemporaryCrashHelper$")
		child.Env = append(os.Environ(), "YSS_PRODUCER_TEMP_CRASH_ROOT="+root)
		var output bytes.Buffer
		child.Stdout, child.Stderr = &output, &output
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		seen := ""
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			refs, _ := filepath.Glob(filepath.Join(root, "payload/*.yss-txn-*-apply.tmp"))
			for _, ref := range refs {
				info, err := os.Stat(ref)
				if err == nil && domain.FileMode(uint32(info.Mode().Perm())) == domain.FileMode(0600) {
					seen = ref
					break
				}
			}
			if seen != "" {
				break
			}
			time.Sleep(10 * time.Microsecond)
		}
		_ = child.Process.Kill()
		err := child.Wait()
		if err == nil {
			t.Fatalf("producer finished before SIGKILL: %s", output.String())
		}
		if seen == "" {
			t.Fatalf("no real producer temporary observed: %s", output.String())
		}
		refs, _ := filepath.Glob(filepath.Join(root, "payload/*.yss-txn-*-apply.tmp"))
		for _, ref := range refs {
			b, err := os.ReadFile(ref)
			info, e := os.Stat(ref)
			if err == nil && e == nil && safefs.Digest(b) == expected && domain.FileMode(uint32(info.Mode().Perm())) == domain.FileMode(0600) {
				return root, ref
			}
		}
	}
	t.Fatal("SIGKILL did not retain the actual full-byte pre-Chmod producer window")
	return "", ""
}
func TestKilledProducerPreChmodTemporaryRecovers(t *testing.T) {
	root, _ := producerPreChmodCrash(t)
	out, err := transaction.Recover(root)
	if err != nil || out.Status != "recovered" {
		t.Fatalf("real complete producer bytes at initial0600 could not recover: %+v %v", out, err)
	}
	files, _ := filepath.Glob(filepath.Join(root, "payload/*"))
	if len(files) != 0 {
		t.Fatalf("created files survived recovery: %v", files)
	}
	b, err := os.ReadFile(filepath.Join(root, "business.bin"))
	info, e := os.Stat(filepath.Join(root, "business.bin"))
	if err != nil || e != nil || !bytes.Equal(b, []byte{0, 255, 13, 10}) || domain.FileMode(uint32(info.Mode().Perm())) != domain.FileMode(0751) {
		t.Fatal("business bytes/mode changed")
	}
	b, err = os.ReadFile(filepath.Join(root, ".git/index"))
	if err != nil || string(b) != "untouched index" {
		t.Fatal("index changed")
	}
}
func TestKilledProducerPreChmodTemporaryRejectsLaterUserBytes(t *testing.T) {
	root, ref := producerPreChmodCrash(t)
	if err := os.WriteFile(ref, []byte("user changed producer temporary"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Recover(root); errorCode(err) != "RECOVERY_FAILED" {
		t.Fatalf("unknown temporary bytes were accepted: %v", err)
	}
	b, err := os.ReadFile(ref)
	if err != nil || string(b) != "user changed producer temporary" {
		t.Fatal("unknown temporary overwritten")
	}
}

func TestKilledProducerPreChmodTemporaryRejectsLaterUserMode(t *testing.T) {
	root, ref := producerPreChmodCrash(t)
	if err := os.Chmod(ref, 0444); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Recover(root); errorCode(err) != "RECOVERY_FAILED" {
		t.Fatalf("unknown temporary mode was accepted: %v", err)
	}
	info, err := os.Stat(ref)
	if err != nil || domain.FileMode(uint32(info.Mode().Perm())) != domain.FileMode(0444) {
		t.Fatal("unknown temporary mode overwritten")
	}
}
