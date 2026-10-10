package bundle

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
)

const projectToken = "__YSS_PROJECT_NAME__"
const domainToken = "__YSS_BUSINESS_DOMAIN__"
const teamToken = "__YSS_TEAM_SIZE__"
const skillPreflight = "\n\n专项技能调用前，运行 `scripts/query-lifecycle-context --work-unit <当前工作单元> --check-skills`；多运行时用 `--agent-runtime`，已确认条件用 `--when`。未安装技能先核对结果中的补装计划，在既有任务授权内应用后重验；漂移、冲突和版本不匹配暂停当前调用，不自动覆盖或迁移。\n"

func renderSource(profile, ref string, data []byte, selected bool, skills []string) ([]byte, error) {
	if bytes.IndexByte(data, 0) >= 0 {
		return data, nil
	}
	prepared, err := prepareSource(profile, ref, data)
	if err != nil {
		return nil, err
	}
	text := string(prepared)
	if profile != "spec" {
		switch ref {
		case "yss-project.yaml":
			text = "schema_version: 1\nrepository_mode: project-instance\n"
		case "README.md":
			id := map[string]string{"design": "harness.business-ddd-strategy-handoff", "backend": "harness.backend-delivery", "frontend": "harness.frontend-delivery"}[profile]
			text = "# " + projectToken + "\n\n本仓是 `" + id + "` 的 `project-instance`。\n\n先读 [AGENTS.md](AGENTS.md)、[CONTEXT.md](CONTEXT.md) 与 [profile](.template-spec/process/harness-profile.yaml)。\n\n业务领域：" + domainToken + "\n团队规模：" + teamToken + "\n"
		case "AGENTS.md":
			text = renderLabels(text)
		}
		return []byte(text), nil
	}
	switch ref {
	case "skills-lock.json":
		if selected {
			return selectedSourceLock(data, skills)
		}
	case "scripts/lib/skill-supply-chain.mjs":
		if selected {
			marker := `export const PROJECTION_ROOTS = [".codex/skills", ".cursor/skills", ".pi/skills"];`
			if !strings.Contains(text, marker) {
				return nil, fmt.Errorf("Skill 投影脚本分发基线变化")
			}
			addition := `
function projectionRootsFor(lock) {
  const identityPath = path.join(ROOT, "yss-project.yaml");
  const isInstance = existsSync(identityPath) && /^repository_mode:\s*project-instance\s*$/m.test(readFileSync(identityPath, "utf8"));
  if (!isInstance) return PROJECTION_ROOTS;
  const roots = lock?.projectionRoots;
  if (!Array.isArray(roots) || !roots.length || roots.some((root) => !PROJECTION_ROOTS.includes(root))) throw new TypeError("project-instance skills-lock.json 缺少有效 projectionRoots");
  return [...new Set(roots)];
}
`
			text = strings.Replace(text, marker, marker+addition, 1)
			text = strings.Replace(text, "  const shared = sharedFromLock(lock);", "  const shared = sharedFromLock(lock);\n  const projectionRoots = projectionRootsFor(lock);", 1)
			text = strings.Replace(text, "  const oldLock = parseLock(); const previous = priorMetadata(oldLock);", "  const oldLock = parseLock(); const projectionRoots = projectionRootsFor(oldLock); const previous = priorMetadata(oldLock);", 1)
			text = strings.ReplaceAll(text, "for (const root of PROJECTION_ROOTS) {", "for (const root of projectionRoots) {")
			text = strings.Replace(text, `const targets = [".agents/skills", ...PROJECTION_ROOTS];`, `const targets = [".agents/skills", ...projectionRoots];`, 1)
			text = strings.Replace(text, "projectionRoots: PROJECTION_ROOTS, sources, skills", "projectionRoots, sources, skills", 1)
		}
	case ".template-spec/engineering/backend-platforms.json":
		if selected {
			text = strings.ReplaceAll(text, ".template-source/evidence/maintenance/2026-09-18-yss-backend-components/aliyun-artifact-resolution.json", "docs/engineering/evidence/aliyun-artifact-resolution.json")
		}
	case ".template-spec/agents/backend-architecture-profiles.md":
		if selected {
			text = strings.Replace(text, "；依据见 `.template-source/evidence/maintenance/2026-09-12-existing-project-delivery/maven-adapters-04.json`", "；适配验证证据保留在模板源，项目实例须对自身工程重新验证", 1)
		}
	case ".template-spec/user-guide/用户手册.md":
		if selected {
			text = "# " + projectToken + " 用户手册\n\n本仓是 `project-instance`，用于 " + domainToken + " 的研发资产。先阅读根 [AGENTS.md](../../AGENTS.md)、[CONTEXT.md](../../CONTEXT.md) 与 [生命周期资产索引](../process/lifecycle-artifact-map.md)。\n\n初始化安装分诊与 Plan 入口资产、入口 Skill 与所选 Agent 平台。进入后续阶段前，运行 `create-yss-spec assets ensure <stage-id> --plan` 核对文件，再运行 `--apply` 原子安装；专项 Skill 使用 `create-yss-spec skills ensure <skill-id...> --plan/--apply`，会补齐该 Skill 引用的实例文件。增加平台使用 `create-yss-spec skills runtime add <codex|cursor|pi> --plan/--apply`。CLI 快照必须与实例记录的模板提交一致；版本不匹配时先核对匹配的 CLI 或 `create-yss-spec sync --plan`，不自动扩展同步范围。\n\n项目校验运行 `scripts/verify-project-instance`；实例 CI 应执行该命令和项目实际的构建、测试。\n" + skillPreflight
		}
	case "yss-project.yaml":
		if !regexp.MustCompile(`(?m)^repository_mode:\s*template-source\s*$`).MatchString(text) {
			return nil, fmt.Errorf("源仓库身份非法")
		}
		text = regexp.MustCompile(`(?m)^repository_mode:\s*template-source$`).ReplaceAllString(text, "repository_mode: project-instance")
	case "AGENTS.md":
		text = renderLabels(text)
		if selected {
			text = regexp.MustCompile("(?m)\\| 影响面、`not-applicable`、模板维护强度 \\|[^\\n]*\\n").ReplaceAllString(text, "| 影响面与 `not-applicable` | `.template-spec/process/harness-process-tailoring.md` |\n")
			text += "\n## 按需阶段资产与 Skill\n\n进入后续生命周期阶段前，运行 `create-yss-spec assets ensure <stage-id> --plan` 查看完整依赖，核对后运行 `--apply`。专项任务根据 .template-spec/agents/yss-skill-registry.yaml 选定 Skill，运行 `create-yss-spec skills ensure <skill-id...> --plan`，核对后运行 `--apply`。缺少阶段资产时先补装，不以缺文件推定门禁不适用。若 CLI 快照与实例模板提交不一致，先核对匹配的 CLI 或 `create-yss-spec sync --plan`；不自动扩展同步范围。\n" + skillPreflight
		}
	case "README.md":
		platform := "legacy-all"
		if selected {
			platform = "codex"
		}
		guidance := "专项 Skill 使用 `create-yss-spec skills ensure <skill-id> --plan/--apply`。"
		if selected {
			guidance = "后续阶段先运行 `create-yss-spec assets ensure <stage-id> --plan` 核对依赖，再运行 `--apply`；" + guidance
		}
		text = "# " + projectToken + "\n\n本仓库用于管理 " + domainToken + " 的研发资产。\n\n- 默认 Issue Tracker：local-markdown\n- Agent 平台：" + platform + "\n- 协作入口：[AGENTS.md](./AGENTS.md)\n- 业务词汇：[CONTEXT.md](./CONTEXT.md)\n- 用户指南：[.template-spec/user-guide/用户手册.md](./.template-spec/user-guide/用户手册.md)\n\n项目校验：`scripts/verify-project-instance`。" + guidance + "\n"
	case ".gitignore":
		start := "# >>> create-yss-spec managed rules"
		end := "# <<< create-yss-spec managed rules"
		a := strings.Index(text, start)
		z := strings.Index(text, end)
		if a < 0 || z < a || strings.Count(text, start) != 1 || strings.Count(text, end) != 1 {
			return nil, fmt.Errorf("模板 .gitignore managed rules 标记无效")
		}
		z += len(end)
		if z < len(text) && text[z] == '\r' {
			z++
		}
		if z < len(text) && text[z] == '\n' {
			z++
		}
		text = text[a:z]
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
	}
	if selected {
		if ref == ".template-spec/design/README.md" {
			text = regexp.MustCompile(`(?m)^.*design-system-sync\.yaml.*\n`).ReplaceAllString(text, "")
		}
		if strings.HasPrefix(ref, ".template-spec/user-guide/") && strings.HasSuffix(ref, ".md") && ref != ".template-spec/user-guide/用户手册.md" {
			text = strings.ReplaceAll(text, "[设备借用贯穿案例](设备借用贯穿案例.md)", "[项目用户手册](用户手册.md)")
			text = strings.ReplaceAll(text, "本仓是 `template-source`", "模板源是 `template-source`")
		}
		if strings.Contains(ref, "/skills/yss-design-system/") {
			text = strings.Replace(text, "运行前先更新 `.template-spec/design/design-system-sync.yaml` 的规范源摘要。", "项目实例无需模板间的 design-system-sync 摘要。", 1)
			text = strings.Replace(text, "修改根 DESIGN.md → 更新 design-system-sync.yaml 摘要 →", "修改根 DESIGN.md →", 1)
		}
	}
	return []byte(text), nil
}
func renderLabels(s string) string {
	for label, value := range map[string]string{"项目名称": projectToken, "业务领域": domainToken, "团队规模": teamToken} {
		re := regexp.MustCompile(`(\*\*` + label + `：\*\*\s*)\[填写\]`)
		loc := re.FindStringSubmatchIndex(s)
		if loc != nil {
			s = s[:loc[0]] + s[loc[2]:loc[3]] + value + s[loc[1]:]
		}
	}
	return s
}

