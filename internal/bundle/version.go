package bundle

import (
	_ "embed"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"sync"

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

// 「commit → 版本」对照表：只记录语义化版本发布之前的历史来源，由 tools/version-table.mjs
// 从来源锁历史与内嵌 Bundle 历史一次性生成。之后的实例直接记录语义化版本，不再查表。
//
//go:embed assets/version-table.json
var versionTableJSON []byte

type versionTableFile struct {
	SchemaVersion int `json:"schemaVersion"`
	Entries       []struct {
		Profile        string `json:"profile"`
		TemplateCommit string `json:"templateCommit"`
		Version        string `json:"version"`
	} `json:"entries"`
}

var (
	versionTableOnce sync.Once
	versionTable     map[string]string
	versionTableErr  error
)

func loadVersionTable() {
	var file versionTableFile
	if e := json.Unmarshal(versionTableJSON, &file); e != nil || file.SchemaVersion != 1 {
		versionTableErr = domain.Fail("BUNDLE", "内嵌的版本对照表不可读")
		return
	}
	versionTable = map[string]string{}
	for _, entry := range file.Entries {
		key := entry.Profile + "\x00" + entry.TemplateCommit
		if _, e := domain.GetProfile(entry.Profile); e != nil || !fullCommit.MatchString(entry.TemplateCommit) || !IsSemver(entry.Version) {
			versionTableErr = domain.Fail("BUNDLE", "内嵌的版本对照表含非法条目")
			return
		}
		if previous, dup := versionTable[key]; dup && previous != entry.Version {
			versionTableErr = domain.Fail("BUNDLE", "内嵌的版本对照表对同一来源给出了不同版本")
			return
		}
		versionTable[key] = entry.Version
	}
}

// LookupTemplateVersion 按 Profile 与模板 commit 查历史对照表；查不到返回 ok=false，调用方必须如实报告，不得猜测。
func LookupTemplateVersion(profile, commit string) (string, bool, error) {
	versionTableOnce.Do(loadVersionTable)
	if versionTableErr != nil {
		return "", false, versionTableErr
	}
	version, ok := versionTable[profile+"\x00"+commit]
	return version, ok, nil
}
