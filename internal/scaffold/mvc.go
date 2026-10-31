package scaffold

import (
	"context"
	"strings"
)

// mvcGen carries the resolved inputs of one layered MVC generation.
type mvcGen struct {
	src           Source
	o             *Options
	configuration *OMap // generation configuration
	platform      *OMap
	modules       []string
}

func xmlEscape(value string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(value)
}

// put stores a text file, adding the trailing newline the generator guarantees.
func put(t *Tree, path, content string) {
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	t.Put(path, []byte(content))
}

func renderMVC(ctx context.Context, src Source, o *Options) (*renderedProject, error) {
	if err := validateMVCOptions(o); err != nil {
		return nil, err
	}
	g := &mvcGen{src: src, o: o}
	if err := guardTarget(o); err != nil {
		return nil, err
	}
	if err := g.configure(ctx); err != nil {
		return nil, err
	}
	tree := newTree()
	name := fieldString(g.configuration, "project_name")
	base := fieldString(g.configuration, "base_package")
	if err := g.renderMechanical(tree, "", g.configuration); err != nil {
		return nil, err
	}
	if err := renderReferenceExample(g.src, tree, exampleSpec{
		enabled:     g.referenceEnabled(),
		family:      "layered-mvc",
		projectName: name,
		basePackage: base,
		projectVer:  fieldString(asMap(mustGet(g.configuration, "maven_coordinates")), "project_version"),
		platform:    g.platform,
		renderModule: func(prefix, project, sampleBase string) error {
			config := g.configuration.Clone()
			config.Set("project_name", project)
			config.Set("base_package", sampleBase)
			config.Set("module_profile", newMap(
				"resolution_version", 1,
				"requested_capabilities", []any{},
				"resolved_modules", stringsToAny(standaloneModules("layered-mvc")),
			))
			return g.renderMechanical(tree, prefix, config)
		},
	}); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	manifest, err := g.manifest(tree)
	if err != nil {
		return nil, err
	}
	put(tree, manifestRef, JSON(manifest, "  "))
	if err := checkPlaceholders(tree); err != nil {
		return nil, err
	}
	return &renderedProject{tree: tree, manifest: manifest, modules: g.modules}, nil
}

func (g *mvcGen) referenceEnabled() bool {
	reference := asMap(mustGet(g.configuration, "reference_example"))
	return reference != nil && mustGet(reference, "enabled") == true
}

// validateMVCOptions mirrors the argument checks of parseArgs in the MVC generator.
func validateMVCOptions(o *Options) error {
	if err := validateDDDOptions(o); err != nil {
		return err
	}
	if !standaloneName.MatchString(o.ProjectName) {
		return fail("--project-name 必须是 kebab-case")
	}
	if !standalonePkg.MatchString(o.BasePackage) {
		return fail("--base-package 不是合法 Java 包名")
	}
	if o.Force {
		return fail("unsupported: initialize-only 脚手架禁止 --force")
	}
	return nil
}

func (g *mvcGen) configure(ctx context.Context) error {
	o := g.o
	configuration, platform, err := standaloneConfiguration(g.src, o, "layered-mvc", o.example())
	if err != nil {
		return err
	}
	g.configuration, g.platform = configuration, platform
	g.modules = stringList(mustGet(asMap(mustGet(configuration, "module_profile")), "resolved_modules"))
	return nil
}

// dependency renders one <dependency> element.
func dependency(groupID, artifactID, version, scope string) string {
	out := "<dependency><groupId>" + xmlEscape(groupID) + "</groupId><artifactId>" + xmlEscape(artifactID) + "</artifactId>"
	if version != "" {
		out += "<version>" + xmlEscape(version) + "</version>"
	}
	if scope != "" {
		out += "<scope>" + scope + "</scope>"
	}
	return out + "</dependency>"
}