// Ordered JSON preserves the source lock's canonical field ordering when a
// selected runtime projection is generated, matching its existing byte contract.
type orderedJSON struct {
	keys   []string
	values map[string]*orderedJSON
	array  []*orderedJSON
	scalar any
	kind   byte
}

func readOrdered(d *json.Decoder) (*orderedJSON, error) {
	t, e := d.Token()
	if e != nil {
		return nil, e
	}
	n := &orderedJSON{scalar: t}
	if delim, ok := t.(json.Delim); ok {
		switch delim {
		case '{':
			n.kind = 'o'
			n.values = map[string]*orderedJSON{}
			for d.More() {
				key, e := d.Token()
				if e != nil {
					return nil, e
				}
				k, ok := key.(string)
				if !ok {
					return nil, fmt.Errorf("invalid JSON object key")
				}
				if _, ok := n.values[k]; ok {
					return nil, fmt.Errorf("duplicate JSON object key: %s", k)
				}
				v, e := readOrdered(d)
				if e != nil {
					return nil, e
				}
				n.keys = append(n.keys, k)
				n.values[k] = v
			}
			_, e = d.Token()
		case '[':
			n.kind = 'a'
			n.array = []*orderedJSON{}
			for d.More() {
				v, e := readOrdered(d)
				if e != nil {
					return nil, e
				}
				n.array = append(n.array, v)
			}
			_, e = d.Token()
		}
	}
	return n, e
}
func (n *orderedJSON) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	switch n.kind {
	case 'o':
		b.WriteByte('{')
		for i, k := range n.keys {
			if i > 0 {
				b.WriteByte(',')
			}
			key, _ := json.Marshal(k)
			b.Write(key)
			b.WriteByte(':')
			v, e := n.values[k].MarshalJSON()
			if e != nil {
				return nil, e
			}
			b.Write(v)
		}
		b.WriteByte('}')
	case 'a':
		b.WriteByte('[')
		for i, v := range n.array {
			if i > 0 {
				b.WriteByte(',')
			}
			a, e := v.MarshalJSON()
			if e != nil {
				return nil, e
			}
			b.Write(a)
		}
		b.WriteByte(']')
	default:
		return json.Marshal(n.scalar)
	}
	return b.Bytes(), nil
}
func (n *orderedJSON) set(k string, v *orderedJSON) {
	if _, ok := n.values[k]; !ok {
		n.keys = append(n.keys, k)
	}
	n.values[k] = v
}
func arrayNode(s []string) *orderedJSON {
	n := &orderedJSON{kind: 'a', array: []*orderedJSON{}}
	for _, v := range s {
		n.array = append(n.array, &orderedJSON{scalar: v})
	}
	return n
}

