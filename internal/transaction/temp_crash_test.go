package transaction_test

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

func TestProducerTemporaryCrashHelper(t *testing.T) {
	root := os.Getenv("YSS_PRODUCER_TEMP_CRASH_ROOT")
	if root == "" {
		return
	}
	defer transaction.SetTemporaryWriterForTest(func(f *os.File, b []byte) (int, error) {
		n, err := f.Write(b)
		if err == nil && strings.HasSuffix(f.Name(), "-apply.tmp") {
			if n != len(b) {
				return n, io.ErrShortWrite
			}
			err = retainProducerWrite(f)
		}
		return n, err
	})()
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
	ref := killObservedProducer(t, root, "TestProducerTemporaryCrashHelper", "YSS_PRODUCER_TEMP_CRASH_ROOT", func(ref string) {
		if !strings.HasSuffix(ref, "-apply.tmp") || filepath.Dir(ref) != filepath.Join(root, "payload") {
			t.Fatalf("not an actual apply temporary: %s", ref)
		}
		b, err := os.ReadFile(ref)
		info, e := os.Lstat(ref)
		if err != nil || e != nil || !info.Mode().IsRegular() || len(b) != len(data) || safefs.Digest(b) != expected || domain.FileMode(uint32(info.Mode().Perm())) != domain.FileMode(0600) {
			t.Fatalf("real full-byte pre-Chmod producer evidence differs: read=%v stat=%v", err, e)
		}
	})
	return root, ref
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
