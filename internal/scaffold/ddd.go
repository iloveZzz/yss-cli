package scaffold

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

const (
	dddSkillRoot = ".agents/skills/yss-ddd-scaffold-generator"
	dddAssets    = dddSkillRoot + "/assets"
)

var userOwnedGlobs = []any{"**/src/main/java/**", "**/src/test/java/**", "db/**"}

// dddGen carries the resolved inputs of one DDD generation.
type dddGen struct {
	src           Source
	o             *Options
	name          string
	basePackage   string
	author, date  string
	appClass      string
	mainSource    string
	contract      *OMap // standalone configuration or approved scaffold contract
	platform      *OMap
	coordinates   *OMap
	coordinateSrc string
	profiles      *OMap
	formal        *formalState
	standalone    bool
}

func newDDDGen(src Source, o *Options) *dddGen {
	g := &dddGen{src: src, o: o, name: o.ProjectName, basePackage: o.BasePackage, standalone: !o.hasContract()}
	g.author = os.Getenv("USER")
	if g.author == "" {
		g.author = "yss-team"
	}
	g.date = isoNow()[:10]
	g.setNames()
	return g
}

func (g *dddGen) setNames() {
	g.appClass = upperCamel(g.name) + "Application"
	g.mainSource = g.name + "-bootstrap/src/main/java/" + strings.ReplaceAll(g.basePackage, ".", "/") + "/" + g.appClass + ".java"
}

func upperCamel(value string) string {
	parts := strings.Split(value, "-")
	for i, part := range parts {
		if part != "" {
			parts[i] = strings.ToUpper(part[:1]) + part[1:]
		}
	}
	return strings.Join(parts, "")
}

func runDDD(ctx context.Context, src Source, o *Options) (*Result, error) {
	if err := validateDDDOptions(o); err != nil {
		return nil, err
	}
	g := newDDDGen(src, o)
	final := filepath.Join(o.OutputDir, g.name)
	gitRoot := findGitRoot(final)
	if gitRoot == "" {
		gitRoot = findGitRoot(o.OutputDir)
	}
	if message := gitSubmoduleScaffoldViolation(gitRoot, o.OutputDir, g.name); message != "" {
		return nil, fail(message)
	}
	if message := overlayMountViolation(gitRoot, final, true); message != "" {
		return nil, fail(message)
	}
	if err := g.validateContractMetadata(ctx); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(final); err == nil {
		if message := overlayMountViolation(gitRoot, final, true); message != "" {
			return nil, fail(message)
		}
		return nil, failf("unsupported: initialize-only 脚手架只创建全新项目，拒绝已存在目标: %s", final)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	staging, err := stage(o.OutputDir, g.name)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)
	tree := newTree()
	if err = g.renderMechanical(tree, ""); err != nil {
		return nil, err
	}
	if err = g.renderExample(tree); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	manifest, err := g.manifest(tree)
	if err != nil {
		return nil, err
	}
	tree.Put(manifestRef, []byte(JSON(manifest, "  ")+"\n"))
	if err = g.validateGenerated(tree); err != nil {
		return nil, err
	}
	root, err := publish(tree, staging, o.OutputDir, g.name)
	if err != nil {
		return nil, err
	}
	return &Result{ProjectRoot: root, ManifestPath: filepath.Join(root, filepath.FromSlash(manifestRef)), Manifest: manifest, Modules: standaloneModules("domain-driven")}, nil
}

// validateDDDOptions mirrors the argument checks of parseArgs in the DDD generator.
func validateDDDOptions(o *Options) error {
	contract := o.hasContract()
	if o.Standalone && contract {
		return fail("--standalone 不得携带合同或批准参数；已有合同须走正式治理路径")
	}
	if contract && o.platformSupplied() {
		return fail("正式模式消费合同平台，不接受独立平台参数")
	}
	for _, item := range []struct{ flag, value string }{{"--project-name", o.ProjectName}, {"--base-package", o.BasePackage}, {"--output-dir", o.OutputDir}} {
		if item.value == "" {
			return failf("缺少必填参数: %s", item.flag)
		}
	}
	if contract && o.ContractFile == "" {
		return fail("缺少必填参数: --contract-file")
	}
	if !standaloneName.MatchString(o.ProjectName) {
		return fail("项目名称必须是 kebab-case 格式 (例如: user-service)")
	}
	if !standalonePkg.MatchString(o.BasePackage) {
		return fail("包名格式不正确 (例如: com.yss.user)")
	}
	if o.ContractVersion != "" && !positiveInteger(o.ContractVersion) {
		return fail("--contract-version(必须为正整数)")
	}
	if o.Force {
		return fail("unsupported: initialize-only 脚手架禁止 --force，也不承担旧项目迁移或模板升级")
	}
	return nil
}