func decodeOrdered(data []byte) (*orderedJSON, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	root, e := readOrdered(d)
	if e != nil {
		return nil, e
	}
	if _, e := d.Token(); e != io.EOF {
		return nil, fmt.Errorf("trailing skills lock JSON")
	}
	return root, nil
}

func orderedBytes(root *orderedJSON) ([]byte, error) {
	raw, e := root.MarshalJSON()
	if e != nil {
		return nil, e
	}
	var pretty bytes.Buffer
	if e = json.Indent(&pretty, raw, "", "  "); e != nil {
		return nil, e
	}
	pretty.WriteByte('\n')
	return pretty.Bytes(), nil
}

func selectedSourceLock(data []byte, skills []string) ([]byte, error) {
	return SelectedSourceLock(data, skills, []string{"codex"})
}

// SelectedSourceLock filters a fixed source lock without reconstructing its
// metadata. Object field order and source identity survive the selected Skill
// and runtime projection; runtime order determines roots and shared targets.
func SelectedSourceLock(data []byte, skills, runtimes []string) ([]byte, error) {
	runtimeRoots := map[string]string{"codex": ".codex/skills", "cursor": ".cursor/skills", "pi": ".pi/skills"}
	roots := []string{}
	seen := map[string]bool{}
	for _, runtime := range runtimes {
		ref, ok := runtimeRoots[runtime]
		if !ok || seen[runtime] {
			return nil, fmt.Errorf("invalid or duplicate Skill runtime: %s", runtime)
		}
		seen[runtime] = true
		roots = append(roots, ref)
	}
	if len(roots) == 0 {
		return nil, fmt.Errorf("selected Skill lock requires a runtime")
	}
	root, e := decodeOrdered(data)
	if e != nil {
		return nil, e
	}
	if root.kind != 'o' || root.values["skills"] == nil || root.values["skills"].kind != 'o' {
		return nil, fmt.Errorf("invalid skills lock")
	}
	if version := root.values["version"]; version != nil && (version.kind != 0 || version.scalar != json.Number("3")) {
		return nil, fmt.Errorf("unsupported skills lock version")
	}
	if sources := root.values["sources"]; sources != nil && sources.kind != 'o' {
		return nil, fmt.Errorf("invalid skills lock sources")
	}
	root.set("projectionRoots", arrayNode(roots))
	section := root.values["skills"]
	shared := section.values["shared"]
	if shared == nil || shared.kind != 'o' {
		return nil, fmt.Errorf("missing shared skills")
	}
	selected := stringSet(skills)
	keys := []string{}
	for _, k := range shared.keys {
		if shared.values[k].kind != 'o' {
			return nil, fmt.Errorf("invalid shared Skill entry: %s", k)
		}
		if selected[k] {
			keys = append(keys, k)
			shared.values[k].set("targets", arrayNode(append([]string{".agents/skills"}, roots...)))
		}
	}
	shared.keys = keys
	platform := &orderedJSON{kind: 'o', values: map[string]*orderedJSON{}}
	if prior := section.values["platform"]; prior != nil {
		if prior.kind != 'o' {
			return nil, fmt.Errorf("invalid platform Skill section")
		}
		for _, ref := range roots {
			entries := prior.values[ref]
			if entries == nil {
				continue
			}
			if entries.kind != 'o' {
				return nil, fmt.Errorf("invalid platform Skill entries: %s", ref)
			}
			keys = []string{}
			for _, k := range entries.keys {
				if entries.values[k].kind != 'o' {
					return nil, fmt.Errorf("invalid platform Skill entry: %s/%s", ref, k)
				}
				if selected[k] {
					keys = append(keys, k)
				}
			}
			entries.keys = keys
			if len(keys) > 0 {
				platform.set(ref, entries)
			}
		}
	}
	section.set("platform", platform)
	return orderedBytes(root)
}

