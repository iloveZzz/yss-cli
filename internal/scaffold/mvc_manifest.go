package scaffold

import "strings"

func (g *mvcGen) manifest(t *Tree) (*OMap, error) {
	name := fieldString(g.contract, "project_name")
	base := fieldString(g.contract, "base_package")
	application := upperCamel(name) + "Application"
	mainSource := name + "-server/src/main/java/" + strings.ReplaceAll(base, ".", "/") + "/" + application + ".java"

	mode := "按工程输入直接生成；平台和构建尚未验证"
	if !g.o.hasContract() {
		put(t, "README.md", "# "+name+"\n\n平台：Spring Boot "+fieldString(g.platform, "spring_boot_version")+" / Java "+fieldString(g.platform, "java_version")+"。\n\n该工程由 "+mvcSkill+" "+mode+"。模块："+strings.Join(g.modules, "、")+"。不包含业务 API、SQL 或生产数据库绑定。\n\n本地运行需同时显式启用 Maven -Pscaffold-local 和 Spring scaffold-local Profile；测试独立使用 H2。可基于骨架继续开发并接入生产数据库；采用正式 Harness 治理时，由后续已批准存储工作单元接入。")
	} else {
		put(t, "README.md", "# "+name+"\n\n平台：Spring Boot "+fieldString(g.platform, "spring_boot_version")+" / Java "+fieldString(g.platform, "java_version")+"。\n\n该工程由 "+mvcSkill+" 根据批准的 schema v"+fieldString(g.contract, "schema_version")+" 合同生成。模块："+strings.Join(g.modules, "、")+"。不包含业务 API、SQL 或生产数据库绑定。\n\n本地运行需同时显式启用 Maven -Pscaffold-local 和 Spring scaffold-local Profile；测试独立使用 H2。可基于骨架继续开发并接入生产数据库；采用正式 Harness 治理时，由后续已批准存储工作单元接入。")
	}
	files := generatedFiles(t, true)
	fingerprint, err := platformSourceFingerprint(g.src, asString(mustGet(g.contract, "architecture_family")))
	if err != nil {
		return nil, err
	}
	digest, err := sourceTreeDigest(g.src, mvcSkillRoot)
	if err != nil {
		return nil, err
	}
	generator := newMap("id", mvcSkill, "template_digest", "sha256:"+digest)
	ownership := newMap("generated_files", files, "user_owned_globs", userOwnedGlobs)
	if !g.o.hasContract() {
		return standaloneManifest(g.contract, newMap(
			"source_fingerprint", fingerprint,
			"generated_tree_digest", generatedTreeDigest(t),
			"bootstrap_main_class", base+"."+application,
			"bootstrap_main_source", mainSource,
			"generator", generator,
			"ownership", ownership,
		)), nil
	}
	return g.formalManifest(t, fingerprint, generator, ownership, mainSource)
}
