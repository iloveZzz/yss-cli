package scaffold

import (
	"context"
	"sort"
	"strconv"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
)

type CatalogChoice struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Default     bool   `json:"default,omitempty"`
}

type CatalogField struct {
	ID          string   `json:"id"`
	Flag        string   `json:"flag,omitempty"`
	Argument    string   `json:"argument,omitempty"`
	Required    bool     `json:"required"`
	Choices     []string `json:"choices"`
	Operations  []string `json:"operations"`
	Description string   `json:"description"`
}

type CatalogPlatform struct {
	ID                   string   `json:"id"`
	PlatformProfile      string   `json:"platformProfile"`
	SpringBootVersion    string   `json:"springBootVersion"`
	JavaVersion          string   `json:"javaVersion"`
	ParentGroupID        string   `json:"parentGroupId,omitempty"`
	ParentArtifactID     string   `json:"parentArtifactId,omitempty"`
	ParentVersion        string   `json:"parentVersion,omitempty"`
	YssComponentsVersion string   `json:"yssComponentsVersion,omitempty"`
	Architectures        []string `json:"architectures"`
	StandaloneAllowed    bool     `json:"standaloneAllowed"`
	Blockers             []string `json:"blockers"`
}

type CatalogView struct {
	Kind          string            `json:"kind"`
	SchemaVersion int               `json:"schemaVersion"`
	Bundles       []BundleBinding   `json:"bundles"`
	Profiles      []CatalogChoice   `json:"profiles"`
	Installations []CatalogChoice   `json:"installations"`
	Architectures []CatalogChoice   `json:"architectures"`
	Platforms     []CatalogPlatform `json:"platforms"`
	Fields        []CatalogField    `json:"fields"`
}