// Template maintenance instructions are source-only; project distributions retain
// the product governance sections exactly as the prior fixed-source producer.
func prepareSource(profile, ref string, data []byte) ([]byte, error) {
	if ref == "AGENTS.md" {
		start := []byte("<!-- YSS_TEMPLATE_SOURCE_ONLY_START -->")
		end := []byte("<!-- YSS_TEMPLATE_SOURCE_ONLY_END -->")
		a, z := bytes.Index(data, start), bytes.Index(data, end)
		if a >= 0 || z >= 0 {
			if bytes.Count(data, start) != 1 || bytes.Count(data, end) != 1 || z < a {
				return nil, fmt.Errorf("AGENTS_SOURCE_ONLY_MARKERS: 缺失、重复或顺序错误")
			}
			data = append(append([]byte{}, data[:a]...), data[z+len(end):]...)
		}
	}
	if profile != "spec" {
		if (profile == "backend" || profile == "frontend") && ref == ".gitignore" {
			return regexp.MustCompile("(?s)\\n# Generated shared skills; authority: Spec profile-skill-sync.json\\n.*?\\n# End generated shared skills\\n").ReplaceAll(data, nil), nil
		}
		return data, nil
	}
	text := string(data)
	switch ref {
	case "AGENTS.md":
		text = regexp.MustCompile("(?s)\\n## 4\\. `template-source` 模板维护路由.*?(\\n## 5\\.)").ReplaceAllString(text, "$1")
	case ".template-spec/process/harness-process-tailoring.md":
		text = regexp.MustCompile("(?s)\\n## 4\\. 模板维护验证与审查强度分级.*?(\\n## 5\\.)").ReplaceAllString(text, "$1")
		text = regexp.MustCompile("(?s)\\n模板维护默认停在 `implementation-ready`.*$").ReplaceAllString(text, "\n")
	case ".template-spec/process/implementation-repo-integration.md":
		text = regexp.MustCompile("(?s)\\n## 3\\. 本变更的跨仓库合同.*$").ReplaceAllString(text, "\n")
	}
	return []byte(text), nil
}

