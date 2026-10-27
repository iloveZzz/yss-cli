package scaffold

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

type OutputInspection struct {
	Outcome    string   `json:"outcome"`
	Missing    []string `json:"missing"`
	Mismatched []string `json:"mismatched"`
	Notices    []string `json:"notices"`
}

// VerifyPlanOutput checks only the outputs owned by p. Added IDE or business
// files are notices; no verification operation writes or removes anything.
func VerifyPlanOutput(ctx context.Context, p *Plan) (*OutputInspection, error) {
	if ctx == nil {
		return nil, domain.Fail("ARGUMENT", "取消上下文不可为空")
	}
	if err := validatePlan(p); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, domain.Wrap("CANCELLED", err)
	}
	view := &OutputInspection{Outcome: "unknown", Missing: []string{}, Mismatched: []string{}, Notices: []string{}}
	root, err := safefs.Path(p.Options.OutputDir, p.Options.ProjectName)
	if err != nil {
		view.Notices = append(view.Notices, "目标路径不能安全核查: "+err.Error())
		return view, nil
	}
	st, err := os.Lstat(root)
	if os.IsNotExist(err) {
		view.Outcome = "not-applied"
		for _, file := range p.Files {
			view.Missing = append(view.Missing, file.Path)
		}
		for _, dir := range p.Dirs {
			view.Missing = append(view.Missing, dir.Path)
		}
		return view, nil
	}
	if err != nil {
		view.Notices = append(view.Notices, "目标目录不可读取: "+err.Error())
		return view, nil
	}
	if !st.IsDir() {
		view.Mismatched = append(view.Mismatched, ".")
		return view, nil
	}
	owned := map[string]bool{}
	unknown, matched := false, 0
	check := func(ref string, directory bool, permission PermissionRule, digest string) error {
		if err := ctx.Err(); err != nil {
			return domain.Wrap("CANCELLED", err)
		}
		owned[ref] = true
		path, e := safefs.Path(root, ref)
		if e != nil {
			view.Mismatched = append(view.Mismatched, ref)
			view.Notices = append(view.Notices, e.Error())
			return nil
		}
		info, e := os.Lstat(path)
		if os.IsNotExist(e) {
			view.Missing = append(view.Missing, ref)
			return nil
		}
		if e != nil {
			unknown = true
			view.Notices = append(view.Notices, "无法读取 "+ref+": "+e.Error())
			return nil
		}
		if directory && !info.IsDir() || !directory && !info.Mode().IsRegular() {
			view.Mismatched = append(view.Mismatched, ref)
			return nil
		}
		if !directory {
			data, e := os.ReadFile(path)
			if e != nil {
				unknown = true
				view.Notices = append(view.Notices, "无法读取 "+ref+": "+e.Error())
				return nil
			}
			if safefs.Digest(data) != digest {
				view.Mismatched = append(view.Mismatched, ref)
				return nil
			}
		}
		if permission.Rule == "fixed" && !fixedPermissionMatches(info.Mode(), permission.Mode) {
			view.Mismatched = append(view.Mismatched, ref)
			return nil
		}
		matched++
		return nil
	}
	for _, file := range p.Files {
		if err := check(file.Path, false, file.Permission, file.SHA256); err != nil {
			return nil, err
		}
	}
	for _, dir := range p.Dirs {
		if err := check(dir.Path, true, dir.Permission, ""); err != nil {
			return nil, err
		}
	}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return domain.Wrap("CANCELLED", err)
		}
		if walkErr != nil {
			view.Notices = append(view.Notices, "附加文件未能完整列举: "+walkErr.Error())
			return nil
		}
		if path == root {
			return nil
		}
		ref, _ := filepath.Rel(root, path)
		ref = filepath.ToSlash(ref)
		if !owned[ref] {
			view.Notices = append(view.Notices, "非本计划文件: "+ref)
			if entry.IsDir() {
				return filepath.SkipDir
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	switch {
	case unknown:
		view.Outcome = "unknown"
	case len(view.Missing) == 0 && len(view.Mismatched) == 0:
		view.Outcome = "complete"
	case matched > 0:
		view.Outcome = "incomplete"
	default:
		view.Notices = append(view.Notices, "现有目标缺少可确认的本计划输出，无法确认是否应用")
	}
	return view, nil
}

func fixedPermissionMatches(actual os.FileMode, expected uint32) bool {
	if runtime.GOOS == "windows" {
		// Windows chmod exposes only the writable bit; execute/group bits are not
		// POSIX permissions and must not be claimed as verified.
		return actual.Perm()&0o200 == os.FileMode(expected)&0o200
	}
	return actual.Perm() == os.FileMode(expected)
}
