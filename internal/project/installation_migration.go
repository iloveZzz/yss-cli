package project

import (
	"context"
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/governance"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"go.yaml.in/yaml/v3"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type InstallationMigration struct {
	Source                           InstallationKind `json:"source"`
	Target                           InstallationKind `json:"target"`
	WorkRoots                        []string         `json:"workRoots"`
	WorkDigest                       string           `json:"workDigest"`
	RetainedTemplateSourceReferences []string         `json:"retainedTemplateSourceReferences,omitempty"`
}

func BuildInstallationMigration(ctx context.Context, root string, target InstallationKind) (*Plan, error) {
	if ctx == nil {
		return nil, domain.Fail("ARGUMENT", "需要取消上下文")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return BuildWithOptions(root, "", "migrate", nil, nil, nil, PlanningOptions{Installation: target})
}
func migrationWorkRoots(root string, required bool) ([]string, error) {
	roots := []string{".work"}
	p, err := safefs.Path(root, ".template-spec/agents/issue-tracker.md")
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		if required {
			return nil, domain.Fail("MIGRATION_INPUT", "正式工程缺少 tracker，无法确认活动任务根")
		}
		return roots, nil
	}
	if err != nil {
		return nil, err
	}
	parts := strings.SplitN(string(data), "---", 3)
	if len(parts) != 3 {
		return nil, domain.Fail("MIGRATION_INPUT", "tracker frontmatter 不可读取")
	}
	var front map[string]any
	if err = yaml.Unmarshal([]byte(parts[1]), &front); err != nil {
		return nil, err
	}
	tracker, ok := object(front["tracker"])
	if !ok || text(tracker["root"]) == "" {
		return nil, domain.Fail("MIGRATION_INPUT", "tracker.root 缺失")
	}
	ref := strings.TrimSuffix(text(tracker["root"]), "/")
	if err = safefs.ValidateRef(ref); err != nil {
		return nil, err
	}
	return union(roots, []string{ref}), nil
}
func observeInstallationWork(ctx context.Context, root string, roots []string) (string, map[string]domain.Descriptor, error) {
	observed := map[string]domain.Descriptor{}
	for _, ref := range roots {
		p, err := safefs.Path(root, ref)
		if err != nil {
			return "", nil, err
		}
		if _, err = os.Lstat(p); os.IsNotExist(err) {
			observed[ref] = domain.Descriptor{Type: "missing"}
			continue
		} else if err != nil {
			return "", nil, err
		}
		err = filepath.WalkDir(p, func(file string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if len(observed) > 10000 {
				return domain.Fail("MIGRATION_INPUT", "工作资产过多，不能证明迁移范围")
			}
			rel, e := filepath.Rel(root, file)
			if e != nil {
				return e
			}
			rel = filepath.ToSlash(rel)
			if entry.Type()&os.ModeSymlink != 0 {
				return domain.Fail("PATH", "迁移工作目录含符号链接: "+rel)
			}
			d, e := workDescribe(root, rel)
			if e != nil {
				return e
			}
			observed[rel] = d
			return nil
		})
		if err != nil {
			return "", nil, err
		}
	}
	data, err := json.Marshal(observed)
	return safefs.Digest(data), observed, err
}
func validateInstallationWork(ctx context.Context, root string, roots []string) error {
	_, observed, err := observeInstallationWork(ctx, root, roots)
	if err != nil {
		return err
	}
	checkpoints := 0
	hasWork := false
	for ref, d := range observed {
		if d.Type != "file" {
			continue
		}
		hasWork = true
		name := filepath.Base(ref)
		if strings.Contains(ref, "/task-packages/") {
			return domain.Fail("MIGRATION_ACTIVE_TASK", "任务包需要按原政策处置后迁移: "+ref)
		}
		if !strings.HasPrefix(name, "checkpoint.") {
			continue
		}
		checkpoints++
		doc, err := schema.LoadFile(filepath.Join(root, filepath.FromSlash(ref)))
		if err != nil {
			return err
		}
		m, ok := object(doc)
		if !ok || m["status"] != "completed" {
			return domain.Fail("MIGRATION_ACTIVE_TASK", "工作单元未完成: "+ref)
		}
		if _, err = governance.RunContext(ctx, "lifecycle", "verify", root, map[string]string{"checkpoint": ref}); err != nil {
			return domain.Wrap("MIGRATION_ACTIVE_TASK", err)
		}
	}
	if hasWork && checkpoints == 0 {
		return domain.Fail("MIGRATION_ACTIVE_TASK", "存在未绑定可核验完成 checkpoint 的工作资产")
	}
	return nil
}
func planInstallationMigration(ctx context.Context, id *Identity, target InstallationKind) (*InstallationMigration, error) {
	if id.Native == nil || id.Native.SchemaVersion < 4 {
		return nil, domain.Fail("MIGRATION_UNSUPPORTED", "当前安装合同不支持此 metadata 格式；原件保留")
	}
	source := InstallationGoverned
	if skillsIdentity(id) {
		source = InstallationSkills
	}
	if source == target {
		return nil, domain.Fail("INSTALLATION_UNCHANGED", "当前安装方式无需转换")
	}
	roots, err := migrationWorkRoots(id.Root, source == InstallationGoverned)
	if err != nil {
		return nil, err
	}
	if source == InstallationGoverned {
		if err = validateInstallationWork(ctx, id.Root, roots); err != nil {
			return nil, err
		}
	}
	var retained []string
	if source == InstallationSkills && target == InstallationGoverned {
		retained, err = retainedTemplateSourceReferences(id)
		if err != nil {
			return nil, err
		}
		// Include the complete tree, including the missing state, so that a
		// new file or directory between plan and apply cannot go unnoticed.
		roots = union(roots, []string{".template-source"})
	}
	digest, _, err := observeInstallationWork(ctx, id.Root, roots)
	if err != nil {
		return nil, err
	}
	return &InstallationMigration{Source: source, Target: target, WorkRoots: roots, WorkDigest: digest, RetainedTemplateSourceReferences: retained}, nil
}
func verifyInstallationWork(ctx context.Context, p *Plan) error {
	if p.InstallationMigration == nil {
		return nil
	}
	digest, _, err := observeInstallationWork(ctx, p.Root, p.InstallationMigration.WorkRoots)
	if err != nil {
		return err
	}
	if digest != p.InstallationMigration.WorkDigest {
		return domain.Fail("INPUT_DRIFT", "迁移工作资产自规划后变化")
	}
	return nil
}