// Native generated entry guidance documents the actual public plan/apply seam.
// Historical package names and source lineage are retained; only command
// invocations with a native equivalent are rewritten in distributed Markdown.
func nativeGuidance(profile, ref string, data []byte) []byte {
	if !strings.HasSuffix(ref, ".md") {
		return data
	}
	text := string(data)
	if profile == "spec" {
		skills := "专项 Skill 先运行 `yss skills ensure <skill-id...> --root . --plan --out <新计划文件>`；核对计划后运行 `yss skills ensure <skill-id...> --root . --apply --plan-file <计划文件>`。"
		assets := "进入后续生命周期阶段前，运行 `yss assets ensure <stage-id> --root . --plan --out <新计划文件>` 核对完整依赖，再运行 `yss assets ensure <stage-id> --root . --apply --plan-file <计划文件>` 原子安装。"
		binding := "CLI bundle 必须与实例记录的模板提交一致；版本变化先运行 `yss sync --root . --plan --out <新计划文件>` 审阅迁移范围。Agent 平台范围由当前初始化或迁移合同定义。"
		switch ref {
		case "AGENTS.md":
			if i := strings.Index(text, "\n## 按需阶段资产与 Skill\n"); i >= 0 {
				text = text[:i] + "\n## 按需阶段资产与 Skill\n\n" + assets + skills + "缺少阶段资产时先补装，不以缺文件推定门禁不适用。" + binding + "\n" + skillPreflight
			}
		case "README.md":
			if i := strings.Index(text, "项目校验："); i >= 0 {
				text = text[:i] + "项目校验：`scripts/verify-project-instance`。" + assets + skills + "\n"
			}
		case ".template-spec/user-guide/用户手册.md":
			if strings.Contains(text, "初始化安装分诊与 Plan") {
				text = "# " + projectToken + " 用户手册\n\n本仓是 `project-instance`，用于 " + domainToken + " 的研发资产。先阅读根 [AGENTS.md](../../AGENTS.md)、[CONTEXT.md](../../CONTEXT.md) 与 [生命周期资产索引](../process/lifecycle-artifact-map.md)。\n\n初始化安装分诊与 Plan 入口资产、入口 Skill 与所选 Agent 平台。" + assets + skills + "补装计划包含该 Skill 引用的实例文件。" + binding + "\n\n项目校验运行 `scripts/verify-project-instance`；实例 CI 应执行该命令和项目实际的构建、测试。\n" + skillPreflight
			}
		}
	}
	for _, legacy := range []string{"create-yss-spec", "create-yss-strategic-design", "create-yss-harness-design", "create-yss-harness-backend", "create-yss-harness-frontend"} {
		for _, command := range []string{"skills ensure", "assets ensure", "sync", "diff", "doctor", "attach", "migrate", "rollback", "recover"} {
			text = strings.ReplaceAll(text, legacy+" "+command, "yss "+command)
		}
	}
	return []byte(text)
}
