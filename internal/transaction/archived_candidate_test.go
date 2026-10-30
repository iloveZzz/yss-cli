package transaction

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func TestArchivedCandidateBindsBytesAndMode(t *testing.T) {
	root := t.TempDir()
	data := []byte("trusted archive bytes\n")
	if _, err := Apply(root, "init", []Operation{{Path: "policy.yaml", Data: data, Mode: 0755}}); err != nil {
		t.Fatal(err)
	}
	got, desc, found, err := ArchivedCandidate(root, "policy.yaml")
	if err != nil || !found || string(got) != string(data) || desc.Type != "file" || desc.Digest != safefs.Digest(data) || desc.Mode != 0755 {
		t.Fatalf("归档字节和模式必须绑定: %q %#v %v %v", got, desc, found, err)
	}
	_, _, found, err = ArchivedCandidate(root, "absent.yaml")
	if err != nil || found {
		t.Fatalf("未登记路径必须明确区分错误和缺失: %v %v", found, err)
	}
}

func TestRollbackValidatorReadsSelectedArchiveAfterRecoveredSuccessor(t *testing.T) {
	for _, same := range []bool{false, true} {
		t.Run(map[bool]string{false: "different-path", true: "same-path"}[same], func(t *testing.T) {
			root := t.TempDir()
			a, err := Apply(root, "sync", []Operation{{Path: "a", Data: []byte("A"), Mode: 0644}})
			if err != nil {
				t.Fatal(err)
			}
			ref := "b"
			var before *domain.Descriptor
			if same {
				ref = "a"
				d, e := safefs.Describe(root, ref)
				if e != nil {
					t.Fatal(e)
				}
				before = &d
			}
			b, err := Apply(root, "sync", []Operation{{Path: ref, Data: []byte("B"), Mode: 0644, Before: before}})
			if err != nil {
				t.Fatal(err)
			}
			if same {
				err = os.WriteFile(filepath.Join(root, ref), []byte("A"), 0644)
			} else {
				err = os.Remove(filepath.Join(root, ref))
			}
			if err != nil {
				t.Fatal(err)
			}
			journal := filepath.Join(b.BackupPath, "journal.json")
			raw, err := os.ReadFile(journal)
			if err != nil {
				t.Fatal(err)
			}
			var j map[string]any
			if err = json.Unmarshal(raw, &j); err != nil {
				t.Fatal(err)
			}
			j["phase"] = "recovered"
			raw, _ = json.Marshal(j)
			if err = os.WriteFile(journal, raw, 0600); err != nil {
				t.Fatal(err)
			}
			result, err := RollbackContextWithValidator(context.Background(), root, func(summary Summary, paths []string) error {
				if summary.TransactionID != a.TransactionID {
					t.Fatalf("选择错误事务: %s", summary.TransactionID)
				}
				data, _, found, err := ArchivedCandidateByID(root, summary.TransactionID, "a")
				if err != nil || !found || string(data) != "A" {
					t.Fatalf("未读取锁内选择的 A 归档: %q %v %v", data, found, err)
				}
				return nil
			})
			if err != nil || result.Status != "rolled-back" {
				t.Fatalf("rollback: %v %v", result, err)
			}
			if _, err = os.Stat(filepath.Join(root, "a")); !os.IsNotExist(err) {
				t.Fatal("A 回退没有恢复原缺失状态")
			}
		})
	}
}
