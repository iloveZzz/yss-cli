package scaffold

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

var (
	mvcModuleOrder  = []string{"server", "service", "repository", "adapter", "client", "feign-client"}
	mvcCoreModules  = []string{"server", "service", "repository"}
	mvcCapabilities = map[string][]string{
		"external-integration": {"adapter"},
		"published-client":     {"client"},
		"feign-client":         {"client", "feign-client"},
	}
)

// mvcGen carries the resolved inputs of one layered MVC generation.
type mvcGen struct {
	src      Source
	o        *Options
	contract *OMap // standalone configuration or approved scaffold contract
	platform *OMap
	modules  []string
	formal   *formalState
}

func xmlEscape(value string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(value)
}

func orderedModules(requested []string) ([]string, error) {
	set := map[string]bool{}
	for _, module := range mvcCoreModules {
		set[module] = true
	}
	for _, capability := range requested {
		modules, ok := mvcCapabilities[capability]
		if !ok {
			return nil, failf("unsupported MVC capability: %s", capability)
		}
		for _, module := range modules {
			set[module] = true
		}
	}
	var out []string
	for _, module := range mvcModuleOrder {
		if set[module] {
			out = append(out, module)
		}
	}
	return out, nil
}

// put stores a text file, adding the trailing newline the generator guarantees.
func put(t *Tree, path, content string) {
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	t.Put(path, []byte(content))
}