func positiveInteger(text string) bool {
	if !javaDigits.MatchString(text) {
		return false
	}
	trimmed := strings.TrimLeft(text, "0")
	return trimmed != ""
}

func (g *dddGen) templateVars() *OMap {
	vars := platformTemplateVars(g.platform)
	vars.Set("project_name", g.name)
	vars.Set("application_class_name", g.appClass)
	vars.Set("base_package", g.basePackage)
	vars.Set("group_id", mustGet(g.coordinates, "group_id"))
	vars.Set("project_version", mustGet(g.coordinates, "project_version"))
	parent := asMap(mustGet(g.coordinates, "parent"))
	vars.Set("parent_group_id", mustGet(parent, "group_id"))
	vars.Set("parent_artifact_id", mustGet(parent, "artifact_id"))
	vars.Set("parent_version", mustGet(parent, "version"))
	vars.Set("yss_components_version", mustGet(g.coordinates, "yss_components_version"))
	vars.Set("project_description", g.name+" service")
	vars.Set("author", g.author)
	vars.Set("date", g.date)
	vars.Set("db_dependency", "")
	return vars
}

func (g *dddGen) renderTemplate(ref string) ([]byte, error) {
	if !g.src.Has(ref) {
		return nil, failf("模板文件不存在: %s", ref)
	}
	data, err := g.src.Read(ref)
	if err != nil {
		return nil, err
	}
	return []byte(replaceVars(string(data), g.templateVars())), nil
}

func (g *dddGen) render(text string) string { return replaceVars(text, g.templateVars()) }

func joinPath(prefix, rel string) string {
	if prefix == "" {
		return rel
	}
	return prefix + "/" + rel
}

