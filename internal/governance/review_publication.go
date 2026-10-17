package governance

import (
	"bytes"
	"context"
	"encoding/json"
	"path"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

type pendingReviewPublication struct {
	SchemaVersion    int    `json:"schema_version"`
	Kind             string `json:"kind"`
	CheckpointRef    string `json:"checkpoint_ref"`
	CheckpointBefore struct {
		Digest string `json:"digest"`
		Mode   uint32 `json:"mode"`
	} `json:"checkpoint_before"`
	CheckpointAfter string            `json:"checkpoint_after,omitempty"`
	OutputDir       string            `json:"output_dir"`
	Files           map[string]string `json:"files"`
}

var pendingReviewFiles = []string{"task.json", "task-issued.json", "bundle-draft.yaml", "coverage.md"}

func reviewPublicationRef(ref string) bool {
	if !kernel.ContractPath(ref) { return false }
	first := strings.ToLower(strings.SplitN(ref, "/", 2)[0])
	return !kernel.SemHas([]string{".git", ".yss", ".agents", ".template-spec", "scripts", "yss-project.yaml", ".yss.json"}, first)
}

// Only pending review drafts and the matching Plan reservation can be published here.
func publishPendingReview(ctx context.Context, root string, args map[string]string) (any, error) {
	s := kernel.NewSemanticSession(ctx, root, args)
	if err := s.Authorities(); err != nil {
		return nil, err
	}
	if err := s.RequireGovernanceWrite(); err != nil {
		return nil, err
	}
	if s.GovernanceVersion != 2 {
		return nil, domain.Fail("GOVERNANCE_MIGRATION_REQUIRED", "新审阅包仅用于显式新治理实例")
	}
	if _, err := s.ContextContract(); err != nil {
		return nil, err
	}
	raw, err := s.Bytes(args["file"])
	if err != nil {
		return nil, err
	}
	if _, err = schema.Parse(raw); err != nil {
		return nil, err
	}
	var p pendingReviewPublication
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&p); err != nil {
		return nil, err
	}
	if p.SchemaVersion != 1 || p.Kind != "pending-review-publication" || !kernel.ContractPath(p.OutputDir) || !kernel.ContractPath(p.CheckpointRef) || len(p.Files) != len(pendingReviewFiles) {
		return nil, domain.Fail("REVIEW_PUBLICATION", "未知审阅包或目标范围")
	}
	for _, ref := range []string{p.OutputDir, p.CheckpointRef} {
		if !reviewPublicationRef(ref) {
			return nil, domain.Fail("REVIEW_PUBLICATION", "审阅包不得覆盖身份、规则或事务状态")
		}
	}
	if present, e := s.Exists(p.OutputDir); e != nil {
		return nil, e
	} else if present {
		return nil, domain.Fail("CONFLICT", "审阅包输出目录必须不存在")
	}
	checkpoint, err := s.Bytes(p.CheckpointRef)
	if err != nil {
		return nil, err
	}
	descriptor, err := s.V.Watch(p.CheckpointRef)
	if err != nil {
		return nil, err
	}
	if p.CheckpointBefore.Digest != "sha256:"+safefs.Digest(checkpoint) || p.CheckpointBefore.Mode != descriptor.Mode {
		return nil, domain.Fail("REVIEW_BINDING_STALE", "当前消费者与准备时字节不符")
	}
	for _, name := range pendingReviewFiles {
		if p.Files[name] == "" {
			return nil, domain.Fail("REVIEW_PUBLICATION", "草案文件集合不完整")
		}
	}
	if p.Files["task.json"] != p.Files["task-issued.json"] {
		return nil, domain.Fail("REVIEW_PUBLICATION", "派发快照必须保留同一原始任务字节")
	}
	value, err := schema.Parse([]byte(p.Files["task.json"]))
	if err != nil {
		return nil, err
	}
	task := kernel.ApMap(value)
	taskRef := path.Join(p.OutputDir, "task.json")
	if kernel.ApNumber(task["schema_version"]) != 3 || task["workflow_status"] != "not-started" || task["result"] != nil || task["checkpoint_ref"] != p.CheckpointRef || kernel.ApMap(task["provenance"])["task_ref"] != taskRef || !kernel.SemHas([]string{"Reviewer", "Verifier"}, kernel.Text(task["execution_state"])) {
		return nil, domain.Fail("REVIEW_PUBLICATION", "只能发布真实来源的未执行 task3 审阅草案")
	}
	bundleValue, err := schema.Parse([]byte(p.Files["bundle-draft.yaml"]))
	if err != nil {
		return nil, err
	}
	bundle := kernel.ApMap(bundleValue)
	if kernel.ApNumber(bundle["schema_version"]) != 3 || bundle["kind"] != "review-bundle" || bundle["review_task_ref"] != taskRef || bundle["review_task_digest"] != safefs.Digest([]byte(p.Files["task.json"])) {
		return nil, domain.Fail("REVIEW_PUBLICATION", "pending bundle3 未绑定本次任务原字节")
	}
	if err = s.ValidateSchema(".template-spec/process/schemas/review-bundle.schema.json", bundle); err != nil {
		return nil, err
	}
	for _, row := range kernel.ApRows(bundle["reviews"]) {
		if row["decision"] != "pending" {
			return nil, domain.Fail("REVIEW_PUBLICATION", "发布入口不创建任何批准")
		}
	}
	changes := []assetChange{}
	for _, name := range pendingReviewFiles {
		changes = append(changes, assetChange{Ref: path.Join(p.OutputDir, name), After: []byte(p.Files[name])})
	}
	if p.CheckpointAfter != "" {
		beforeValue, e := schema.Parse(checkpoint)
		if e != nil {
			return nil, e
		}
		afterValue, e := schema.Parse([]byte(p.CheckpointAfter))
		if e != nil {
			return nil, e
		}
		before, after := kernel.ApMap(beforeValue), kernel.ApMap(afterValue)
		binding := kernel.ApMap(kernel.ApMap(task["review_context"])["plan_review_binding"])
		if binding["checkpoint_ref"] != p.CheckpointRef || after["plan_review_control"] == nil {
			return nil, domain.Fail("REVIEW_PUBLICATION", "只有当前 Plan 尝试可以登记控制状态")
		}
		priorControl, nextControl := kernel.ApMap(before["plan_review_control"]), kernel.ApMap(after["plan_review_control"])
		priorAttempts, nextAttempts := kernel.ApArray(priorControl["attempts"]), kernel.ApArray(nextControl["attempts"])
		if priorControl == nil || len(nextAttempts) != len(priorAttempts)+1 {
			return nil, domain.Fail("REVIEW_PUBLICATION", "登记只能追加当前一次尝试，不能重置或删除历史")
		}
		prefix, _ := json.Marshal(nextAttempts[:len(priorAttempts)])
		original, _ := json.Marshal(priorAttempts)
		oldFrozen, newFrozen := kernel.ApCopy(priorControl), kernel.ApCopy(nextControl)
		delete(oldFrozen, "attempts")
		delete(newFrozen, "attempts")
		oldBytes, _ := json.Marshal(oldFrozen)
		newBytes, _ := json.Marshal(newFrozen)
		if !bytes.Equal(prefix, original) || !bytes.Equal(oldBytes, newBytes) {
			return nil, domain.Fail("REVIEW_PUBLICATION", "登记不能改变原范围、政策、诊断或历史")
		}
		delete(before, "plan_review_control")
		delete(after, "plan_review_control")
		left, _ := json.Marshal(before)
		right, _ := json.Marshal(after)
		if !bytes.Equal(left, right) {
			return nil, domain.Fail("REVIEW_PUBLICATION", "发布不得改变已有合同、用户决定或其他 checkpoint 内容")
		}
		changes = append(changes, assetChange{Ref: p.CheckpointRef, After: []byte(p.CheckpointAfter)})
	}
	ops, err := assetOperations(s, changes)
	if err != nil {
		return nil, err
	}
	guards := map[string]domain.Descriptor{}
	written := map[string]bool{}
	for _, op := range ops {
		written[op.Path] = true
	}
	for ref, d := range s.V.Observed {
		if !written[ref] && ref != ".yss/transactions" && !strings.HasPrefix(ref, ".yss/transactions/") {
			guards[ref] = d
		}
	}
	if err = s.Finish(); err != nil {
		return nil, err
	}
	tx, err := transaction.ApplyContextWithValidation(ctx, root, "review-package", ops, guards, nil, func() error {
		fresh := kernel.NewSemanticSession(ctx, root, args)
		fresh.InsideTransaction = true
		if e := fresh.Authorities(); e != nil {
			return e
		}
		if _, e := fresh.ContextContract(); e != nil {
			return e
		}
		if e := fresh.Verify("task", taskRef, nil); e != nil {
			return e
		}
		return fresh.Finish()
	})
	if err != nil {
		return tx, err
	}
	return map[string]any{"task_ref": taskRef, "task_digest": safefs.Digest([]byte(p.Files["task.json"])), "bundle_ref": path.Join(p.OutputDir, "bundle-draft.yaml"), "coverage_ref": path.Join(p.OutputDir, "coverage.md"), "decision": "pending", "execution_authorization": "not-granted", "transaction": tx}, nil
}

