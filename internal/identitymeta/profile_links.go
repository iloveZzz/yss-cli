package identitymeta

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"golang.org/x/text/cases"
)

const ProfileLinksFile = ".yss-profile-links.json"

type ProfileLinks struct {
	SchemaVersion int               `json:"schema_version"`
	Links         map[string]string `json:"links"`
}

func NormalizeProfileRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" || strings.ContainsAny(root, "\x00\r\n") {
		return "", domain.Fail("PATH", "Profile 根目录不可为空或含控制字符")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	for _, part := range strings.Split(filepath.ToSlash(root), "/") {
		if strings.EqualFold(part, ".git") {
			return "", domain.Fail("PROTECTED", "Profile 根目录不得进入 Git 内部目录")
		}
	}
	if _, err = safefs.Path(root, domain.MetadataFile); err != nil {
		return "", err
	}
	return root, nil
}

func ProfileRootsOverlap(a, b string) bool {
	fold := cases.Fold()
	a, b = fold.String(filepath.Clean(a)), fold.String(filepath.Clean(b))
	separator := string(filepath.Separator)
	return a == b || strings.HasPrefix(a, strings.TrimSuffix(b, separator)+separator) || strings.HasPrefix(b, strings.TrimSuffix(a, separator)+separator)
}

func ValidateProfileLinks(root string, links *ProfileLinks) error {
	if links == nil || links.SchemaVersion != 1 || links.Links == nil || len(links.Links) > 3 {
		return domain.Fail("PROFILE_LINKS", "Profile 关联 schema 未知或 links 缺失")
	}
	roots := []string{root}
	for _, name := range []string{"design", "backend", "frontend"} {
		value, ok := links.Links[name]
		if !ok {
			continue
		}
		normalized, err := NormalizeProfileRoot(value)
		if err != nil {
			return err
		}
		if !filepath.IsAbs(value) || normalized != value {
			return domain.Fail("PROFILE_LINKS", "关联必须使用规范绝对路径: "+name)
		}
		for _, prior := range roots {
			if ProfileRootsOverlap(prior, value) {
				return domain.Fail("PROFILE_LINKS", "Profile 根目录不得相同或互相包含")
			}
		}
		roots = append(roots, value)
	}
	for name := range links.Links {
		if name != "design" && name != "backend" && name != "frontend" {
			return domain.Fail("PROFILE_LINKS", "未知目标 Profile: "+name)
		}
	}
	return nil
}

func ReadProfileLinks(root string) (*ProfileLinks, error) {
	root, err := NormalizeProfileRoot(root)
	if err != nil {
		return nil, err
	}
	file, err := safefs.Path(root, ProfileLinksFile)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(file)
	if os.IsNotExist(err) {
		return &ProfileLinks{SchemaVersion: 1, Links: map[string]string{}}, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, domain.Fail("PROFILE_LINKS", "Profile 关联必须是有限大小的普通文件")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var links ProfileLinks
	if err = decodeLinksJSON(raw, &links); err != nil {
		return nil, err
	}
	if err = ValidateProfileLinks(root, &links); err != nil {
		return nil, err
	}
	return &links, nil
}

func decodeLinksJSON(raw []byte, out any) error {
	if !json.Valid(raw) {
		return domain.Fail("PROFILE_LINKS", "Profile 关联必须是单个 JSON 文档")
	}
	if _, err := schema.Parse(raw); err != nil {
		return err
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	return d.Decode(out)
}