// renderMechanical writes the DDD skeleton below prefix of tree.
func (g *dddGen) renderMechanical(t *Tree, prefix string) error {
	pkg := strings.ReplaceAll(g.basePackage, ".", "/")
	for _, module := range standaloneModules("domain-driven") {
		root := joinPath(prefix, g.name+"-"+module)
		t.Mkdir(root + "/src/main/java/" + pkg)
		t.Mkdir(root + "/src/main/resources")
		t.Mkdir(root + "/src/test/java/" + pkg)
	}
	t.Mkdir(joinPath(prefix, g.name+"-adapter/"+g.name+"-web/src/main/java/"+pkg+"/rest"))
	t.Mkdir(joinPath(prefix, "db"))

	poms := dddAssets + "/templates/pom/"
	web := g.name + "-adapter/" + g.name + "-web"
	for _, item := range []struct{ template, target string }{
		{"parent-pom.xml.template", "pom.xml"},
		{"domain-pom.xml.template", g.name + "-domain/pom.xml"},
		{"application-pom.xml.template", g.name + "-application/pom.xml"},
		{"infrastructure-pom.xml.template", g.name + "-infrastructure/pom.xml"},
		{"adapter-pom.xml.template", g.name + "-adapter/pom.xml"},
		{"web-pom.xml.template", web + "/pom.xml"},
		{"bootstrap-pom.xml.template", g.name + "-bootstrap/pom.xml"},
	} {
		data, err := g.renderTemplate(poms + item.template)
		if err != nil {
			return err
		}
		t.Put(joinPath(prefix, item.target), data)
	}

	main, err := g.renderTemplate(dddAssets + "/templates/java/bootstrap-application.java.template")
	if err != nil {
		return err
	}
	t.Put(joinPath(prefix, g.mainSource), main)
	t.Put(joinPath(prefix, g.name+"-bootstrap/src/test/java/"+pkg+"/PlatformIntegrationTest.java"), []byte(platformSmokeTest(g.basePackage, g.platform, nil)))

	rules, err := g.renderTemplate(dddAssets + "/templates/java/architecture-rules-test.java.template")
	if err != nil {
		return err
	}
	t.Put(joinPath(prefix, g.name+"-bootstrap/src/test/java/"+pkg+"/ArchitectureRulesTest.java"), rules)

	resources := g.name + "-bootstrap/src/main/resources/"
	for _, item := range [][2]string{{"application.yml.template", "application.yml"}, {"logback-spring.xml.template", "logback-spring.xml"}} {
		data, err := g.renderTemplate(dddAssets + "/templates/config/" + item[0])
		if err != nil {
			return err
		}
		t.Put(joinPath(prefix, resources+item[1]), data)
	}
	t.Put(joinPath(prefix, resources+"application-scaffold-local.yml"), []byte(localDatabaseConfiguration(g.name)))
	t.Put(joinPath(prefix, g.name+"-bootstrap/src/test/resources/application-scaffold-test.yml"), []byte(localDatabaseConfiguration(g.name+"_test")))

	readme := "# {{project_name}}\n\n平台：Spring Boot {{boot_version}} / Java {{java_version}}。\n\n## 模块说明\n\n- {{project_name}}-domain\n- {{project_name}}-application\n- {{project_name}}-infrastructure\n- {{project_name}}-adapter\n- {{project_name}}-bootstrap\n\n可基于骨架继续开发业务 API、领域模型、数据结构和权限行为。接入正式 Harness 治理后，按当前批准的 Slice Implementation Contract 和对应 YSS skill 逐切片实现。\n\n## 快速开始\n\n```bash\ncd {{project_name}}\n./mvnw clean compile\n./mvnw -Pscaffold-local spring-boot:run -pl {{project_name}}-bootstrap -Dspring-boot.run.profiles=scaffold-local\n```\n"
	if g.standalone {
		readme = "按工程输入直接生成的开发脚手架；构建尚未验证。\n\n" + readme
	}
	t.Put(joinPath(prefix, "README.md"), []byte(g.render(readme)))

	return copyWrapper(g.src, t, prefix, false)
}

// copyWrapper adds the Maven Wrapper from the DDD skill assets, keeping the
// permission bits recorded in the Bundle.
func copyWrapper(src Source, t *Tree, prefix string, forceExecutable bool) error {
	wrapper := dddAssets + "/wrapper"
	for _, rel := range src.Walk(wrapper) {
		data, err := src.Read(wrapper + "/" + rel)
		if err != nil {
			return err
		}
		mode, _ := src.Mode(wrapper + "/" + rel)
		if rel == "mvnw" && forceExecutable {
			mode = executableMode
		}
		for dir := parent(rel); dir != ""; dir = parent(dir) {
			t.MkdirFixed(joinPath(prefix, dir), 0o755)
		}
		t.PutFixed(joinPath(prefix, rel), data, mode&0o777)
	}
	return nil
}

func localDatabaseConfiguration(name string) string {
	return "# 仅通过 scaffold-local 显式启用；不用于生产。\nspring:\n  datasource:\n    url: jdbc:h2:mem:" + strings.ReplaceAll(name, "-", "_") + ";DB_CLOSE_DELAY=-1\n    username: sa\n    password: \"\"\n    driver-class-name: org.h2.Driver\n  sql:\n    init:\n      mode: never\n  cloud:\n    discovery:\n      enabled: false\n    service-registry:\n      auto-registration:\n        enabled: false\n    nacos:\n      discovery:\n        enabled: false\n      config:\n        enabled: false\n  cache:\n    type: none\n"
}

func (g *dddGen) validateGenerated(t *Tree) error {
	for _, ref := range []string{"pom.xml", g.name + "-bootstrap/pom.xml", g.mainSource, "mvnw", manifestRef} {
		if !t.Has(ref) {
			return failf("生成产物缺失: %s", ref)
		}
	}
	return checkPlaceholders(t)
}
