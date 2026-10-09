package bundle

import (
	"strings"
	"testing"
)

func TestSkillDocumentedEntryFollowsStaticSharedModuleClosure(t *testing.T) {
	const entry = ".agents/skills/scaffold/scripts/generate.mjs"
	raw := map[string]sourceFile{
		".agents/skills/scaffold/SKILL.md": {data: []byte("Run `node scripts/generate.mjs`.\n")},
		entry:                              {data: []byte(`import './helper.js';`)},
		".agents/skills/scaffold/scripts/helper.js":          {data: []byte(`export {configured} from '../../../../scripts/lib/scaffold-local-database.mjs';`)},
		"scripts/lib/scaffold-local-database.mjs":            {data: []byte(`import './database-policy.mjs'; export const configured=true;`)},
		"scripts/lib/database-policy.mjs":                    {data: []byte(`export const policy='local-only';`)},
		".agents/skills/scaffold/scripts/generator.test.mjs": {data: []byte(`import '../../../../scripts/fixtures/future-product.mjs';`)},
		"scripts/fixtures/future-product.mjs":                {data: []byte(`export const future=true;`)},
	}
	build := func() (Requirement, error) {
		return assetClosure(raw, Policy{}, nil, []string{"scaffold"}, []string{"scaffold"})
	}
	req, err := build()
	if err != nil {
		t.Fatal(err)
	}
	paths := stringSet(req.Paths)
	for _, ref := range []string{entry, ".agents/skills/scaffold/scripts/helper.js", "scripts/lib/scaffold-local-database.mjs", "scripts/lib/database-policy.mjs"} {
		if !paths[ref] {
			t.Fatalf("documented Skill entry omitted static module dependency %s", ref)
		}
	}
	for _, ref := range []string{".agents/skills/scaffold/scripts/generator.test.mjs", "scripts/fixtures/future-product.mjs"} {
		if paths[ref] {
			t.Fatalf("unreferenced tests/future business assets entered closure: %s", ref)
		}
	}
	delete(raw, "scripts/lib/database-policy.mjs")
	if _, err = build(); err == nil || !strings.Contains(err.Error(), "database-policy.mjs") {
		t.Fatalf("missing transitive static module accepted: %v", err)
	}
}

func TestSelectedCrossSkillModuleContinuesStaticClosureAndRejectsEscape(t *testing.T) {
	const module = ".agents/skills/tactical/scripts/validate.mjs"
	raw := map[string]sourceFile{
		"scripts/verify-design":            {data: []byte(`import '../.agents/skills/tactical/scripts/validate.mjs';`)},
		".agents/skills/tactical/SKILL.md": {data: []byte("Tactical design.\n")},
		module:                             {data: []byte(`import '../../../../scripts/lib/current-source.mjs';`)},
		"scripts/lib/current-source.mjs":   {data: []byte(`export const current=true;`)},
	}
	build := func() (Requirement, error) {
		return assetClosure(raw, Policy{Common: Entries{Scripts: []string{"scripts/verify-design"}}}, nil, []string{"tactical"}, nil)
	}
	req, err := build()
	if err != nil {
		t.Fatal(err)
	}
	if !stringSet(req.Paths)["scripts/lib/current-source.mjs"] {
		t.Fatal("selected Skill module import stopped at the Skill boundary")
	}
	// A stage may discover its dependent Skill during the existing closure
	// retry. Its module must still be traversed after that Skill is selected.
	auto, err := assetClosure(raw, Policy{Common: Entries{Scripts: []string{"scripts/verify-design"}}}, nil, nil, nil)
	if err != nil || !stringSet(auto.Skills)["tactical"] || !stringSet(auto.Paths)["scripts/lib/current-source.mjs"] {
		t.Fatalf("discovered Skill dependency stopped at the selected boundary: %+v, %v", auto, err)
	}
	raw[module] = sourceFile{data: []byte(`import '../../../../unregistered/current-source.mjs';`)}
	raw["unregistered/current-source.mjs"] = sourceFile{data: []byte(`export const current=true;`)}
	if _, err = build(); err == nil || !strings.Contains(err.Error(), "未分发") {
		t.Fatalf("cross-boundary unregistered module accepted: %v", err)
	}
}

func TestSkillSharedScriptFallbackAndStaticCycleStayBounded(t *testing.T) {
	raw := map[string]sourceFile{
		".agents/skills/entry/SKILL.md": {data: []byte("Run `scripts/shared-entry`.\n")},
		"scripts/shared-entry":          {data: []byte(`import './lib/shared.mjs';`)},
		"scripts/lib/shared.mjs":        {data: []byte(`import '../shared-entry';`)},
	}
	req, err := assetClosure(raw, Policy{}, nil, []string{"entry"}, []string{"entry"})
	if err != nil || len(req.Paths) != 2 || !stringSet(req.Paths)["scripts/lib/shared.mjs"] {
		t.Fatalf("shared root fallback or module cycle guard regressed: %+v, %v", req, err)
	}
}

