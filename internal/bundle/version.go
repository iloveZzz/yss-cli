package bundle

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

// 迁移规则按模板版本区间匹配（architecture-iteration-v1 FR-003）。
// 版本比较遵循 SemVer 2.0 的优先级规则，不接受构建元数据。

var semverPattern = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-((?:0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*))*))?$`)

type semver struct {
	core [3]uint64
	pre  []string
}

func parseSemver(s string) (semver, bool) {
	m := semverPattern.FindStringSubmatch(s)
	if m == nil {
		return semver{}, false
	}
	var v semver
	for i := 0; i < 3; i++ {
		n, e := strconv.ParseUint(m[i+1], 10, 64)
		if e != nil {
			return semver{}, false
		}
		v.core[i] = n
	}
	if m[4] != "" {
		v.pre = strings.Split(m[4], ".")
	}
	return v, true
}

// IsSemver 判断字符串是否为不带构建元数据的语义化版本。
func IsSemver(s string) bool {
	_, ok := parseSemver(s)
	return ok
}

func numeric(s string) (uint64, bool) {
	n, e := strconv.ParseUint(s, 10, 64)
	return n, e == nil && !(len(s) > 1 && s[0] == '0')
}

// compare 返回 -1、0、1。没有预发布标识的版本高于同核心版本的预发布版本。
func (a semver) compare(b semver) int {
	for i := 0; i < 3; i++ {
		if a.core[i] != b.core[i] {
			if a.core[i] < b.core[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a.pre) == 0 && len(b.pre) == 0:
		return 0
	case len(a.pre) == 0:
		return 1
	case len(b.pre) == 0:
		return -1
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		x, y := a.pre[i], b.pre[i]
		nx, ox := numeric(x)
		ny, oy := numeric(y)
		switch {
		case ox && oy:
			if nx != ny {
				if nx < ny {
					return -1
				}
				return 1
			}
		case ox:
			return -1
		case oy:
			return 1
		default:
			if x != y {
				if x < y {
					return -1
				}
				return 1
			}
		}
	}
	switch {
	case len(a.pre) < len(b.pre):
		return -1
	case len(a.pre) > len(b.pre):
		return 1
	}
	return 0
}

// VersionRange 是左闭右开区间：Min 含，Max 不含。两端至少给一端。
type VersionRange struct {
	Min string `json:"min,omitempty"`
	Max string `json:"max,omitempty"`
}

func (r VersionRange) bounds() (lo, hi semver, hasLo, hasHi, ok bool) {
	ok = r.Min != "" || r.Max != ""
	if r.Min != "" {
		lo, hasLo = parseSemver(r.Min)
		ok = ok && hasLo
	}
	if r.Max != "" {
		hi, hasHi = parseSemver(r.Max)
		ok = ok && hasHi
	}
	if hasLo && hasHi && lo.compare(hi) >= 0 {
		ok = false
	}
	return
}

func (r VersionRange) Validate() error {
	if _, _, _, _, ok := r.bounds(); !ok {
		return domain.Fail("BUNDLE_RULE", "版本区间必须是左闭右开的语义化版本，且 min 小于 max")
	}
	return nil
}

// Contains 在版本不是合法语义化版本时返回 false，不做猜测。
func (r VersionRange) Contains(version string) bool {
	v, ok := parseSemver(version)
	lo, hi, hasLo, hasHi, valid := r.bounds()
	if !ok || !valid {
		return false
	}
	return (!hasLo || v.compare(lo) >= 0) && (!hasHi || v.compare(hi) < 0)
}

// overlaps 判断两个合法区间是否有公共版本。
func (r VersionRange) overlaps(o VersionRange) bool {
	aLo, aHi, aHasLo, aHasHi, aOK := r.bounds()
	bLo, bHi, bHasLo, bHasHi, bOK := o.bounds()
	if !aOK || !bOK {
		return true
	}
	// a 的上界不高于 b 的下界，或 b 的上界不高于 a 的下界，则不相交。
	if aHasHi && bHasLo && aHi.compare(bLo) <= 0 {
		return false
	}
	if bHasHi && aHasLo && bHi.compare(aLo) <= 0 {
		return false
	}
	return true
}

// verifyTemplateTag 校验来源锁里的模板 tag（architecture-iteration-v1 FR-001）：tag 必须是 vX.Y.Z 形式的语义化版本，
// 并且指向锁里的 templateCommit；templateVersion 写成不带 v 的版本号，使实例记录语义化的模板版本。
// 没有 tag 的来源保持原样（git:<commit>），所以发布第一个模板 tag 之前的锁照常可用。
func verifyTemplateTag(ctx context.Context, root string, s *Source) error {
	if s.TemplateTag == "" {
		return nil
	}
	if !strings.HasPrefix(s.TemplateTag, "v") || !IsSemver(s.TemplateTag[1:]) || strings.Contains(s.TemplateTag, "-") {
		return domain.Fail("BUNDLE_RULE", "templateTag 必须是 vX.Y.Z 形式的正式语义化版本: "+s.TemplateTag)
	}
	out, e := git(ctx, root, "rev-parse", "--verify", "--quiet", "refs/tags/"+s.TemplateTag+"^{commit}")
	if e != nil {
		return domain.Fail("BUNDLE_RULE", "模板仓库里没有 tag "+s.TemplateTag)
	}
	if strings.TrimSpace(string(out)) != s.Commit {
		return domain.Fail("BUNDLE_RULE", "tag "+s.TemplateTag+" 指向 "+strings.TrimSpace(string(out))+"，与来源锁的 templateCommit "+s.Commit+" 不一致")
	}
	version := s.TemplateTag[1:]
	if s.TemplateVersion != "" && s.TemplateVersion != version {
		return domain.Fail("BUNDLE_RULE", "templateVersion "+s.TemplateVersion+" 与 tag "+s.TemplateTag+" 不一致")
	}
	s.TemplateVersion = version
	return nil
}
