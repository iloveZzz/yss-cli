package scaffold

import "context"

func (g *dddGen) configure(ctx context.Context) error {
	configuration, platform, err := standaloneConfiguration(g.src, g.o, "domain-driven", g.o.example())
	if err != nil {
		return err
	}
	g.configuration, g.platform = configuration, platform
	g.coordinates = asMap(mustGet(configuration, "maven_coordinates"))
	return nil
}

func (g *dddGen) referenceEnabled() bool {
	reference := asMap(mustGet(g.configuration, "reference_example"))
	return reference != nil && mustGet(reference, "enabled") == true
}

// renderExample adds the independent reference reactor when the input enables it.
func (g *dddGen) renderExample(t *Tree) error {
	return renderReferenceExample(g.src, t, exampleSpec{
		enabled:     g.referenceEnabled(),
		family:      "domain-driven",
		projectName: g.name,
		basePackage: g.basePackage,
		projectVer:  fieldString(g.coordinates, "project_version"),
		platform:    g.platform,
		renderModule: func(prefix, project, base string) error {
			sample := *g
			sample.name, sample.basePackage = project, base
			sample.setNames()
			return sample.renderMechanical(t, prefix)
		},
	})
}

func (g *dddGen) manifest(t *Tree) (*OMap, error) {
	fingerprint, err := platformSourceFingerprint(g.src, asString(mustGet(g.configuration, "architecture_family")))
	if err != nil {
		return nil, err
	}
	templateDigest, err := sourceTreeDigest(g.src, dddAssets)
	if err != nil {
		return nil, err
	}
	files := generatedFiles(t, false)
	generator := newMap("id", dddSkill, "template_digest", templateDigest)
	ownership := newMap("generated_files", files, "user_owned_globs", userOwnedGlobs)
	return standaloneManifest(g.configuration, newMap(
		"source_fingerprint", fingerprint,
		"generated_tree_digest", generatedTreeDigest(t),
		"bootstrap_main_class", g.basePackage+"."+g.appClass,
		"bootstrap_main_source", g.mainSource,
		"generator", generator,
		"ownership", ownership,
	), g.o.renderContext), nil
}
