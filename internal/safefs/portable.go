package safefs

import (
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

// ValidateRef requires one portable slash-separated project-relative path.
// The same distribution is consumed on Unix and Windows, so Windows device
// names and aliasing syntax are rejected on every platform.
func ValidateRef(ref string) error {
	if ref == "" || !utf8.ValidString(ref) || filepath.IsAbs(ref) || strings.ContainsAny(ref, "\\<>:\"|?*") {
		return domain.Fail("PATH", "路径必须是安全的可移植项目相对路径: "+ref)
	}
	for _, r := range ref {
		if r < 32 || r == 127 {
			return domain.Fail("PATH", "路径含不可移植控制字符")
		}
	}
	for _, part := range strings.Split(ref, "/") {
		if part == "" || part == "." || part == ".." || strings.EqualFold(part, ".git") {
			return domain.Fail("PATH", "拒绝越界或 Git 内部路径: "+ref)
		}
		if strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return domain.Fail("PATH", "路径组件不能以点或空格结尾: "+ref)
		}
		base, _, _ := strings.Cut(part, ".")
		base = strings.ToUpper(strings.TrimRight(base, " "))
		reserved := base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || base == "CONIN$" || base == "CONOUT$"
		if strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT") {
			suffix := strings.TrimPrefix(strings.TrimPrefix(base, "COM"), "LPT")
			if len(suffix) == 1 && suffix[0] >= '1' && suffix[0] <= '9' || suffix == "¹" || suffix == "²" || suffix == "³" {
				reserved = true
			}
		}
		if reserved {
			return domain.Fail("PATH", "拒绝 Windows 保留设备名: "+ref)
		}
	}
	return nil
}

// PathSet binds one spelling to each case-folded path prefix. Identical refs may
// be shared by an operation and its input guard; case variants may not alias.
// Prefix indexing avoids comparing each new path against all previous paths.
type PathSet struct{ spellings map[string]string }

func (s *PathSet) Add(ref string) error {
	if err := ValidateRef(ref); err != nil {
		return err
	}
	if s.spellings == nil {
		s.spellings = map[string]string{}
	}
	prefix := ""
	for _, part := range strings.Split(ref, "/") {
		if prefix != "" {
			prefix += "/"
		}
		prefix += part
		key := strings.Map(func(r rune) rune {
			minimum := r
			for folded := unicode.SimpleFold(r); folded != r; folded = unicode.SimpleFold(folded) {
				if folded < minimum {
					minimum = folded
				}
			}
			return minimum
		}, prefix)
		if prior, ok := s.spellings[key]; ok && prior != prefix {
			return domain.Fail("PATH", "路径存在大小写碰撞: "+prior+" / "+prefix)
		}
		s.spellings[key] = prefix
	}
	return nil
}
