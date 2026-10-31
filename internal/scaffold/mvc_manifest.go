package scaffold

import "strings"

func (g *mvcGen) manifest(t *Tree) (*OMap, error) {
	name := fieldString(g.configuration, "project_name")
	base := fieldString(g.configuration, "base_package")
	application := upperCamel(name) + "Application"
	mainSource := name + "-server/src/main/java/" + strings.ReplaceAll(base, ".", "/") + "/" + application + ".java"

	mode := "按工程输入直接生成；平台和构建尚未验证"
	put(t, "README.md", "# "+name+"\n\n平台：Spring Boot "+fieldString(g.platform, "spring_boot_version")+" / Java "+fieldString(g.platform, "java_version")+"。\n\n该工程由 "+mvcSkill+" "+mode+"。模块："+strings.Join(g.modules, "、")+"。本地运行使用 Maven -Pscaffold-local 和 Spring scaffold-local Profile。")
	files := generatedFiles(t, true)
	fingerprint, err := platformSourceFingerprint(g.src, asString(mustGet(g.configuration, "architecture_family")))
	if err != nil {
		return nil, err
	}
	digest, err := sourceTreeDigest(g.src, mvcSkillRoot)
	if err != nil {
		return nil, err
	}
	generator := newMap("id", mvcSkill, "template_digest", "sha256:"+digest)
	ownership := newMap("generated_files", files, "user_owned_globs", userOwnedGlobs)
	return standaloneManifest(g.configuration, newMap(
		"source_fingerprint", fingerprint,
		"generated_tree_digest", generatedTreeDigest(t),
		"bootstrap_main_class", base+"."+application,
		"bootstrap_main_source", mainSource,
		"generator", generator,
		"ownership", ownership,
	), g.o.renderContext), nil
}
