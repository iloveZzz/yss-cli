package scaffold

import (
	"path"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

var (
	placeholderPattern = regexp.MustCompile(`\{\{([a-z_]+)\}\}`)
)

// wireProfile reads the fields the reference example needs from the OpenAPI wire
// profile: the schema version and the ordered common response fields.
func wireProfile(src Source) (schemaVersion any, required []any, properties *OMap, raw []byte, err error) {
	raw, err = src.Read(wireRef)
	if err != nil {
		return
	}
	var document yaml.Node
	if err = yaml.Unmarshal(raw, &document); err != nil || len(document.Content) == 0 {
		return nil, nil, nil, nil, fail("OpenAPI wire profile 无法解析")
	}
	root := document.Content[0]
	if version := mappingValue(root, "schema_version"); version != nil {
		var decoded any
		if err = version.Decode(&decoded); err != nil {
			return
		}
		schemaVersion = decoded
		if n, ok := decoded.(int); ok {
			schemaVersion = n
		}
	}
	fields := mappingValue(mappingValue(root, "common_response"), "fields")
	if fields == nil || fields.Kind != yaml.MappingNode {
		return nil, nil, nil, nil, fail("OpenAPI wire profile 缺少 common_response.fields")
	}
	properties = NewOMap()
	required = []any{}
	for i := 0; i+1 < len(fields.Content); i += 2 {
		key, field := fields.Content[i].Value, fields.Content[i+1]
		if flag := mappingValue(field, "required"); flag != nil {
			var value any
			_ = flag.Decode(&value)
			if truthy(value) {
				required = append(required, key)
			}
		}
		var types []any
		if list := mappingValue(field, "wire_types"); list != nil {
			var decoded any
			if err = list.Decode(&decoded); err != nil {
				return
			}
			if items, ok := decoded.([]any); ok {
				types = items
			}
		}
		if types == nil {
			types = []any{}
		}
		properties.Set(key, newMap("type", types))
	}
	return
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func truthy(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case int:
		return v != 0
	case float64:
		return v != 0
	}
	return true
}

// referenceExample describes the independent teaching reactor of a generation.
type exampleSpec struct {
	enabled      bool
	family       string
	projectName  string
	basePackage  string
	projectVer   string
	platform     *OMap
	renderModule func(prefix, project, base string) error
}

// renderReferenceExample mirrors renderReferenceExample in scaffold-reference-example.mjs.
func renderReferenceExample(src Source, t *Tree, spec exampleSpec) error {
	if !spec.enabled {
		return nil
	}
	project := spec.projectName + "-reference"
	base := spec.basePackage + ".reference"
	ddd := spec.family == "domain-driven"
	referenceRoot := "examples/reference"
	if err := spec.renderModule(referenceRoot, project, base); err != nil {
		return err
	}
	schemaVersion, required, properties, raw, err := wireProfile(src)
	if err != nil {
		return err
	}
	pick := func(domain, mvc string) string {
		if ddd {
			return domain
		}
		return mvc
	}
	vars := map[string]string{
		"project_name":    project,
		"project_version": spec.projectVer,
		"base_package":    base,
		"package_path":    strings.ReplaceAll(base, ".", "/"),
		"wire_profile":    JSON(newMap("ref", wireRef, "schema_version", schemaVersion, "digest", sha256Ref(raw)), ""),
		"result_meta_schema": JSON(newMap(
			"type", "object",
			"required", required,
			"properties", properties,
		), ""),
		"web_module":           pick(project+"-adapter/"+project+"-web", project+"-server"),
		"run_module":           project + "-" + pick("bootstrap", "server"),
		"app_module":           project + "-" + pick("application", "service"),
		"storage_module":       project + "-" + pick("infrastructure", "repository"),
		"app_package":          pick("application", "service"),
		"storage_package":      pick("infrastructure", "repository"),
		"web_package":          pick("adapter.web", "server"),
		"web_path":             pick("adapter/web", "server"),
		"validation_namespace": fieldString(spec.platform, "validation_namespace"),
		"spring_boot_version":  fieldString(spec.platform, "spring_boot_version"),
		"java_version":         fieldString(spec.platform, "java_version"),
	}
	render := func(text string) string {
		return placeholderPattern.ReplaceAllStringFunc(text, func(match string) string {
			if value, ok := vars[placeholderPattern.FindStringSubmatch(match)[1]]; ok {
				return value
			}
			return match
		})
	}
	for _, family := range []string{"common", spec.family} {
		root := referenceAssets + "/" + family
		for _, rel := range orderedPaths(src.Walk(root), bytewise) {
			data, err := src.Read(root + "/" + rel)
			if err != nil {
				return err
			}
			target := strings.TrimSuffix(render(rel), ".template")
			t.Put(referenceRoot+"/"+path.Clean(target), []byte(render(string(data))))
		}
	}
	if !ddd {
		webPom := referenceRoot + "/" + vars["web_module"] + "/pom.xml"
		if !t.Has(webPom) {
			return failf("参考示例缺少文件: %s", webPom)
		}
		text := strings.Replace(string(t.Data(webPom)), "</dependencies>", "<dependency><groupId>com.yss.cloud</groupId><artifactId>yss-component-exception</artifactId></dependency></dependencies>", 1)
		t.Put(webPom, []byte(text))
	}

	return nil
}
