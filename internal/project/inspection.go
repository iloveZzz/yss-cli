package project

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/governance"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

type Inspection struct {
	Kind             string           `json:"kind"`
	SchemaVersion    int              `json:"schemaVersion"`
	Root             string           `json:"root"`
	IdentityState    string           `json:"identityState"`
	Profile          string           `json:"profile,omitempty"`
	Installation     InstallationKind `json:"installation,omitempty"`
	RepositoryMode   string           `json:"repositoryMode,omitempty"`
	TransactionState string           `json:"transactionState"`
	Notices          []string         `json:"notices"`
}

// Inspect reports observations only. A legacy/absent classification grants no
// attach or recovery authority, and transaction.Status does not acquire a lock.
func Inspect(ctx context.Context, root string) (*Inspection, error) {
	if ctx == nil {
		return nil, domain.Fail("ARGUMENT", "取消上下文不可为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, domain.Wrap("CANCELLED", err)
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if _, err = safefs.Path(root, "yss-project.yaml"); err != nil {
		return nil, err
	}
	view := &Inspection{Kind: "project-inspection", SchemaVersion: 1, Root: root, IdentityState: "absent", TransactionState: "unknown", Notices: []string{}}
	id, detectErr := Detect(root, "", true)
	if detectErr != nil {
		if unreadable(detectErr) {
			return nil, detectErr
		}
		view.IdentityState = classifyIdentityError(detectErr)
		view.Notices = append(view.Notices, detectErr.Error())
	} else if id.Native != nil {
		view.IdentityState = "native"
		view.Profile, view.RepositoryMode = id.Profile.Name, id.Mode
		view.Installation = InstallationGoverned
		if id.Native.SchemaVersion == 5 {
			view.Installation = InstallationKind(text(id.Native.Distribution["installation"]))
		}
	} else {
		state, notices, err := inspectUnboundMarkers(root)
		if err != nil {
			return nil, err
		}
		view.IdentityState = state
		view.Notices = append(view.Notices, notices...)
	}
	state, notices, err := inspectTransactions(ctx, root)
	if err != nil {
		return nil, err
	}
	view.TransactionState = state
	view.Notices = append(view.Notices, notices...)
	return view, nil
}

func unreadable(err error) bool {
	var pathErr *os.PathError
	return errors.As(err, &pathErr) && !os.IsNotExist(err)
}

func classifyIdentityError(err error) string {
	var detail interface{ DiagnosticDetail() domain.ErrorDetail }
	if errors.As(err, &detail) {
		switch detail.DiagnosticDetail().ID {
		case "METADATA_SCHEMA_UNSUPPORTED", "PROFILE_REMOVED":
			return "unsupported"
		}
	}
	var rejection *domain.Error
	if errors.As(err, &rejection) {
		switch rejection.Code {
		case "LEGACY":
			return "legacy"
		case "CAPABILITY", "PLAN_VERSION":
			return "unsupported"
		}
	}
	return "invalid"
}

func inspectUnboundMarkers(root string) (string, []string, error) {
	for _, ref := range []string{"yss-project.yaml", ".template-spec/process/harness-profile.yaml"} {
		ok, err := exists(root, ref)
		if err != nil {
			if unreadable(err) {
				return "", nil, err
			}
			return "invalid", []string{err.Error()}, nil
		}
		if !ok {
			continue
		}
		value, err := load(root, ref)
		if err != nil {
			if unreadable(err) {
				return "", nil, err
			}
			return "invalid", []string{err.Error()}, nil
		}
		m, ok := object(value)
		if ref == "yss-project.yaml" && ok && ((number(m["schema_version"]) != 1 && number(m["schema_version"]) != 2) || text(m["repository_mode"]) != "project-instance") {
			return "unsupported", []string{"根身份版本或仓库模式不支持当前项目接入"}, nil
		}
		return "invalid", []string{"存在 YSS 身份或 Profile 合同，但缺少合法统一 CLI metadata"}, nil
	}
	return "absent", []string{}, nil
}

func inspectTransactions(ctx context.Context, root string) (string, []string, error) {
	if err := ctx.Err(); err != nil {
		return "unknown", nil, domain.Wrap("CANCELLED", err)
	}
	status, err := transaction.Status(root)
	if err != nil {
		if unreadable(err) {
			return "unknown", nil, err
		}
		var rejection *domain.Error
		if errors.As(err, &rejection) {
			return "blocked", []string{err.Error()}, nil
		}
		return "unknown", []string{err.Error()}, nil
	}
	if err := ctx.Err(); err != nil {
		return "unknown", nil, domain.Wrap("CANCELLED", err)
	}
	if len(status.Pending) > 0 || len(status.Preparations) > 0 {
		return "pending", []string{"存在未结束事务或准备记录；只读检查未执行恢复"}, nil
	}
	return "clear", []string{}, nil
}

type OutputInspection struct {
	Outcome    string   `json:"outcome"`
	Missing    []string `json:"missing"`
	Mismatched []string `json:"mismatched"`
	Notices    []string `json:"notices"`
}

// VerifyPlanOutput observes postconditions directly, never reusing the inputs
// that were expected before application or attempting apply/recovery again.
func VerifyPlanOutput(ctx context.Context, p *Plan) (*OutputInspection, error) {
	if ctx == nil {
		return nil, domain.Fail("ARGUMENT", "取消上下文不可为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, domain.Wrap("CANCELLED", err)
	}
	if p == nil {
		return nil, domain.Fail("ARGUMENT", "缺少安装保存计划")
	}
	if p.SchemaVersion < 2 || p.SchemaVersion > 5 || p.ProtocolVersion != domain.ProtocolVersion {
		return nil, domain.Fail("PLAN_VERSION", "安装保存计划类型或版本不支持；请重新规划")
	}
	if p.Digest == "" || p.Digest != planDigest(p) || p.Command == "doctor" || p.Command == "diff" || !filepath.IsAbs(p.Root) {
		return nil, domain.Fail("ARGUMENT", "安装计划摘要、操作或目标路径不合法")
	}
	if p.SchemaVersion >= 4 && p.Installation != InstallationSkills && p.Installation != InstallationGoverned {
		return nil, domain.Fail("ARGUMENT", "安装计划缺少合法安装方式")
	}
	if len(p.Conflicts) > 0 || len(p.Blockers) > 0 || !p.ReadyToApply {
		return nil, domain.Fail("ARGUMENT", "含阻断或冲突的计划不能声明已经应用")
	}
	b, err := bundle.Load(p.Profile)
	if err != nil {
		return nil, err
	}
	if err = verifyInstallationPlanSource(p, b); err != nil {
		return nil, err
	}
	view := &OutputInspection{Outcome: "unknown", Missing: []string{}, Mismatched: []string{}, Notices: []string{}}
	if _, err := safefs.Path(p.Root, MetadataFile); err != nil {
		view.Notices = append(view.Notices, err.Error())
		return view, nil
	}
	rootInfo, err := os.Lstat(p.Root)
	if os.IsNotExist(err) {
		view.Outcome = "not-applied"
		for _, change := range p.Changes {
			if change.After.Type != "missing" {
				view.Missing = append(view.Missing, change.Path)
			}
		}
		sort.Strings(view.Missing)
		return view, nil
	}
	if err != nil {
		view.Notices = append(view.Notices, err.Error())
		return view, nil
	}
	if !rootInfo.IsDir() {
		view.Mismatched = append(view.Mismatched, ".")
		return view, nil
	}
	allBefore, unknown, changedMatches := true, false, 0
	seen := map[string]domain.Descriptor{}
	check := func(ref string, before, after domain.Descriptor) error {
		if err := ctx.Err(); err != nil {
			return domain.Wrap("CANCELLED", err)
		}
		if err := safefs.ValidateRef(ref); err != nil {
			return err
		}
		if after.Type != "file" && after.Type != "directory" && after.Type != "missing" {
			return domain.Fail("ARGUMENT", "安装计划包含未知输出类型")
		}
		seen[ref] = after
		actual, err := workDescribe(p.Root, ref)
		if err != nil {
			allBefore = false
			if unreadable(err) {
				unknown = true
				view.Notices = append(view.Notices, ref+": "+err.Error())
			} else {
				view.Mismatched = append(view.Mismatched, ref)
			}
			return nil
		}
		allBefore = allBefore && outputDescriptorMatches(actual, before)
		if outputDescriptorMatches(actual, after) {
			if !outputDescriptorMatches(before, after) {
				changedMatches++
			}
		} else if actual.Type == "missing" && after.Type != "missing" {
			view.Missing = append(view.Missing, ref)
		} else {
			view.Mismatched = append(view.Mismatched, ref)
		}
		return nil
	}
	for _, change := range p.Changes {
		if _, duplicate := seen[change.Path]; duplicate {
			return nil, domain.Fail("ARGUMENT", "安装计划包含重复变更路径")
		}
		if err := check(change.Path, change.Before, change.After); err != nil {
			return nil, err
		}
	}
	for _, asset := range p.Assets {
		if asset.Action == "preserve" || asset.Action == "retired-preserved" {
			continue
		}
		want := asset.Target
		switch asset.Action {
		case "unchanged", "add", "update":
		case "delete", "rename":
			want = domain.Descriptor{Type: "missing"}
		default:
			return nil, domain.Fail("ARGUMENT", "安装计划包含未知资产结果")
		}
		if checked, exists := seen[asset.Path]; exists {
			if !outputDescriptorMatches(checked, want) {
				return nil, domain.Fail("ARGUMENT", "安装计划的变更与资产输出描述矛盾")
			}
			continue
		}
		if err := check(asset.Path, asset.Before, want); err != nil {
			return nil, err
		}
	}
	id, identityErr := Detect(p.Root, p.Profile, false)
	nativeMatches := identityErr == nil && id.Native != nil && id.Native.TemplateCommit == p.TemplateCommit && id.Native.SnapshotHash == p.SnapshotHash && id.Native.Profile == p.Profile
	installation := InstallationGoverned
	if p.SchemaVersion >= 4 {
		installation = p.Installation
	}
	if nativeMatches {
		actualInstallation := InstallationGoverned
		if id.Native.SchemaVersion == 5 {
			actualInstallation = InstallationKind(text(id.Native.Distribution["installation"]))
		}
		nativeMatches = actualInstallation == installation && (p.SchemaVersion != 5 || id.Native.BundleHash == p.BundleHash)
	}
	if identityErr != nil {
		view.Notices = append(view.Notices, identityErr.Error())
		unknown = unknown || unreadable(identityErr)
	} else if !nativeMatches {
		view.Notices = append(view.Notices, "实际身份、安装方式或来源与计划不一致")
	}
	identityValid := nativeMatches
	if nativeMatches {
		if err := verifySkillTrees(p.Root, id.Native); err != nil {
			identityValid = false
			unknown = unknown || unreadable(err)
			view.Notices = append(view.Notices, err.Error())
		}
		if id.Profile.Name == "spec" && id.Native.SchemaVersion == 5 {
			readiness, err := CheckInstalledSkills(id, b)
			if err != nil || readiness.Status != "ready" {
				identityValid = false
				if err != nil {
					unknown = unknown || unreadable(err)
					view.Notices = append(view.Notices, err.Error())
				} else {
					view.Notices = append(view.Notices, "已安装技能未就绪: "+readiness.Status)
				}
			}
		}
		if !skillsIdentity(id) && !IsMattSpec(id) {
			if _, err := governance.RunContext(ctx, "context", "verify", p.Root, nil); err != nil {
				if ctx.Err() != nil {
					return nil, domain.Wrap("CANCELLED", ctx.Err())
				}
				identityValid = false
				unknown = unknown || unreadable(err)
				view.Notices = append(view.Notices, err.Error())
			}
		}
	}
	transactionState, notices, err := inspectTransactions(ctx, p.Root)
	if err != nil {
		if ctx.Err() != nil {
			return nil, err
		}
		unknown = true
		view.Notices = append(view.Notices, err.Error())
	}
	view.Notices = append(view.Notices, notices...)
	if transactionState == "unknown" {
		unknown = true
	}
	switch {
	case unknown:
		view.Outcome = "unknown"
	case len(view.Missing) == 0 && len(view.Mismatched) == 0 && identityValid && transactionState == "clear":
		view.Outcome = "complete"
	case allBefore && changedMatches == 0 && len(p.Changes) > 0 && transactionState == "clear":
		view.Outcome = "not-applied"
	case changedMatches > 0 || nativeMatches:
		view.Outcome = "incomplete"
	default:
		view.Notices = append(view.Notices, "无法从现有输出确认本计划是否已经应用")
	}
	sort.Strings(view.Missing)
	sort.Strings(view.Mismatched)
	return view, nil
}

func outputDescriptorMatches(actual, expected domain.Descriptor) bool {
	return actual.Type == expected.Type && actual.Digest == expected.Digest && (actual.Type == "missing" || domain.FileMode(actual.Mode) == domain.FileMode(expected.Mode))
}