// DescribeCatalog describes the bundled generation inputs and supported platforms.
func DescribeCatalog(ctx context.Context) (*CatalogView, error) {
	if ctx == nil {
		return nil, domain.Fail("ARGUMENT", "取消上下文不可为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, domain.Wrap("CANCELLED", err)
	}
	view := &CatalogView{Kind: "scaffold-catalog", SchemaVersion: 1, Bundles: []BundleBinding{}, Platforms: []CatalogPlatform{},
		Profiles:      []CatalogChoice{{ID: "spec", Description: "规格及适用后端职责"}, {ID: "backend", Description: "后端交付职责"}},
		Installations: []CatalogChoice{{ID: "project", Description: "安装项目入口、资料和技能", Default: true}, {ID: "skills", Description: "安装规则、技能及其资源闭包"}},
		Architectures: []CatalogChoice{{ID: "ddd", Description: "领域驱动多模块后端"}, {ID: "mvc", Description: "分层 MVC 多模块后端"}}}
	var src Source
	for _, profile := range view.Profiles {
		if err := ctx.Err(); err != nil {
			return nil, domain.Wrap("CANCELLED", err)
		}
		b, err := bundle.Load(profile.ID)
		if err != nil {
			return nil, err
		}
		view.Bundles = append(view.Bundles, BundleBinding{Profile: b.Profile, SchemaVersion: b.SchemaVersion, BundleHash: bundle.Hash(b),
			TemplateVersion: b.TemplateVersion, TemplateCommit: b.TemplateCommit, SourceState: b.SourceState, SnapshotHash: b.SnapshotHash, ManifestHash: b.ManifestHash})
		if profile.ID == "spec" {
			src = newBundleSource(b.Files)
		}
	}
	catalog, err := loadCatalog(src)
	if err != nil {
		return nil, domain.Wrap("CAPABILITY", err)
	}
	for _, profile := range catalog.Profiles {
		if err := ctx.Err(); err != nil {
			return nil, domain.Wrap("CANCELLED", err)
		}
		matched := false
		for _, entry := range catalog.Compatibility {
			if mustGet(entry, "profile_id") != mustGet(profile, "id") || mustGet(entry, "spring_boot_version") != mustGet(profile, "spring_boot_version") {
				continue
			}
			matched = true
			view.Platforms = append(view.Platforms, describeCatalogPlatform(src, profile, entry))
		}
		if !matched {
			platform := catalogPlatformBase(profile)
			platform.Blockers = append(platform.Blockers, "固定目录未提供该平台的完整 YSS Parent/BOM 坐标组合")
			view.Platforms = append(view.Platforms, platform)
		}
	}
	sort.Slice(view.Platforms, func(i, j int) bool { return view.Platforms[i].ID < view.Platforms[j].ID })
	view.Fields = describeCatalogFields(view.Platforms)
	if err := ctx.Err(); err != nil {
		return nil, domain.Wrap("CANCELLED", err)
	}
	return view, nil
}

func catalogPlatformBase(profile *OMap) CatalogPlatform {
	return CatalogPlatform{ID: fieldString(profile, "id") + "@" + fieldString(profile, "spring_boot_version"),
		PlatformProfile: fieldString(profile, "id"), SpringBootVersion: fieldString(profile, "spring_boot_version"),
		JavaVersion: strconv.Itoa(intField(profile, "java_version")), Architectures: []string{},
		Blockers: []string{}}
}

func describeCatalogPlatform(src Source, profile, entry *OMap) CatalogPlatform {
	platform := catalogPlatformBase(profile)
	platform.ID = fieldString(entry, "id")
	parent := asMap(mustGet(entry, "parent"))
	bom := asMap(mustGet(entry, "bom"))
	platform.ParentGroupID = asString(mustGet(parent, "group_id"))
	platform.ParentArtifactID = asString(mustGet(parent, "artifact_id"))
	platform.ParentVersion = asString(mustGet(parent, "version"))
	platform.YssComponentsVersion = asString(mustGet(bom, "version"))
	if asString(mustGet(bom, "group_id")) != "com.yss.cloud" || asString(mustGet(bom, "artifact_id")) != "yss-components-bom" {
		platform.Blockers = append(platform.Blockers, "固定组合的 BOM 不符合独立生成器坐标")
	}
	withoutExample := false
	// Probe syntax only; these project-specific values are never UI defaults.
	options := Options{ProjectName: "catalog-probe", BasePackage: "com.yss.catalog", GroupID: "com.yss", ProjectVersion: "0.0.0",
		PlatformProfile: platform.PlatformProfile, SpringBootVersion: platform.SpringBootVersion, JavaVersion: platform.JavaVersion,
		ParentGroupID: platform.ParentGroupID, ParentArtifactID: platform.ParentArtifactID, ParentVersion: platform.ParentVersion,
		YssComponentsVersion: platform.YssComponentsVersion, WithExample: &withoutExample}
	for _, family := range []struct{ architecture, family string }{{"ddd", "domain-driven"}, {"mvc", "layered-mvc"}} {
		if _, _, err := standaloneConfiguration(src, &options, family.family, false); err != nil {
			platform.Blockers = append(platform.Blockers, family.architecture+": "+err.Error())
		} else {
			platform.Architectures = append(platform.Architectures, family.architecture)
		}
	}
	platform.StandaloneAllowed = len(platform.Blockers) == 0 && len(platform.Architectures) > 0

	return platform
}

func describeCatalogFields(platforms []CatalogPlatform) []CatalogField {
	choices := map[string]bool{}
	for _, platform := range platforms {
		if platform.StandaloneAllowed {
			choices[platform.PlatformProfile] = true
		}
	}
	fields := []CatalogField{
		{ID: "profile", Flag: "--profile", Required: true, Choices: []string{"spec", "backend"}, Operations: []string{"init", "attach"}, Description: "AI 工作资产职责"},
		{ID: "installation", Flag: "--installation", Choices: []string{"project", "skills"}, Operations: []string{"init", "attach"}, Description: "安装规则和技能"},
		{ID: "root", Flag: "--root", Required: true, Choices: []string{}, Operations: []string{"init", "attach"}, Description: "AI 安装的实际目标根目录"},
		{ID: "architecture", Argument: "<ddd|mvc>", Required: true, Choices: []string{"ddd", "mvc"}, Operations: []string{"scaffold"}, Description: "scaffold 的位置参数"},
	}
	for _, item := range []struct{ id, flag, description string }{
		{"projectName", "project-name", "项目名；Java 生成使用 kebab-case"},
		{"basePackage", "base-package", "Java 根包名，由生成器最终校验"},
		{"outputDir", "output-dir", "输出父目录，项目名子目录必须不存在"},
		{"platformProfile", "platform-profile", "从同一固定组合选择，不自由拼接关联版本"},
		{"springBootVersion", "spring-boot-version", "所选组合的精确 Spring Boot 版本"},
		{"javaVersion", "java-version", "所选组合的 Java 主版本"},
		{"groupId", "group-id", "工程 Maven groupId"},
		{"projectVersion", "project-version", "明确的工程 Maven 版本"},
		{"parentGroupId", "parent-group-id", "所选固定 Parent 的 groupId"},
		{"parentArtifactId", "parent-artifact-id", "所选固定 Parent 的 artifactId"},
		{"parentVersion", "parent-version", "所选固定 Parent 的精确声明版本"},
		{"yssComponentsVersion", "yss-components-version", "所选固定 YSS BOM 的精确声明版本"},
	} {
		field := CatalogField{ID: item.id, Flag: "--" + item.flag, Required: true, Choices: []string{}, Operations: []string{"scaffold"}, Description: item.description}
		if item.id == "platformProfile" {
			field.Choices = sortedKeys(choices)
		}
		fields = append(fields, field)
	}
	return fields
}
