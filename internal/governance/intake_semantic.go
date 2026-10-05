package governance

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func init() { registerSemanticValidator("intake-observation", verifyIntakeObservationSemantic) }
func (s *semanticSession) intakeGit(root string, args ...string) (semanticGitObservation, error) {
	row, err := s.gitFresh(root, args)
	if err != nil {
		return row, err
	}
	key := root + "\x00" + strings.Join(args, "\x00")
	if old, ok := s.gitInputs[key]; ok && (old.Exit != row.Exit || old.Output != row.Output) {
		return row, s.unavailable("INPUT_DRIFT", "Git 分诊观测变化")
	}
	s.gitInputs[key] = row
	return row, nil
}
func (s *semanticSession) intakeSnapshot(root string, depth int) (map[string]any, error) {
	if depth > 16 {
		return nil, s.unavailable("CAPABILITY", "子仓观测深度超过16层")
	}
	row, err := s.intakeGit(root, "ls-files", "-z", "--cached", "--others")
	if err != nil {
		return nil, err
	}
	if row.Exit != 0 {
		return nil, s.unavailable("CAPABILITY", "无法观察 Git 仓库文件集合")
	}
	refs := uniqueStrings(strings.Split(strings.TrimSuffix(row.Output, "\x00"), "\x00"))
	if len(refs) > 50000 {
		return nil, s.unavailable("CAPABILITY", "分诊观测超过50000项")
	}
	files := map[string]any{}
	for _, ref := range refs {
		if ref == "" {
			continue
		}
		if err = s.guard(); err != nil {
			return nil, err
		}
		if err = safefs.ValidateRef(ref); err != nil {
			return nil, s.unavailable("PATH", err.Error())
		}
		parent := filepath.ToSlash(filepath.Dir(ref))
		p := filepath.Join(root, filepath.FromSlash(ref))
		if parent != "." {
			if _, err = safefs.Path(root, parent); err != nil {
				return nil, s.unavailable("PATH", err.Error())
			}
		} else if _, err = safefs.Path(root, "yss-project.yaml"); err != nil {
			return nil, s.unavailable("PATH", err.Error())
		}
		st, e := os.Lstat(p)
		if os.IsNotExist(e) {
			files[ref] = nil
			continue
		}
		if e != nil {
			return nil, s.unavailable("INPUT", e.Error())
		}
		if st.IsDir() {
			nested, e := s.intakeGit(p, "rev-parse", "--show-toplevel")
			if e != nil {
				return nil, e
			}
			if nested.Exit != 0 || filepath.Clean(strings.TrimSpace(nested.Output)) != p {
				return nil, s.reject("INTAKE_REPOSITORY", "子仓未初始化: "+ref)
			}
			head, e := s.intakeGit(p, "rev-parse", "HEAD")
			if e != nil {
				return nil, e
			}
			if head.Exit != 0 {
				return nil, s.reject("INTAKE_REPOSITORY", "子仓不可观测: "+ref)
			}
			subtree, e := s.intakeSnapshot(p, depth+1)
			if e != nil {
				return nil, e
			}
			files[ref] = map[string]any{"kind": "gitlink", "head": strings.TrimSpace(head.Output), "files": subtree}
			continue
		}
		kind := "file"
		mode := int64(st.Mode().Perm()) | 0100000
		if st.Mode()&os.ModeSetuid != 0 {
			mode |= 04000
		}
		if st.Mode()&os.ModeSetgid != 0 {
			mode |= 02000
		}
		if st.Mode()&os.ModeSticky != 0 {
			mode |= 01000
		}
		var b []byte
		if st.Mode()&os.ModeSymlink != 0 {
			kind = "link"
			mode = int64(st.Mode().Perm()) | 0120000
			target, e := os.Readlink(p)
			if e != nil {
				return nil, s.unavailable("INPUT", e.Error())
			}
			b = []byte(target)
		} else if st.Mode().IsRegular() {
			b, e = os.ReadFile(p)
			if e != nil {
				return nil, s.unavailable("INPUT", e.Error())
			}
		} else {
			return nil, s.unavailable("PATH", "分诊观测包含不支持的文件类型")
		}
		files[ref] = map[string]any{"kind": kind, "mode": mode, "digest": "sha256:" + safefs.Digest(b)}
	}
	head, err := s.intakeGit(root, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return nil, err
	}
	var headValue any
	if head.Exit == 0 {
		headValue = strings.TrimSpace(head.Output)
	} else if head.Exit != 128 && head.Exit != 1 {
		return nil, s.unavailable("CAPABILITY", "无法观察 Git HEAD")
	}
	index, err := s.intakeGit(root, "ls-files", "--stage", "-z")
	if err != nil {
		return nil, err
	}
	if index.Exit != 0 {
		return nil, s.unavailable("CAPABILITY", "无法观察 Git index")
	}
	return map[string]any{"git": map[string]any{"head": headValue, "index_digest": "sha256:" + safefs.Digest([]byte(index.Output))}, "files": files}, nil
}
func verifyIntakeObservationSemantic(s *semanticSession, ref string, opts map[string]string) error {
	observation, err := s.doc(opts["observation"])
	if err != nil {
		return err
	}
	if text(observation["root"]) != s.root || observation["kind"] != "read-only-intake-observation" || !apEqual(observation["before"], observation["after"]) {
		return s.reject("INTAKE_OBSERVATION", "观测身份或前后状态不一致")
	}
	actual, err := s.intakeSnapshot(s.root, 0)
	if err != nil {
		return err
	}
	if !apEqual(actual, observation["after"]) {
		return s.reject("INTAKE_OBSERVATION", "仓库实际差异与只读声明冲突")
	}
	s.intakeInputs[s.root] = actual
	return nil
}
