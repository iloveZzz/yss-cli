//go:build !yss_nobundle

package project

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

func TestInitPlanDirectoryRollback(t *testing.T) { runInitPlanDirectoryRecovery(t, "rollback") }
func TestInitPlanDirectoryRecover(t *testing.T)  { runInitPlanDirectoryRecovery(t, "recover") }

func runInitPlanDirectoryRecovery(t *testing.T, mode string) {
	t.Helper()
	b, err := bundle.Load("backend")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	plan, err := buildCore(root, "backend", "init", nil, nil, nil, PlanningOptions{target: b})
	if err != nil {
		t.Fatal(err)
	}
	all, err := nativePlanOperations(plan)
	if err != nil {
		t.Fatal(err)
	}
	const directory = ".template-spec/process/schemas"
	const schema = ".template-spec/process/schemas/stage-tracking.schema.json"
	var ops []transaction.Operation
	for _, op := range all {
		if op.Path == directory || op.Path == schema || op.Path == MetadataFile {
			ops = append(ops, op)
		}
	}
	if len(ops) != 3 {
		t.Fatalf("真实 init 计划缺少测试输入: %d", len(ops))
	}
	result, err := transaction.Apply(root, "init", ops)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, MetadataFile))
	if err != nil {
		t.Fatal(err)
	}
	var meta Metadata
	if err = json.Unmarshal(raw, &meta); err != nil {
		t.Fatal(err)
	}
	id := &Identity{Root: root, Profile: domain.Profiles["backend"], Native: &meta}
	user := filepath.Join(root, "keep.txt")
	if err = os.WriteFile(user, []byte("user"), 0644); err != nil {
		t.Fatal(err)
	}
	validate := func(summary transaction.Summary, paths []string) error {
		return VerifyRecoveryCandidatePolicies(root, id, paths, summary.TransactionID)
	}
	if mode == "recover" {
		journal := filepath.Join(result.BackupPath, "journal.json")
		raw, err = os.ReadFile(journal)
		if err != nil {
			t.Fatal(err)
		}
		var j map[string]any
		if err = json.Unmarshal(raw, &j); err != nil {
			t.Fatal(err)
		}
		j["phase"] = "applying"
		raw, _ = json.Marshal(j)
		if err = os.WriteFile(journal, raw, 0600); err != nil {
			t.Fatal(err)
		}
		_, err = transaction.RecoverContextWithValidator(context.Background(), root, validate)
	} else {
		_, err = transaction.RollbackContextWithValidator(context.Background(), root, validate)
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, MetadataFile)); !os.IsNotExist(err) {
		t.Fatal("初始化 metadata 未恢复")
	}
	if data, err := os.ReadFile(user); err != nil || string(data) != "user" {
		t.Fatal("用户文件改变")
	}
}
