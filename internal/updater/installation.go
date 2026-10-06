package updater

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

type FileDiagnostic struct {
	Path     string            `json:"path"`
	Status   string            `json:"status"`
	Reason   string            `json:"reason,omitempty"`
	Expected domain.Descriptor `json:"expected"`
	Actual   domain.Descriptor `json:"actual"`
}
type InstallationDiagnostic struct {
	ToolRoot                  string           `json:"toolRoot"`
	Consistent                bool             `json:"consistent"`
	Status                    string           `json:"status"`
	RunningVersion            string           `json:"runningVersion,omitempty"`
	RecordedVersion           string           `json:"recordedVersion,omitempty"`
	ManifestVersion           string           `json:"manifestVersion,omitempty"`
	RunningProgramMatchesRoot bool             `json:"runningProgramMatchesRoot"`
	Files                     []FileDiagnostic `json:"files"`
	Issues                    []string         `json:"issues"`
	NextSteps                 []string         `json:"nextSteps"`
	ReadOnly                  bool             `json:"readOnly"`
}

type installationError struct {
	cause      error
	diagnostic InstallationDiagnostic
}

func (e *installationError) Unwrap() error { return e.cause }
func (e *installationError) ErrorResult() any {
	return map[string]any{"message": e.cause.Error(), "diagnostic": e.diagnostic}
}
func (e *installationError) Error() string {
	var out strings.Builder
	fmt.Fprintf(&out, "%s\n工具目录: %s", e.cause.Error(), e.diagnostic.ToolRoot)
	if e.diagnostic.RunningProgramMatchesRoot {
		fmt.Fprintf(&out, "\n运行版本: %s", e.diagnostic.RunningVersion)
	} else {
		out.WriteString("\n运行程序来自其他目录；未比较运行版本")
	}
	if e.diagnostic.RecordedVersion != "" {
		fmt.Fprintf(&out, "\n安装记录版本: %s", e.diagnostic.RecordedVersion)
	}
	for _, f := range e.diagnostic.Files {
		if f.Status != "unchanged" {
			fmt.Fprintf(&out, "\n冲突文件: %s（%s）\n  预期: %s %s mode=%#o\n  实际: %s %s mode=%#o", f.Path, f.Reason, f.Expected.Type, f.Expected.Digest, f.Expected.Mode, f.Actual.Type, f.Actual.Digest, f.Actual.Mode)
		}
	}
	if len(e.diagnostic.NextSteps) > 0 {
		out.WriteString("\n下一步:\n  ")
		out.WriteString(strings.Join(e.diagnostic.NextSteps, "\n  "))
	}
	return out.String()
}
func quotedPath(path string) string {
	if runtime.GOOS == "windows" {
		return "'" + strings.ReplaceAll(path, "'", "''") + "'"
	}
	return "'" + strings.ReplaceAll(path, "'", "'\\''") + "'"
}
func diagnosticSteps(root, code string) []string {
	status := "yss update status --tool-root " + quotedPath(root) + " --json"
	if code == "STATE" {
		return []string{status, "存在未完成事务；先检查状态与归档，再执行 yss update recover --tool-root " + quotedPath(root)}
	}
	return []string{status, "保留旧目录及归档；选择不存在的新目录安装，例如：", "yss upgrade --tool-root " + quotedPath(root+"-clean"), "使用新目录内的 yss version --json 和 yss update status --tool-root <新目录> --json 核验后，再调整 PATH 入口；完整离线安装案例见 yss help tutorial"}
}

