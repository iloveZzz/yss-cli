package reading

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// 渲染规则提示：本文件、render.go、build.go 里的渲染相关代码改变输出语义时，
// 必须同步评估是否需要递增清单 renderer 的规则版本（见 docs/governance-convergence.md）。

const (
	navBegin = "<!-- YSS-READING:BEGIN -->"
	navEnd   = "<!-- YSS-READING:END -->"
)

type manifestRow struct {
	Scope, Ref string
	Digest     any // string 或 nil（缺失文件）
}

// jsString 与 JavaScript JSON.stringify 对字符串的转义一致：仅转义引号、反斜杠与控制字符，
// 不转义非 ASCII、<>& 以及 U+2028/U+2029，清单才能与 Node 渲染逐字节相同。
func jsString(v string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range v {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func jsValue(v any) string {
	if v == nil {
		return "null"
	}
	return jsString(v.(string))
}

var readingOrder = collate.New(language.English)

// ManifestRows 把构建观察到的项目文件与工具文件归并成清单依赖行，顺序与 Node 渲染器一致：
// 已存在文件按 scope+ref 的本地化顺序，缺失文件追加其后（阅读策略文件的缺失行最后）。
func (b *ReadingBuild) ManifestRows(s *kernel.SemanticSession, policyRef string) []manifestRow {
	byKey := map[string]manifestRow{}
	order := map[string]int{}
	add := func(scope, ref string, digest any) {
		key := scope + "\x00" + ref
		byKey[key] = manifestRow{scope, ref, digest}
	}
	for i, ref := range b.ProjectOrder {
		order["project\x00"+ref] = i
	}
	for ref, digest := range b.ProjectRefs {
		add("project", ref, digest)
	}
	for ref, digest := range b.ToolRefs {
		scope, local := readingDependencyScope(s, ref)
		add(scope, local, digest)
	}
	present, missing := []manifestRow{}, []manifestRow{}
	for _, row := range byKey {
		if row.Digest == nil {
			missing = append(missing, row)
		} else {
			present = append(present, row)
		}
	}
	sort.Slice(present, func(i, j int) bool {
		return readingOrder.CompareString(present[i].Scope+present[i].Ref, present[j].Scope+present[j].Ref) < 0
	})
	rank := func(row manifestRow) (int, int) {
		if row.Scope == "project" && row.Ref == policyRef {
			return 1, 0
		}
		return 0, order[row.Scope+"\x00"+row.Ref]
	}
	sort.Slice(missing, func(i, j int) bool {
		a, ai := rank(missing[i])
		c, ci := rank(missing[j])
		if a != c {
			return a < c
		}
		if ai != ci {
			return ai < ci
		}
		return missing[i].Scope+missing[i].Ref < missing[j].Scope+missing[j].Ref
	})
	return append(present, missing...)
}

// RenderManifest 序列化 v1 清单：键序固定、两格缩进、末尾换行，与 Node `JSON.stringify(x, null, 2) + "\n"` 相同。
func (b *ReadingBuild) RenderManifest(s *kernel.SemanticSession, checkpoint, navRef string) []byte {
	var out strings.Builder
	out.WriteString("{\n  \"schema_version\": 1,\n  \"checkpoint_ref\": " + jsString(checkpoint) + ",\n  \"renderer\": ")
	keys := make([]string, 0, len(b.Renderer))
	for key := range b.Renderer {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return readingOrder.CompareString(keys[i], keys[j]) < 0 })
	if len(keys) == 0 {
		out.WriteString("{}")
	} else {
		out.WriteString("{\n")
		for i, key := range keys {
			out.WriteString("    " + jsString(key) + ": " + jsValue(b.Renderer[key]))
			if i+1 < len(keys) {
				out.WriteString(",")
			}
			out.WriteString("\n")
		}
		out.WriteString("  }")
	}
	rows := b.ManifestRows(s, readingPolicyRef)
	out.WriteString(",\n  \"dependencies\": ")
	if len(rows) == 0 {
		out.WriteString("[]")
	} else {
		out.WriteString("[\n")
		for i, row := range rows {
			out.WriteString("    {\n      \"scope\": " + jsString(row.Scope) + ",\n      \"ref\": " + jsString(row.Ref) + ",\n      \"digest\": " + jsValue(row.Digest) + "\n    }")
			if i+1 < len(rows) {
				out.WriteString(",")
			}
			out.WriteString("\n")
		}
		out.WriteString("  ]")
	}
	out.WriteString(",\n  \"outputs\": ")
	if len(b.OutputOrder) == 0 {
		out.WriteString("{}")
	} else {
		out.WriteString("{\n")
		for i, ref := range b.OutputOrder {
			out.WriteString("    " + jsString(ref) + ": " + jsString("sha256:"+safefs.Digest([]byte(b.Outputs[ref]))))
			if i+1 < len(b.OutputOrder) {
				out.WriteString(",")
			}
			out.WriteString("\n")
		}
		out.WriteString("  }")
	}
	out.WriteString(",\n  \"navigation\": {\n    \"ref\": " + jsString(navRef) + ",\n    \"block_digest\": " + jsString("sha256:"+safefs.Digest([]byte(b.Block))) + "\n  },\n  \"diagnostics\": []\n}\n")
	return []byte(out.String())
}

// oldManifest 是上一次渲染登记的输出与导航摘要，用于判断现有文件是否可被安全覆盖。
type oldManifest struct {
	Outputs     map[string]string
	BlockDigest string
}

var reviewNames = map[string]bool{"domain-strategy.review.md": true, "stage-decision-package.review.md": true, "status.review.md": true}

// parseOldManifest 对应 Node oldManifest：不是合法 v1 清单、输出键不在允许集合或导航不是 <base>/map.md
// 都拒绝（READING_MANIFEST_INVALID），保留现场，不覆盖。重复键与超范围整数按更严处理。
func parseOldManifest(s *kernel.SemanticSession, raw []byte, base, directory string) (*oldManifest, error) {
	invalid := func() error {
		return s.Reject("READING_MANIFEST_INVALID", "阅读清单无效；请保留现有文件并先恢复最近一次有效清单，再重新渲染")
	}
	node, err := readingParse(raw)
	if err != nil || node.Kind != '{' {
		return nil, invalid()
	}
	version := node.get("schema_version")
	if n, ok := kernel.Integer(versionValue(version)); !ok || n != 1 {
		return nil, invalid()
	}
	outputs, navigation := node.get("outputs"), node.get("navigation")
	if outputs == nil || outputs.Kind != '{' || navigation == nil || navigation.Kind != '{' {
		return nil, invalid()
	}
	old := &oldManifest{Outputs: map[string]string{}}
	for _, p := range outputs.Pairs {
		if path.Dir(p.Key) != directory || !reviewNames[path.Base(p.Key)] {
			return nil, invalid()
		}
		digest := ""
		if p.Value.Kind == 0 {
			if v, ok := p.Value.Value.(string); ok {
				digest = v
			}
		}
		old.Outputs[p.Key] = digest
	}
	ref := navigation.get("ref")
	if ref == nil || ref.Value != base+"/map.md" {
		return nil, invalid()
	}
	if digest := navigation.get("block_digest"); digest != nil {
		if v, ok := digest.Value.(string); ok {
			old.BlockDigest = v
		}
	}
	return old, nil
}

func versionValue(n *readingNode) any {
	if n == nil || n.Kind != 0 {
		return nil
	}
	return n.Value
}

// RenderChange 是一次渲染要写的一个文件。Before 为空表示文件当前不存在。
type RenderChange struct {
	Ref    string
	Before []byte
	After  []byte
	Exists bool
}

// RenderPlan 是 PlanReading 的纯结果：构建产物与需要写入的文件，不含任何写入。
type RenderPlan struct {
	Checkpoint string
	Base       string
	Build      *ReadingBuild
	Changes    []RenderChange
}

func readOptional(s *kernel.SemanticSession, ref string) ([]byte, bool, error) {
	d, err := s.V.Watch(ref)
	if err != nil {
		return nil, false, err
	}
	if d.Type == "missing" {
		return nil, false, nil
	}
	if d.Type != "file" {
		return nil, false, s.Unavailable("INPUT", "输入必须为普通文件: "+ref)
	}
	raw, err := s.Bytes(ref)
	return raw, true, err
}

// PlanReading 先做策略层构建（任何来源诊断都 fail-closed），再按 Node changesFor 的判定顺序比较现状，
// 产出变更集或拒绝。它只读，不写文件；错误码与 Node 的冲突保护一一对应。
func PlanReading(s *kernel.SemanticSession, cpRef string) (*RenderPlan, error) {
	return PlanReadingWith(s, cpRef, nil)
}

// PlanReadingWith 在 PlanReading 的基础上假定 overrides 中的项目文件已被写成给定内容：
// 构建结果里这些文件的依赖摘要按给定内容计算（例如启用时先写入的阅读策略）。
// 提交后的完整核验仍以磁盘上的实际内容为准，所以假定错误只会导致回滚，不会放行。
func PlanReadingWith(s *kernel.SemanticSession, cpRef string, overrides map[string][]byte) (*RenderPlan, error) {
	cp, err := s.Doc(cpRef)
	if err != nil {
		return nil, err
	}
	_, base, err := kernel.StageFeatureBinding(s, cpRef, cp)
	if err != nil {
		return nil, err
	}
	directory := base + "/reading"
	for _, name := range []string{".transaction.json", ".lock"} {
		exists, err := s.Exists(directory + "/" + name)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, s.Reject("READING_LEGACY_JOURNAL", "阅读目录留有旧版渲染的事务日志或锁："+directory+"/"+name+"；先用原渲染器恢复，不能直接覆盖")
		}
	}
	build, err := BuildReading(s, cpRef)
	if err != nil {
		return nil, err
	}
	for ref, data := range overrides {
		if _, seen := build.ProjectRefs[ref]; !seen {
			build.ProjectOrder = append(build.ProjectOrder, ref)
		}
		build.ProjectRefs[ref] = "sha256:" + safefs.Digest(data)
	}
	navRef, manifestRef := base+"/map.md", directory+"/.manifest.json"

	var old *oldManifest
	if raw, exists, err := readOptional(s, manifestRef); err != nil {
		return nil, err
	} else if exists {
		if old, err = parseOldManifest(s, raw, base, directory); err != nil {
			return nil, err
		}
	}
	mapRaw, mapExists, err := readOptional(s, navRef)
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(mapRaw) {
		return nil, s.Reject("READING_NAVIGATION_CONFLICT", "导航文件不是合法 UTF-8，不自动改写: "+navRef)
	}
	current := string(mapRaw)
	starts, ends := strings.Count(current, navBegin), strings.Count(current, navEnd)
	a, bEnd := -1, -1
	if starts != 0 || ends != 0 {
		if starts != 1 || ends != 1 || strings.Index(current, navEnd) < strings.Index(current, navBegin) {
			return nil, s.Reject("READING_NAVIGATION_CONFLICT", "导航受管区标记个数或顺序不合法: "+navRef)
		}
		a = strings.Index(current, navBegin)
		bEnd = strings.Index(current, navEnd) + len(navEnd)
	}

	plan := &RenderPlan{Checkpoint: cpRef, Base: base, Build: build}
	for _, ref := range build.OutputOrder {
		raw, exists, err := readOptional(s, ref)
		if err != nil {
			return nil, err
		}
		if exists {
			registered := ""
			if old != nil {
				registered = old.Outputs[ref]
			}
			if registered == "" || "sha256:"+safefs.Digest(raw) != registered {
				return nil, s.Reject("READING_OUTPUT_CONFLICT", "输出文件已存在但与上次渲染登记不符，保留现场: "+ref)
			}
		}
		if !exists || string(raw) != build.Outputs[ref] {
			plan.Changes = append(plan.Changes, RenderChange{Ref: ref, Before: raw, After: []byte(build.Outputs[ref]), Exists: exists})
		}
	}
	if old != nil {
		retired := []string{}
		for ref := range old.Outputs {
			if _, kept := build.Outputs[ref]; !kept {
				retired = append(retired, ref)
			}
		}
		sort.Strings(retired)
		if len(retired) > 0 {
			return nil, s.Reject("READING_RETIRED_OUTPUT", "上次登记的输出本次不再产生，需人工审阅后处理: "+retired[0])
		}
	}
	if a >= 0 && (old == nil || "sha256:"+safefs.Digest([]byte(current[a:bEnd])) != old.BlockDigest) {
		return nil, s.Reject("READING_OUTPUT_CONFLICT", "导航受管区与上次渲染登记不符，保留现场: "+navRef)
	}
	next := current
	if a >= 0 {
		next = current[:a] + build.Block + current[bEnd:]
	} else {
		if !strings.HasSuffix(current, "\n") {
			next += "\n"
		}
		next += "\n" + build.Block + "\n"
	}
	if next != current {
		plan.Changes = append(plan.Changes, RenderChange{Ref: navRef, Before: mapRaw, After: []byte(next), Exists: mapExists})
	}
	manifest := build.RenderManifest(s, cpRef, navRef)
	oldRaw, manifestExists, err := readOptional(s, manifestRef)
	if err != nil {
		return nil, err
	}
	if !manifestExists || string(oldRaw) != string(manifest) {
		plan.Changes = append(plan.Changes, RenderChange{Ref: manifestRef, Before: oldRaw, After: manifest, Exists: manifestExists})
	}
	return plan, nil
}