func runMVC(ctx context.Context, src Source, o *Options) (*Result, error) {
	if err := validateMVCOptions(o); err != nil {
		return nil, err
	}
	g := &mvcGen{src: src, o: o}
	final := filepath.Join(o.OutputDir, o.ProjectName)
	gitRoot := findGitRoot(final)
	if gitRoot == "" {
		gitRoot = findGitRoot(o.OutputDir)
	}
	if message := gitSubmoduleScaffoldViolation(gitRoot, o.OutputDir, o.ProjectName); message != "" {
		return nil, fail(message)
	}
	if message := overlayMountViolation(gitRoot, final, true); message != "" {
		return nil, fail(message)
	}
	if _, err := os.Lstat(final); err == nil {
		return nil, failf("unsupported: 目标已存在 %s", final)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if err := g.validateContract(ctx); err != nil {
		return nil, err
	}
	staging, err := stage(o.OutputDir, o.ProjectName)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)
	tree := newTree()
	name := fieldString(g.contract, "project_name")
	base := fieldString(g.contract, "base_package")
	if err = g.renderMechanical(tree, "", g.contract); err != nil {
		return nil, err
	}
	if err = renderReferenceExample(g.src, tree, exampleSpec{
		enabled:     g.referenceEnabled(),
		family:      "layered-mvc",
		projectName: name,
		basePackage: base,
		projectVer:  fieldString(asMap(mustGet(g.contract, "maven_coordinates")), "project_version"),
		platform:    g.platform,
		renderModule: func(prefix, project, sampleBase string) error {
			config := g.contract.Clone()
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
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	manifest, err := g.manifest(tree)
	if err != nil {
		return nil, err
	}
	put(tree, manifestRef, JSON(manifest, "  "))
	if err = checkPlaceholdersMVC(tree); err != nil {
		return nil, err
	}
	root, err := publish(tree, staging, o.OutputDir, o.ProjectName)
	if err != nil {
		return nil, err
	}
	return &Result{ProjectRoot: root, ManifestPath: filepath.Join(root, filepath.FromSlash(manifestRef)), Manifest: manifest, Modules: g.modules}, nil
}

// The MVC generator never scans for leftover placeholders; keep that behaviour.
func checkPlaceholdersMVC(*Tree) error { return nil }

func (g *mvcGen) referenceEnabled() bool {
	reference := asMap(mustGet(g.contract, "reference_example"))
	return reference != nil && mustGet(reference, "enabled") == true
}

// validateMVCOptions mirrors the argument checks of parseArgs in the MVC generator.
func validateMVCOptions(o *Options) error {
	contract := o.hasContract()
	if o.Standalone && contract {
		return fail("--standalone 不得携带合同或批准参数；已有合同须走正式治理路径")
	}
	if contract && o.platformSupplied() {
		return fail("正式模式消费合同平台，不接受独立平台参数")
	}
	required := []struct{ key, value string }{
		{"projectName", o.ProjectName}, {"basePackage", o.BasePackage}, {"outputDir", o.OutputDir},
		{"contractFile", o.ContractFile}, {"contractId", o.ContractID}, {"contractVersion", o.ContractVersion},
		{"approvalRef", o.ApprovalRef}, {"compilerDraftRef", o.CompilerDraftRef}, {"persistedRef", o.PersistedRef},
		{"groupId", o.GroupID}, {"projectVersion", o.ProjectVersion}, {"parentGroupId", o.ParentGroupID},
		{"parentArtifactId", o.ParentArtifactID}, {"parentVersion", o.ParentVersion}, {"yssComponentsVersion", o.YssComponentsVersion},
	}
	for i, item := range required {
		if !contract && i >= 3 && i < 9 {
			continue
		}
		if item.value == "" {
			return failf("缺少必填参数: %s", item.key)
		}
	}
	if !standaloneName.MatchString(o.ProjectName) {
		return fail("--project-name 必须是 kebab-case")
	}
	if !standalonePkg.MatchString(o.BasePackage) {
		return fail("--base-package 不是合法 Java 包名")
	}
	if contract && !positiveInteger(o.ContractVersion) {
		return fail("--contract-version 必须为正整数")
	}
	if o.Force {
		return fail("unsupported: initialize-only 脚手架禁止 --force")
	}
	return nil
}

func (g *mvcGen) validateContract(ctx context.Context) error {
	o := g.o
	if !o.hasContract() {
		configuration, platform, err := standaloneConfiguration(g.src, o, "layered-mvc", o.example())
		if err != nil {
			return err
		}
		g.contract, g.platform = configuration, platform
		g.modules = stringList(mustGet(asMap(mustGet(configuration, "module_profile")), "resolved_modules"))
		return nil
	}
	return g.validateFormalContract(ctx)
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

func importPom(groupID, artifactID, version string) string {
	return strings.Replace(dependency(groupID, artifactID, version, "import"), "</dependency>", "<type>pom</type></dependency>", 1)
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
		dependencies = append(dependencies, own("service"), dependency("org.springframework.boot", fieldString(platform, "web_starter"), "", ""), dependency("org.springframework.boot", "spring-boot-starter-validation", "", ""), dependency("org.springframework.boot", "spring-boot-starter-test", "", "test"), dependency("com.tngtech.archunit", "archunit-junit5", version("archunit"), "test"))
		dependencies = append(dependencies, dependency("org.projectlombok", "lombok", version("lombok"), "test"))
		if fieldString(platform, "web_test_starter") != "spring-boot-starter-test" {
			dependencies = append(dependencies, dependency("org.springframework.boot", fieldString(platform, "web_test_starter"), "", "test"))
		}
		if has("client") {
			dependencies = append(dependencies, own("client"))
		}
		dependencies = append(dependencies, dependency("com.h2database", "h2", "", "test"), dependency("org.mapstruct", "mapstruct", version("mapstruct"), ""), dependency("com.yss.cloud", "yss-component-dto", "", ""))
	case "service":
		dependencies = append(dependencies, own("repository"), dependency("org.springframework", "spring-tx", "", ""), dependency("com.yss.cloud", "yss-component-dto", "", ""), dependency("org.springframework.boot", "spring-boot-starter-test", "", "test"))
		if has("adapter") {
			dependencies = append(dependencies, own("adapter"))
		}
	case "repository":
		dependencies = append(dependencies, dependency("com.yss.cloud", "yss-component-mybatis-plus-starter", "", ""), dependency("org.mapstruct", "mapstruct", version("mapstruct"), ""), dependency("org.springframework.boot", "spring-boot-starter-test", "", "test"), dependency("com.h2database", "h2", "", "test"))
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

// renderMechanical writes the MVC skeleton of contract below prefix of the tree.
func (g *mvcGen) renderMechanical(t *Tree, prefix string, contract *OMap) error {
	name := fieldString(contract, "project_name")
	base := fieldString(contract, "base_package")
	coordinates := asMap(mustGet(contract, "maven_coordinates"))
	profile := asMap(mustGet(contract, "module_profile"))
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
	put(t, at(server+"/src/test/resources/application-scaffold-test.yml"), localDatabaseConfiguration(name+"_test"))
	put(t, at(server+"/src/test/java/"+pkg+"/"+application+"Test.java"), "package "+base+";\n\nimport org.junit.jupiter.api.Test;\nimport org.springframework.boot.test.context.SpringBootTest;\nimport org.springframework.test.context.ActiveProfiles;\n\n@SpringBootTest\n@ActiveProfiles(\"scaffold-test\")\nclass "+application+"Test {\n    @Test\n    void contextLoads() {\n    }\n}")
	rules, err := g.src.Read(mvcSkillRoot + "/assets/templates/architecture-rules-test.java.template")
	if err != nil {
		return err
	}
	put(t, at(server+"/src/test/java/"+pkg+"/architecture/LayeredMvcArchitectureTest.java"), strings.ReplaceAll(string(rules), "{{base_package}}", base))
	put(t, at(server+"/src/test/java/"+pkg+"/PlatformIntegrationTest.java"), platformSmokeTest(base, g.platform, stringList(mustGet(profile, "requested_capabilities"))))
	return copyWrapper(g.src, t, prefix, true)
}

const mvcSkillRoot = ".agents/skills/" + mvcSkill