// The same strict, read-only inspection supplies online preflight, offline
// conflict diagnostics and status. Errors never authorize a repair or adoption.
func inspectInstallation(root string, requireSettled bool, executable func() (string, error)) (*installedProgram, InstallationDiagnostic, error) {
	report := InstallationDiagnostic{ToolRoot: root, Status: "unverified", Files: []FileDiagnostic{}, Issues: []string{}, NextSteps: []string{}, ReadOnly: true}
	if executable == nil {
		executable = os.Executable
	}
	if current, err := executable(); err == nil {
		if current, err = filepath.EvalSymlinks(current); err == nil {
			currentRoot := filepath.Dir(current)
			matches := currentRoot == root
			if runtime.GOOS == "windows" {
				matches = strings.EqualFold(currentRoot, root)
			}
			if matches && filepath.Base(current) == fileName() {
				report.RunningProgramMatchesRoot = true
				report.RunningVersion = domain.Version
			}
		}
	}
	unfinished := false
	reject := func(err error) (*installedProgram, InstallationDiagnostic, error) {
		report.Consistent = false
		report.Status = "inconsistent"
		report.Issues = append(report.Issues, err.Error())
		code := "ARTIFACT"
		var de *domain.Error
		if errors.As(err, &de) {
			code = de.Code
		}
		if unfinished {
			report.Status = "incomplete"
			report.Issues = append(report.Issues, "存在未完成事务，须先检查恢复范围")
			code = "STATE"
		}
		report.NextSteps = diagnosticSteps(root, code)
		return nil, report, &installationError{err, report}
	}
	txn, err := transaction.Status(root)
	if err != nil {
		return reject(err)
	}
	unfinished = len(txn.Pending) > 0 || len(txn.Preparations) > 0
	if requireSettled && (len(txn.Pending) > 0 || len(txn.Preparations) > 0) {
		return reject(fail("STATE", "工具目录有未完成事务"))
	}
	receiptPath, err := safefs.Path(root, receiptRef)
	if err != nil {
		return reject(err)
	}
	b, err := os.ReadFile(receiptPath)
	if os.IsNotExist(err) {
		report.Status = "not-installed"
		if len(txn.Pending) > 0 || len(txn.Preparations) > 0 {
			report.Status = "incomplete"
			report.Issues = append(report.Issues, "首次安装事务尚未完成，缺少安装收据")
			report.NextSteps = diagnosticSteps(root, "STATE")
			return nil, report, nil
		}
		refs := []string{}
		for ref := range expectedFiles() {
			refs = append(refs, ref)
		}
		sort.Strings(refs)
		for _, ref := range refs {
			got, describeErr := safefs.Describe(root, ref)
			if describeErr != nil {
				return reject(describeErr)
			}
			if got.Type != "missing" {
				report.Files = append(report.Files, FileDiagnostic{Path: ref, Status: "unmanaged", Reason: "缺少安装收据，不能确认受管来源", Expected: domain.Descriptor{Type: "missing"}, Actual: got})
			}
		}
		if len(report.Files) > 0 {
			return reject(fail("CONFLICT", "首次安装拒绝覆盖已有文件；工具目录缺少安装收据"))
		}
		return nil, report, nil
	}
	if err != nil {
		return reject(err)
	}
	var installed installedProgram
	if err = parseJSON(b, &installed.receipt); err != nil {
		return reject(err)
	}
	r := installed.receipt
	report.RecordedVersion = r.CLIVersion
	if r.SchemaVersion != 1 || r.ProtocolVersion != domain.ProtocolVersion || r.Platform != runtime.GOOS+"/"+runtime.GOARCH || !digestPattern.MatchString(r.ArchiveDigest) || r.SourceState != "committed" && r.SourceState != "working-tree" || r.StableReady && r.SourceState != "committed" {
		return reject(fail("ARTIFACT", "未知程序安装合同"))
	}
	if _, err = versionParts(r.CLIVersion); err != nil {
		return reject(err)
	}
	path, err := safefs.Path(root, "release-manifest.json")
	if err != nil {
		return reject(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return reject(err)
	}
	if !info.Mode().IsRegular() || info.Size() > 8<<20 {
		return reject(fail("ARTIFACT", "安装 manifest 非普通文件或超过 8 MiB"))
	}
	b, err = os.ReadFile(path)
	if err != nil {
		return reject(err)
	}
	if err = parseJSON(b, &installed.manifest); err != nil {
		return reject(err)
	}
	m := installed.manifest
	if report.RunningProgramMatchesRoot && r.CLIVersion != domain.Version {
		report.Issues = append(report.Issues, "运行版本与安装记录版本不同")
	}
	report.ManifestVersion = m.CLIVersion
	if m.SchemaVersion != 1 || m.CLIVersion != r.CLIVersion || m.Platform != r.Platform || m.ProtocolVersion != r.ProtocolVersion || m.SourceState != r.SourceState || m.StableReady != r.StableReady || m.CGO || m.StableReady && m.RuntimeVerification != "passed" {
		return reject(fail("ARTIFACT", "安装收据与 manifest 身份不一致"))
	}
	for _, ref := range []string{fileName(), "README.md", "docs/source-lock.json", "docs/compatibility.md"} {
		if _, ok := m.Files[ref]; !ok {
			return reject(fail("ARTIFACT", "安装 manifest 缺少核心文件: "+ref))
		}
	}
	refs := []string{}
	for ref := range m.Files {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	drift := false
	for _, ref := range refs {
		want := m.Files[ref]
		if !expectedFiles()[ref] || ref == "release-manifest.json" || want.Type != "file" || !digestPattern.MatchString(want.Digest) || want.Mode != 0644 && want.Mode != 0755 || ref == fileName() && want.Mode != 0755 {
			return reject(fail("ARTIFACT", "安装 manifest 描述不合法: "+ref))
		}
		want.Mode = domain.FileMode(want.Mode)
		got, err := safefs.Describe(root, ref)
		if err != nil {
			return reject(err)
		}
		f := FileDiagnostic{Path: ref, Status: "unchanged", Expected: want, Actual: got}
		if got != want {
			drift = true
			f.Status = "changed"
			reasons := []string{}
			if got.Type != want.Type {
				reasons = append(reasons, "文件类型或存在性不同")
			}
			if got.Digest != want.Digest {
				reasons = append(reasons, "内容摘要不同")
			}
			if got.Mode != want.Mode {
				reasons = append(reasons, "文件权限不同")
			}
			f.Reason = strings.Join(reasons, "、")
		}
		report.Files = append(report.Files, f)
	}
	if drift {
		return reject(fail("CONFLICT", "受管程序文件与安装清单不一致"))
	}
	lockPath, err := safefs.Path(root, "docs/source-lock.json")
	if err != nil {
		return reject(err)
	}
	lock, err := os.ReadFile(lockPath)
	if err != nil {
		return reject(err)
	}
	if err = validateProvenance(m, lock); err != nil {
		return reject(err)
	}
	if err = validateStableProof(m, lock, b); err != nil {
		return reject(err)
	}
	if report.RunningProgramMatchesRoot && (r.CLIVersion != domain.Version || domain.BuildSourceState == "committed" && m.CLICommit != domain.BuildCommit) {
		return reject(fail("INSTALLATION", "安装身份与实际运行 CLI 不一致"))
	}
	report.Consistent = true
	report.Status = "consistent"
	if len(txn.Pending) > 0 || len(txn.Preparations) > 0 {
		report.Consistent = false
		report.Status = "incomplete"
		report.Issues = append(report.Issues, "存在未完成事务，当前安装尚未稳定")
		report.NextSteps = diagnosticSteps(root, "STATE")
	}
	return &installed, report, nil
}

func installationConflict(root string, cause error) error {
	_, report, _ := inspectInstallation(root, false, nil)
	report.Consistent = false
	if report.Status != "incomplete" {
		report.Status = "inconsistent"
	}
	if len(report.Issues) == 0 {
		report.Issues = append(report.Issues, cause.Error())
	}
	if report.Status != "incomplete" {
		report.NextSteps = diagnosticSteps(root, "CONFLICT")
	}
	return &installationError{cause, report}
}