func TestSelectedSkillStaticResourceAndLocalSchemaClosure(t *testing.T) {
	const entry = ".agents/skills/design/scripts/validate.mjs"
	const schema = ".agents/skills/design/references/design.schema.json"
	const local = ".agents/skills/design/references/local.schema.json"
	fixture := func() map[string]sourceFile {
		return map[string]sourceFile{
			".agents/skills/design/SKILL.md": {data: []byte("Run `scripts/validate.mjs`.\n")},
			entry:                            {data: []byte(`const schema = new URL('../references/design.schema.json', import.meta.url);`)},
			schema:                           {data: []byte(`{"$ref":"local.schema.json#/$defs/design"}`)},
			local:                            {data: []byte(`{"$defs":{"design":{"type":"object"}}}`)},
			".agents/skills/other/SKILL.md":  {data: []byte("Registered other Skill.\n")},
			".agents/skills/other/references/other.schema.json":      {data: []byte(`{}`)},
			".agents/skills/unselected/SKILL.md":                     {data: []byte("Not selected.\n")},
			".agents/skills/unselected/references/other.schema.json": {data: []byte(`{}`)},
			"unregistered/other.schema.json":                         {data: []byte(`{}`)},
		}
	}
	build := func(raw map[string]sourceFile) (Requirement, error) {
		return assetClosure(raw, Policy{}, nil, []string{"design", "other"}, []string{"design"})
	}
	req, err := build(fixture())
	if err != nil || !stringSet(req.Paths)[schema] || !stringSet(req.Paths)[local] {
		t.Fatalf("selected canonical Skill static resource/local Schema closure omitted: %+v, %v", req, err)
	}
	for _, kind := range []string{"missing", "external-schema", "schema-escape", "other-selected-skill", "other-unselected-skill", "unregistered-root"} {
		t.Run(kind, func(t *testing.T) {
			raw := fixture()
			switch kind {
			case "missing":
				delete(raw, local)
			case "external-schema":
				raw[schema] = sourceFile{data: []byte(`{"$ref":"https://example.invalid/schema.json"}`)}
			case "schema-escape":
				raw[schema] = sourceFile{data: []byte(`{"$ref":"../../other/references/other.schema.json"}`)}
			default:
				target := "../../other/references/other.schema.json"
				if kind == "other-unselected-skill" {
					target = "../../unselected/references/other.schema.json"
				}
				if kind == "unregistered-root" {
					target = "../../../../unregistered/other.schema.json"
				}
				raw[entry] = sourceFile{data: []byte("const schema = new URL('" + target + "', import.meta.url);")}
			}
			if _, err := build(raw); err == nil {
				t.Fatal("missing/offline/ownership boundary accepted")
			}
		})
	}
	// A path underneath .agents/skills is insufficient: ownership also needs
	// both current selection and a canonical Skill registration entry.
	for _, kind := range []string{"source-not-selected", "canonical-entry-missing"} {
		t.Run(kind, func(t *testing.T) {
			raw := fixture()
			selected := []string{"design"}
			if kind == "source-not-selected" {
				selected = nil
			} else {
				delete(raw, ".agents/skills/design/SKILL.md")
			}
			_, err := assetClosure(raw, Policy{Common: Entries{Scripts: []string{entry}}}, nil, selected, nil)
			if err == nil || !strings.Contains(err.Error(), entry) || !strings.Contains(err.Error(), schema) {
				t.Fatalf("unselected/noncanonical ownership accepted or diagnostic lost: %v", err)
			}
		})
	}
}

func TestStaticModuleClosureDistinguishesJavaScriptCommentsAndLiterals(t *testing.T) {
	raw := map[string]sourceFile{
		"scripts/entry.mjs": {data: []byte("/** @param {import('./Expression.js').default} value */\n" +
			"// import './commented.js';\n" +
			"const url='https://example.invalid/*literal*/';\n" +
			"const example=\"import './quoted.js'\";\n" +
			"const pattern=/[/*] import '.\\/regex.js'/;\n" +
			"const template=`import './template-text.js' ${import('./required.mjs')}`;\n" +
			"const literal=import(`./template-required.mjs`);\n" +
			"const schema=new URL('./actual.schema.json', import.meta.url);\n" +
			"import /* quoted example: './comment-only.js' */ './required.mjs';\n" +
			"const literalSchema=new URL(`./template.schema.json`, import.meta.url);\n" +
			"export { value } from './required.mjs';\n")},
		"scripts/required.mjs":          {data: []byte("export const value=1;\n")},
		"scripts/template-required.mjs": {data: []byte("export const template=true;\n")},
		"scripts/actual.schema.json":    {data: []byte(`{"type":"object"}`)},
		"scripts/template.schema.json":  {data: []byte(`{"type":"object"}`)},
	}
	build := func() (Requirement, error) {
		return assetClosure(raw, Policy{Common: Entries{Scripts: []string{"scripts/entry.mjs"}}}, nil, nil, nil)
	}
	req, err := build()
	if err != nil || !stringSet(req.Paths)["scripts/required.mjs"] || !stringSet(req.Paths)["scripts/actual.schema.json"] {
		t.Fatalf("non-executable comment/literal treated as import or real dependencies lost: %+v, %v", req, err)
	}
	if !stringSet(req.Paths)["scripts/template-required.mjs"] || !stringSet(req.Paths)["scripts/template.schema.json"] {
		t.Fatal("static template import/URL was lost")
	}
	delete(raw, "scripts/required.mjs")
	if _, err := build(); err == nil || !strings.Contains(err.Error(), "required.mjs") {
		t.Fatalf("real literal import was silently skipped: %v", err)
	}
}

