package scaffold

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
)

const standaloneMode = "standalone-generation"

var (
	mavenCoordinate  = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	exactMavenVerion = regexp.MustCompile(`^\d+\.\d+\.\d+(?:-[A-Za-z0-9][A-Za-z0-9.-]*)?$`)
	standaloneName   = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	standalonePkg    = regexp.MustCompile(`^[a-z][a-z0-9]*(?:\.[a-z][a-z0-9]*)*$`)
	javaDigits       = regexp.MustCompile(`^\d+$`)
)

// standaloneModules are the modules of an independent skeleton.
func standaloneModules(family string) []string {
	if family == "domain-driven" {
		return []string{"domain", "application", "infrastructure", "adapter", "bootstrap"}
	}
	return []string{"server", "service", "repository"}
}

// referenceExample mirrors referenceExample(enabled) in scaffold-reference-example.mjs.
func referenceExample(src Source, enabled bool) (*OMap, error) {
	if !enabled {
		return newMap("enabled", false), nil
	}
	digest, err := referenceTemplateDigest(src)
	if err != nil {
		return nil, err
	}
	return newMap("enabled", enabled, "id", "reference-entry", "version", 1, "path", "examples/reference", "template_digest", digest), nil
}

const referenceAssets = ".agents/skills/yss-ddd-scaffold-generator/assets/reference"

func referenceTemplateDigest(src Source) (string, error) {
	hash := sha256.New()
	for _, rel := range orderedPaths(src.Walk(referenceAssets), bytewise) {
		data, err := src.Read(referenceAssets + "/" + rel)
		if err != nil {
			return "", err
		}
		hash.Write([]byte(rel))
		hash.Write([]byte{0})
		hash.Write(data)
		hash.Write([]byte{0})
	}
	wire, err := src.Read(wireRef)
	if err != nil {
		return "", err
	}
	hash.Write([]byte(wireRef))
	hash.Write([]byte{0})
	hash.Write(wire)
	hash.Write([]byte{0})
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

// standaloneConfiguration mirrors standaloneConfiguration(options, family) in
// standalone-backend-scaffold.mjs. It returns the generation input and the
// selected platform profile.
func standaloneConfiguration(src Source, o *Options, family string, example bool) (*OMap, *OMap, error) {
	if !standaloneName.MatchString(o.ProjectName) || !standalonePkg.MatchString(o.BasePackage) {
		return nil, nil, fail("独立生成必须提供合法项目名和 Java 包名")
	}
	required := []struct{ name, value string }{
		{"platformProfile", o.PlatformProfile}, {"springBootVersion", o.SpringBootVersion}, {"javaVersion", o.JavaVersion},
		{"groupId", o.GroupID}, {"projectVersion", o.ProjectVersion}, {"parentGroupId", o.ParentGroupID},
		{"parentArtifactId", o.ParentArtifactID}, {"parentVersion", o.ParentVersion}, {"yssComponentsVersion", o.YssComponentsVersion},
	}
	var missing []string
	for _, item := range required {
		if item.value == "" {
			missing = append(missing, item.name)
		}
	}
	if len(missing) > 0 {
		return nil, nil, failf("独立生成缺少明确输入: %s", strings.Join(missing, ", "))
	}
	catalog, err := loadCatalog(src)
	if err != nil {
		return nil, nil, err
	}
	profile, err := catalog.profile(o.PlatformProfile, o.SpringBootVersion)
	if err != nil {
		return nil, nil, err
	}
	if !javaDigits.MatchString(o.JavaVersion) || !sameNumber(o.JavaVersion, intField(profile, "java_version")) {
		return nil, nil, fail("独立生成要求精确且匹配模板配方的 Spring Boot / Java 平台")
	}
	values := []string{o.GroupID, o.ProjectVersion, o.ParentGroupID, o.ParentArtifactID, o.ParentVersion, o.YssComponentsVersion}
	for _, value := range values {
		if !mavenCoordinate.MatchString(value) {
			return nil, nil, fail("Maven 坐标只能包含字母、数字、点、下划线和连字符")
		}
	}
	for _, value := range []string{o.ProjectVersion, o.ParentVersion, o.YssComponentsVersion} {
		if !exactMavenVerion.MatchString(value) {
			return nil, nil, fail("独立生成必须提供精确 Maven 版本，不能使用 x、latest 或版本范围")
		}
	}
	parentCoordinates := newMap("group_id", o.ParentGroupID, "artifact_id", o.ParentArtifactID, "version", o.ParentVersion)
	coordinates := newMap("group_id", o.GroupID, "project_version", o.ProjectVersion, "parent", parentCoordinates, "yss_components_version", o.YssComponentsVersion)
	ddd := family == "domain-driven"
	architectureProfile, generatorSkill, architecture := "layered-mvc-service", mvcSkill, "layered-mvc"
	if ddd {
		architectureProfile, generatorSkill, architecture = "target-domain-model", dddSkill, "target-domain-model"
	}
	profiles := newMap("architecture", architecture, "persistence", "mybatis-plus", "production_database", "not-bound", "repository", "yss-internal")
	if ddd {
		profiles.Set("dto_placement", "web")
	}
	profiles.Set("platform", mustGet(profile, "id"))
	profiles.Set("validation_namespace", mustGet(profile, "validation_namespace"))
	configuration := newMap(
		"project_name", o.ProjectName,
		"base_package", o.BasePackage,
		"architecture_family", family,
		"architecture_profile", architectureProfile,
		"generator_skill", generatorSkill,
		"maven_coordinates", coordinates,
		"profiles", profiles,
		"module_profile", newMap("resolution_version", 1, "requested_capabilities", []any{}, "resolved_modules", stringsToAny(standaloneModules(family))),
		"platform_configuration", newMap(
			"schema_version", 1,
			"kind", "standalone-platform-configuration",
			"profile_id", mustGet(profile, "id"),
			"spring_boot_version", mustGet(profile, "spring_boot_version"),
			"java_version", mustGet(profile, "java_version"),
			"parent", parentCoordinates,
			"bom", newMap("group_id", "com.yss.cloud", "artifact_id", "yss-components-bom", "version", o.YssComponentsVersion),
			"recipe_digest", platformDigest(newMap("profile", profile, "coordinates", coordinates)),
		),
	)
	reference, err := referenceExample(src, example)
	if err != nil {
		return nil, nil, err
	}
	configuration.Set("reference_example", reference)
	configuration.Set("generation_policy", newMap("mode", "initialize-only", "existing_target", "unsupported", "old_project_migration", "unsupported", "template_upgrade", "unsupported"))
	return configuration, profile, nil
}

func sameNumber(text string, value int) bool {
	n, err := strconv.Atoi(text)
	return err == nil && n == value
}

// standaloneManifest mirrors standaloneManifest(configuration, generated).
func standaloneManifest(configuration, generated *OMap, render *RenderContext) *OMap {
	manifest := NewOMap()
	for _, source := range []*OMap{configuration, generated} {
		for _, key := range source.Keys() {
			manifest.Set(key, mustGet(source, key))
		}
	}
	manifest.Set("schema_version", 2)
	manifest.Set("kind", "standalone-backend-scaffold")
	manifest.Set("generation_mode", standaloneMode)
	manifest.Set("completion_level", "generated")
	manifest.Set("maven_coordinates_source", "user-supplied")
	manifest.Set("generation_input", configuration)
	manifest.Set("input_digest", platformDigest(configuration))
	manifest.Set("generated_at", render.GeneratedAt)
	return manifest
}
