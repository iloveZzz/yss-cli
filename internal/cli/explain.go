package cli

import (
	"errors"
	"io/fs"
	"os"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
)

const (
	explainGlossaryRef = ".template-spec/process/process-glossary.md"
	explainRegistryRef = ".template-spec/process/lifecycle-registry.yaml"
	explainLimit       = 10
)

// 注册表中可被解释的对象集合，按出现顺序检索。
var explainKinds = []struct{ key, kind string }{
	{"stages", "stage"}, {"gates", "gate"}, {"checks", "check"},
	{"artifacts", "artifact"}, {"evidence", "evidence"}, {"work_units", "work-unit"},
}

type explainMatch struct {
	Kind    string `json:"kind"`
	ID      string `json:"id,omitempty"`
	Term    string `json:"term,omitempty"`
	Name    string `json:"name,omitempty"`
	Meaning string `json:"meaning,omitempty"`
	Note    string `json:"note,omitempty"`
	Source  string `json:"source"`
}

// explainCommand 在当前项目的流程术语表与生命周期注册表里查找术语或稳定 ID；只读，不解释业务术语。
func explainCommand(root string, args []string) (any, error) {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return nil, domain.Fail("ARGUMENT", "需要一个术语或稳定 ID，例如 yss explain gate.plan-approved")
	}
	query := strings.TrimSpace(args[0])
	matches := []explainMatch{}
	sources := []string{}
	if raw, ok, err := readProjectFile(root, explainRegistryRef); err != nil {
		return nil, err
	} else if ok {
		sources = append(sources, explainRegistryRef)
		found, err := explainRegistry(raw, query)
		if err != nil {
			return nil, err
		}
		matches = append(matches, found...)
	}
	if raw, ok, err := readProjectFile(root, explainGlossaryRef); err != nil {
		return nil, err
	} else if ok {
		sources = append(sources, explainGlossaryRef)
		matches = append(matches, explainGlossary(string(raw), query)...)
	}
	if len(sources) == 0 {
		return nil, domain.Fail("IDENTITY", "当前目录不是 YSS 项目：缺少流程术语表与生命周期注册表")
	}
	truncated := len(matches) > explainLimit
	if truncated {
		matches = matches[:explainLimit]
	}
	result := map[string]any{"query": query, "matches": matches, "truncated": truncated, "sources": sources, "read_only": true}
	if len(matches) == 0 {
		result["hint"] = "未找到；业务术语见根 CONTEXT.md，完整流程词表见 " + explainGlossaryRef
	}
	return result, nil
}

func readProjectFile(root, ref string) ([]byte, bool, error) {
	path, err := safefs.Path(root, ref)
	if err != nil {
		return nil, false, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	return raw, err == nil, err
}

// explainRegistry 先按完整 ID 或去掉类别前缀的 ID 精确匹配，再按名称包含匹配。
func explainRegistry(raw []byte, query string) ([]explainMatch, error) {
	doc, err := schema.Parse(raw)
	if err != nil {
		return nil, domain.Fail("INPUT", explainRegistryRef+" 无法解析: "+err.Error())
	}
	registry, _ := doc.(map[string]any)
	lower := strings.ToLower(query)
	exact, partial := []explainMatch{}, []explainMatch{}
	for _, k := range explainKinds {
		items, _ := registry[k.key].([]any)
		for _, item := range items {
			entry, _ := item.(map[string]any)
			id, _ := entry["id"].(string)
			if id == "" {
				continue
			}
			name := explainField(entry, "public_name", "name")
			m := explainMatch{Kind: k.kind, ID: id, Name: name, Meaning: explainField(entry, "public_goal", "goal", "public_description", "description", "completion", "trigger"), Source: explainRegistryRef}
			switch {
			case id == query || strings.TrimPrefix(id, k.kind+".") == query:
				exact = append(exact, m)
			case strings.Contains(strings.ToLower(id), lower) || strings.Contains(strings.ToLower(name), lower):
				partial = append(partial, m)
			}
		}
	}
	return append(exact, partial...), nil
}

// explainGlossary 读取术语表第一张“| 术语 |”表格；术语列包含查询词即命中。
func explainGlossary(text, query string) []explainMatch {
	matches := []explainMatch{}
	lower := strings.ToLower(query)
	inTable := false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "| 术语 |") {
			inTable = true
			continue
		}
		if !inTable {
			continue
		}
		if !strings.HasPrefix(line, "|") {
			break
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		if len(cells) < 4 || strings.HasPrefix(strings.TrimSpace(cells[0]), "---") {
			continue
		}
		term := strings.TrimSpace(cells[0])
		if strings.Contains(strings.ToLower(strings.Trim(term, "`")), lower) {
			matches = append(matches, explainMatch{Kind: "term", Term: term, Meaning: strings.TrimSpace(cells[1]), Note: strings.TrimSpace(cells[3]), Source: explainGlossaryRef})
		}
	}
	return matches
}

func explainField(entry map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := entry[k].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// renderExplain 读取经 JSON 归一化后的结果（matches 为 []any 的对象列表）。
func renderExplain(m map[string]any) string {
	var out strings.Builder
	matches, _ := m["matches"].([]any)
	if len(matches) == 0 {
		return "未找到：" + explainString(m["query"]) + "\n" + explainString(m["hint"])
	}
	for _, item := range matches {
		x, _ := item.(map[string]any)
		label := explainString(x["id"])
		if label == "" {
			label = explainString(x["term"])
		}
		out.WriteString("· " + label)
		if name := explainString(x["name"]); name != "" {
			out.WriteString("（" + name + "）")
		}
		out.WriteString(" [" + explainString(x["kind"]) + "]\n")
		if meaning := explainString(x["meaning"]); meaning != "" {
			out.WriteString("  " + meaning + "\n")
		}
		if note := explainString(x["note"]); note != "" && note != "—" {
			out.WriteString("  注意：" + note + "\n")
		}
	}
	if m["truncated"] == true {
		out.WriteString("结果已截断，请用更具体的词或完整 ID。\n")
	}
	return strings.TrimRight(out.String(), "\n")
}

func explainString(v any) string {
	s, _ := v.(string)
	return s
}