func TestLiveImportWithOnlyAnInterveningBlockComment(t *testing.T) {
	raw := map[string]sourceFile{
		"scripts/entry.mjs":    {data: []byte("import /* explanatory note */ './required.mjs';\n")},
		"scripts/required.mjs": {data: []byte("export const live=true;\n")},
	}
	build := func() (Requirement, error) {
		return assetClosure(raw, Policy{Common: Entries{Scripts: []string{"scripts/entry.mjs"}}}, nil, nil, nil)
	}
	if req, err := build(); err != nil || !stringSet(req.Paths)["scripts/required.mjs"] {
		t.Fatalf("sole live import with comment was lost: %+v %v", req, err)
	}
	delete(raw, "scripts/required.mjs")
	if _, err := build(); err == nil || !strings.Contains(err.Error(), "required.mjs") {
		t.Fatalf("sole commented import missing dependency was accepted: %v", err)
	}
}

func TestPostfixDivisionKeepsRealStaticImportAndResourceGuard(t *testing.T) {
	const entry = ".agents/skills/entry/scripts/entry.mjs"
	raw := map[string]sourceFile{
		".agents/skills/entry/SKILL.md": {data: []byte("Run `scripts/entry.mjs`.\n")},
		entry:                           {data: []byte("let n=1; n++ / 2; import '../../../../scripts/required.mjs';\n")},
		"scripts/required.mjs":          {data: []byte("export const live=true;\n")},
	}
	build := func() (Requirement, error) {
		return assetClosure(raw, Policy{}, nil, []string{"entry"}, []string{"entry"})
	}
	if req, err := build(); err != nil || !stringSet(req.Paths)["scripts/required.mjs"] {
		t.Fatalf("division after postfix operator concealed a real import: %+v %v", req, err)
	}
	for _, expression := range []string{"n-- / 2", "object.return / 2", "of / 2"} {
		raw[entry] = sourceFile{data: []byte(expression + "; import '../../../../scripts/required.mjs';\n")}
		if req, err := build(); err != nil || !stringSet(req.Paths)["scripts/required.mjs"] {
			t.Fatalf("division expression lost live import (%s): %+v %v", expression, req, err)
		}
	}
	raw[entry] = sourceFile{data: []byte("let n=1; n++ / 2; const schema=new URL('../../../../unregistered/escape.schema.json', import.meta.url);\n")}
	if _, err := build(); err == nil || !strings.Contains(err.Error(), "资源越界") {
		t.Fatalf("division concealed the static resource ownership guard: %v", err)
	}
}

func TestDocumentedSkillStaticSubprocessEntryAndMultilineImports(t *testing.T) {
	const prefix = ".agents/skills/scaffold/"
	raw := map[string]sourceFile{
		prefix + "SKILL.md":             {data: []byte("Run `scripts/workflow.mjs`.\n")},
		prefix + "scripts/workflow.mjs": {data: []byte("const SCRIPT_DIR=path.dirname(fileURLToPath(import.meta.url));\nconst GENERATOR=path.join(SCRIPT_DIR,'generate.mjs');\nexecFile(process.execPath,[GENERATOR,...args]);\n")},
		prefix + "scripts/generate.mjs": {data: []byte("import {\n value\n} from '../../../../scripts/live.mjs';\n")},
		"scripts/live.mjs":              {data: []byte("export const value=true;\n")},
		prefix + "scripts/unused.mjs":   {data: []byte("import '../../../../scripts/future.mjs';\n")},
	}
	build := func() (Requirement, error) {
		return assetClosure(raw, Policy{}, nil, []string{"scaffold"}, []string{"scaffold"})
	}
	if req, err := build(); err != nil || !stringSet(req.Paths)["scripts/live.mjs"] || stringSet(req.Paths)[prefix+"scripts/unused.mjs"] {
		t.Fatalf("static subprocess entry/multiline dependency omitted or unrelated script included: %+v, %v", req, err)
	}
	delete(raw, "scripts/live.mjs")
	if _, err := build(); err == nil || !strings.Contains(err.Error(), "live.mjs") {
		t.Fatalf("missing multiline import accepted: %v", err)
	}
}
