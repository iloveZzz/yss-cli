package assetmigration

import (
	"embed"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/schema"
)

// Resources are fixed to this executor, never read from the instance being migrated.
//
//go:embed resources/*
var resources embed.FS

type Family struct {
	Current  int               `json:"current"`
	Resident []int             `json:"resident"`
	Schemas  map[string]string `json:"schemas"`
}

func Upgrade(family string, raw []byte, values map[string]any) ([]byte, []schema.Issue, error) {
	var families map[string]Family
	catalog, err := resources.ReadFile("resources/families.json")
	if err != nil {
		return nil, nil, err
	}
	if err = json.Unmarshal(catalog, &families); err != nil {
		return nil, nil, err
	}
	rule, known := families[family]
	if !known {
		return nil, nil, fmt.Errorf("未知资产协议族: %s", family)
	}
	parsed, err := schema.Parse(raw)
	if err != nil {
		return nil, nil, err
	}
	doc, ok := parsed.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("迁移源必须为对象")
	}
	version := fmt.Sprint(doc["schema_version"])
	name, known := rule.Schemas[version]
	if !known {
		return nil, nil, fmt.Errorf("未登记的源协议版本: %s", version)
	}
	resourceRoot, err := filepath.Abs("yss-migration-resources")
	if err != nil {
		return nil, nil, err
	}
	validate := func(name string, value any) ([]schema.Issue, error) {
		return schema.ValidateValueWithReader(filepath.Join(resourceRoot, name), value, func(file string) ([]byte, error) {
			rel, err := filepath.Rel(resourceRoot, file)
			if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
				return nil, fmt.Errorf("非法迁移 Schema 引用")
			}
			return resources.ReadFile("resources/" + filepath.ToSlash(rel))
		})
	}
	issues, err := validate(name, doc)
	if err != nil {
		return nil, nil, err
	}
	if len(issues) > 0 {
		return nil, issues, fmt.Errorf("源资产不满足登记协议")
	}
	target := rule.Schemas[strconv.Itoa(rule.Current)]
	targetRaw, err := resources.ReadFile("resources/" + target)
	if err != nil {
		return nil, nil, err
	}
	var targetSchema map[string]any
	if err = json.Unmarshal(targetRaw, &targetSchema); err != nil {
		return nil, nil, err
	}
	properties, ok := targetSchema["properties"].(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("目标协议缺少属性定义")
	}
	// Historical receipts are evidence, not mutable draft families.
	status, _ := properties["status"].(map[string]any)
	states, _ := status["enum"].([]any)
	draftable := status["const"] == "draft"
	for _, state := range states {
		draftable = draftable || state == "draft"
	}
	if !draftable {
		return nil, nil, fmt.Errorf("此协议不支持草稿迁移")
	}
	out := map[string]any{}
	for key, value := range doc {
		if _, known := properties[key]; known {
			out[key] = value
		}
	}
	for key, value := range values {
		if _, known := properties[key]; !known {
			return nil, nil, fmt.Errorf("目标协议未声明字段: %s", key)
		}
		out[key] = value
	}
	for _, key := range []string{"approval", "approvals", "approval_ref", "approval_refs", "approved_at", "approved_by", "package_export"} {
		delete(out, key)
	}
	out["schema_version"] = rule.Current
	out["status"] = "draft"
	issues, err = validate(target, out)
	if err != nil {
		return nil, nil, err
	}
	data, err := json.MarshalIndent(out, "", "  ")
	return append(data, '\n'), issues, err
}
