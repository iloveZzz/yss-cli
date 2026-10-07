package transaction

import (
	"os"
	"path/filepath"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

// File callers retain the safefs contract. Only explicitly journaled ordinary
// directories and read-only directory guards use this transaction descriptor.
func describeTarget(root, ref string) (domain.Descriptor, error) {
	p, e := safefs.Path(root, ref)
	if e != nil {
		return domain.Descriptor{}, e
	}
	st, e := os.Lstat(p)
	if e == nil && st.IsDir() {
		return domain.Descriptor{Type: "directory", Mode: uint32(st.Mode().Perm())}, nil
	}
	return safefs.Describe(root, ref)
}

func describeOperation(root, ref string, r record) (domain.Descriptor, error) {
	if r.After.Type == "directory" {
		return describeTarget(root, ref)
	}
	return safefs.Describe(root, ref)
}

func replaceDirectory(root, base string, i int, r record, wanted domain.Descriptor, role string, dirty map[string]bool) error {
	if e := ensureParents(root, r.Path); e != nil {
		return e
	}
	ref := temporary(r.Path, base, i, role)
	temp, e := safefs.Path(root, ref)
	if e != nil {
		return e
	}
	if e = os.Mkdir(temp, 0700); e != nil {
		return e
	}
	if e = os.Chmod(temp, os.FileMode(wanted.Mode)); e != nil {
		return e
	}
	if e = syncDirectory(temp); e != nil {
		return e
	}
	dest, e := safefs.Path(root, r.Path)
	if e != nil {
		return e
	}
	if e = os.Rename(temp, dest); e != nil {
		return e
	}
	if e = persistDirectory(dest, dirty); e != nil {
		return e
	}
	return persistDirectory(filepath.Dir(dest), dirty)
}

// A new child cannot be removed by rollback, or discovered after some files
// have already been restored. Inspect every created directory before any write.
func preflightCreatedDirectories(root string, l loaded, n int) error {
	// Source directories remain in place when their files retire. Recovery must
	// not silently recreate them or restore files under later permission edits.
	for ref, wanted := range l.plan.Guards {
		if wanted.Type != "directory" {
			continue
		}
		current, e := describeTarget(root, ref)
		if e != nil {
			return e
		}
		if current != wanted {
			return fail("RECOVERY_FAILED", "源目录或权限已有后续修改: "+ref)
		}
	}
	allowed := map[string]bool{}
	for i := 0; i < n; i++ {
		r := l.plan.Operations[i]
		allowed[r.Path] = true
		for _, role := range []string{"apply", "restore"} {
			allowed[temporary(r.Path, l.base, i, role)] = true
		}
	}
	for i := 0; i < n; i++ {
		r := l.plan.Operations[i]
		if r.After.Type != "directory" {
			continue
		}
		p, e := safefs.Path(root, r.Path)
		if e != nil {
			return e
		}
		if _, e = os.Lstat(p); os.IsNotExist(e) {
			continue
		} else if e != nil {
			return e
		}
		e = filepath.WalkDir(p, func(file string, entry os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			ref, e := filepath.Rel(root, file)
			if e != nil {
				return e
			}
			ref = filepath.ToSlash(ref)
			if !allowed[ref] {
				return fail("RECOVERY_FAILED", "目录有后续新增内容，事务整体停止: "+ref)
			}
			return nil
		})
		if e != nil {
			return e
		}
	}
	return nil
}