func ValidateReviewPublicationTransaction(root string, paths []string) error {
	taskRef := ""
	for _, ref := range paths {
		if path.Base(ref) == "task.json" {
			if taskRef != "" {
				return domain.Fail("STATE", "审阅包归档任务不唯一")
			}
			taskRef = ref
		}
	}
	if taskRef == "" {
		return domain.Fail("STATE", "审阅包归档缺少任务")
	}
	if !reviewPublicationRef(path.Dir(taskRef)) {
		return domain.Fail("STATE", "审阅包归档目录越出允许范围")
	}
	raw, err := transaction.ArchivedFile(root, taskRef)
	if err != nil {
		return err
	}
	value, err := schema.Parse(raw)
	if err != nil {
		return err
	}
	task := kernel.ApMap(value)
	if kernel.ApNumber(task["schema_version"]) != 3 || kernel.ApMap(task["provenance"])["task_ref"] != taskRef || task["workflow_status"] != "not-started" {
		return domain.Fail("STATE", "审阅包归档任务未知")
	}
	allowed := map[string]bool{}
	for _, name := range pendingReviewFiles {
		allowed[path.Join(path.Dir(taskRef), name)] = true
	}
	if binding := kernel.ApMap(kernel.ApMap(task["review_context"])["plan_review_binding"]); binding != nil {
		checkpointRef := kernel.Text(binding["checkpoint_ref"])
		if !reviewPublicationRef(checkpointRef) || checkpointRef != task["checkpoint_ref"] {
			return domain.Fail("STATE", "审阅包归档控制记录越出原任务范围")
		}
		allowed[checkpointRef] = true
	}
	for ref := range allowed {
		for parent := path.Dir(ref); parent != "."; parent = path.Dir(parent) {
			allowed[parent] = true
		}
	}
	for _, ref := range paths {
		if !allowed[ref] {
			return domain.Fail("STATE", "审阅包恢复越出归档范围: "+ref)
		}
	}
	return nil
}