func modulePom(name string, coordinates *OMap, module string, modules []string, platform *OMap) string {
	groupID := fieldString(coordinates, "group_id")
	versions := asMap(mustGet(platform, "versions"))
	version := func(key string) string { return fieldString(versions, key) }
	own := func(part string) string { return dependency(groupID, name+"-"+part, "${project.version}", "") }
	has := func(module string) bool {
		for _, item := range modules {
			if item == module {
				return true
			}
		}
		return false
	}
	var dependencies []string
	switch module {
	case "server":
		dependencies = append(dependencies, own("service"), dependency("org.springframework.boot", fieldString(platform, "web_starter"), "", ""), dependency("org.springframework.boot", "spring-boot-starter-validation", "", ""))
		if has("client") {
			dependencies = append(dependencies, own("client"))
		}
		dependencies = append(dependencies, dependency("org.mapstruct", "mapstruct", version("mapstruct"), ""), dependency("com.yss.cloud", "yss-component-dto", "", ""))
	case "service":
		dependencies = append(dependencies, own("repository"), dependency("org.springframework", "spring-tx", "", ""), dependency("com.yss.cloud", "yss-component-dto", "", ""))
		if has("adapter") {
			dependencies = append(dependencies, own("adapter"))
		}
	case "repository":
		dependencies = append(dependencies, dependency("com.yss.cloud", "yss-component-mybatis-plus-starter", "", ""), dependency("org.mapstruct", "mapstruct", version("mapstruct"), ""))
	case "adapter":
		dependencies = append(dependencies, dependency("org.springframework", "spring-context", "", ""))
		if has("feign-client") {
			dependencies = append(dependencies, own("feign-client"))
		}
	case "client":
		dependencies = append(dependencies, dependency("com.yss.cloud", "yss-component-dto", "", ""), dependency(fieldString(platform, "validation_group"), fieldString(platform, "validation_artifact"), "", ""))
	}
	switch module {
	case "server", "service", "core", "client", "repository":
		dependencies = append(dependencies, dependency("org.projectlombok", "lombok", version("lombok"), "provided"))
	case "feign-client":
		dependencies = append(dependencies, own("client"), dependency("org.springframework.cloud", "spring-cloud-starter-openfeign", "", ""))
	}
	plugin := ""
	if module == "server" {
		plugin = "<profiles><profile><id>scaffold-local</id><dependencies>" + dependency("com.h2database", "h2", "", "runtime") + "</dependencies></profile></profiles><build><plugins><plugin><groupId>org.springframework.boot</groupId><artifactId>spring-boot-maven-plugin</artifactId><configuration><skip>false</skip></configuration></plugin></plugins></build>"
	}
	return "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<project xmlns=\"http://maven.apache.org/POM/4.0.0\"><modelVersion>4.0.0</modelVersion><parent><groupId>" + xmlEscape(groupID) + "</groupId><artifactId>" + xmlEscape(name) + "</artifactId><version>" + xmlEscape(fieldString(coordinates, "project_version")) + "</version></parent><artifactId>" + xmlEscape(name) + "-" + module + "</artifactId><dependencies>" + strings.Join(dependencies, "") + "</dependencies>" + plugin + "</project>"
}

// renderMechanical writes the MVC skeleton of configuration below prefix of the tree.
func (g *mvcGen) renderMechanical(t *Tree, prefix string, configuration *OMap) error {
	name := fieldString(configuration, "project_name")
	base := fieldString(configuration, "base_package")
	coordinates := asMap(mustGet(configuration, "maven_coordinates"))
	profile := asMap(mustGet(configuration, "module_profile"))
	modules := stringList(mustGet(profile, "resolved_modules"))
	pkg := strings.ReplaceAll(base, ".", "/")
	at := func(rel string) string { return joinPath(prefix, rel) }

	t.Mkdir(prefix)
	put(t, at("pom.xml"), parentPom(name, coordinates, modules, g.platform))
	for _, module := range modules {
		root := name + "-" + module
		put(t, at(root+"/pom.xml"), modulePom(name, coordinates, module, modules, g.platform))
		put(t, at(root+"/src/main/java/"+pkg+"/"+strings.ReplaceAll(module, "-", "/")+"/package-info.java"), "/** "+module+" mechanical package boundary. */\npackage "+base+"."+strings.ReplaceAll(module, "-", ".")+";")
	}
	application := upperCamel(name) + "Application"
	server := name + "-server"
	put(t, at(server+"/src/main/java/"+pkg+"/"+application+".java"), "package "+base+";\n\nimport org.springframework.boot.SpringApplication;\nimport org.springframework.boot.autoconfigure.SpringBootApplication;\n\n/** Mechanical Spring Boot entrypoint. */\n@SpringBootApplication\npublic class "+application+" {\n    public static void main(String[] args) {\n        SpringApplication.run("+application+".class, args);\n    }\n}")
	put(t, at(server+"/src/main/resources/application.yml"), "spring:\n  application:\n    name: "+name)
	put(t, at(server+"/src/main/resources/application-scaffold-local.yml"), localDatabaseConfiguration(name))
	return copyWrapper(g.src, t, prefix, true)
}

const mvcSkillRoot = ".agents/skills/" + mvcSkill
