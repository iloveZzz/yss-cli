package governance

import (
	"fmt"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

var pascal = regexp.MustCompile(`^[A-Z][A-Za-z0-9]+$`)
var aliasPattern = regexp.MustCompile(`(?:^|；)\s*避免[：:]\s*([^；]+)`)
var trailingSpace = regexp.MustCompile(`[ \t]+$`)
var tableSeparator = regexp.MustCompile(`^:?-{3,}:?$`)

func normalizeContext(source string) string {
	source = strings.ReplaceAll(source, "\r\n", "\n")
	lines := strings.Split(source, "\n")
	for i := range lines {
		lines[i] = trailingSpace.ReplaceAllString(lines[i], "")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
}

type contextRow struct {
	cells []string
	line  int
}

func splitRow(line string) []string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "|") || !strings.HasSuffix(line, "|") {
		return nil
	}
	cells := strings.Split(line[1:len(line)-1], "|")
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}
	return cells
}
func readTable(lines []string, heading string, variants [][]string) ([]contextRow, error) {
	i := -1
	for n, line := range lines {
		if strings.TrimSpace(line) == heading {
			i = n
			break
		}
	}
	if i < 0 {
		return nil, domain.Fail("CONTEXT", "缺少 "+heading)
	}
	i++
	for i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "|") && !strings.HasPrefix(strings.TrimSpace(lines[i]), "## ") {
		i++
	}
	if i+1 >= len(lines) {
		return nil, domain.Fail("CONTEXT", heading+" 缺少表格")
	}
	header := splitRow(lines[i])
	valid := false
	for _, variant := range variants {
		if strings.Join(header, "\x00") == strings.Join(variant, "\x00") {
			valid = true
		}
	}
	if !valid {
		return nil, domain.Fail("CONTEXT", heading+" 表头不符合 Context Contract")
	}
	sep := splitRow(lines[i+1])
	if len(sep) != len(header) {
		return nil, domain.Fail("CONTEXT", heading+" 缺少合法 Markdown 表格分隔行")
	}
	for _, cell := range sep {
		if !tableSeparator.MatchString(cell) {
			return nil, domain.Fail("CONTEXT", heading+" 缺少合法 Markdown 表格分隔行")
		}
	}
	rows := []contextRow{}
	for n := i + 2; n < len(lines); n++ {
		row := splitRow(lines[n])
		if row == nil {
			break
		}
		if len(row) != len(header) {
			return nil, domain.Fail("CONTEXT", fmt.Sprintf("%s 第 %d 行列数错误", heading, n+1))
		}
		rows = append(rows, contextRow{row, n + 1})
	}
	return rows, nil
}

func scanContext(root string) error {
	skip := map[string]bool{".git": true, ".codegraph": true, ".template-source": true, "node_modules": true, "dist": true, "build": true}
	return filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() && rel != "." {
			if skip[d.Name()] {
				return filepath.SkipDir
			}
			if _, e := os.Lstat(filepath.Join(p, ".git")); e == nil {
				return filepath.SkipDir
			} else if !os.IsNotExist(e) {
				return e
			}
		}
		if !d.IsDir() {
			if d.Name() == "CONTEXT-MAP.md" {
				return domain.Fail("CONTEXT", "禁止 CONTEXT-MAP.md: "+rel)
			}
			if d.Name() == "context.md" {
				return domain.Fail("CONTEXT", "文件名大小写错误: "+rel)
			}
			if d.Name() == "CONTEXT.md" && rel != "CONTEXT.md" {
				return domain.Fail("CONTEXT", "禁止嵌套 CONTEXT.md: "+rel)
			}
		}
		return nil
	})
}

func contextContract(root string) (map[string]any, error) {
	if err := scanContext(root); err != nil {
		return nil, err
	}
	file, err := safefs.Path(root, "CONTEXT.md")
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	return contextContractBytes(b)
}

// Shared parser: governance sessions provide observed bytes, including source
// Context snapshots inside immutable Handoff payloads.
func contextContractBytes(b []byte) (map[string]any, error) {
	if !utf8.Valid(b) {
		return nil, domain.Fail("CONTEXT", "CONTEXT.md 必须是有效 UTF-8")
	}
	source := normalizeContext(string(b))
	if !regexp.MustCompile(`^---\ncontext_schema_version:\s*1\n---\n`).MatchString(source) {
		return nil, domain.Fail("CONTEXT", "CONTEXT.md 顶部必须声明 context_schema_version: 1")
	}
	lines := strings.Split(source, "\n")
	process, err := readTable(lines, "## 流程术语", [][]string{{"术语", "含义", "英文标识", "避免 / 备注"}})
	if err != nil {
		return nil, err
	}
	business, err := readTable(lines, "## 业务术语", [][]string{{"术语", "含义", "英文标识", "适用业务责任区", "避免 / 备注"}, {"术语", "含义", "英文标识", "适用限界上下文", "避免 / 备注"}})
	if err != nil {
		return nil, err
	}
	for _, row := range process {
		if row.cells[0] == "" || row.cells[1] == "" || row.cells[2] != "—" {
			return nil, domain.Fail("CONTEXT", fmt.Sprintf("流程术语第 %d 行必填列或英文标识非法", row.line))
		}
	}
	terms := []Term{}
	ids := map[string]bool{}
	names := map[string]bool{}
	aliases := map[string]bool{}
	for _, row := range business {
		c := row.cells
		if c[0] == "" || c[1] == "" || !pascal.MatchString(c[2]) || (c[3] != "Global" && !pascal.MatchString(c[3])) {
			return nil, domain.Fail("CONTEXT", fmt.Sprintf("业务术语第 %d 行身份或必填列非法", row.line))
		}
		t := Term{TermRef: c[3] + "/" + c[2], Term: c[0], Meaning: c[1], EnglishIdentifier: c[2], ContextID: c[3], Notes: c[4], Line: row.line, ForbiddenAliases: []string{}}
		if ids[t.TermRef] || names[t.ContextID+"/"+t.Term] {
			return nil, domain.Fail("CONTEXT", "术语身份或中文术语重复: "+t.TermRef)
		}
		ids[t.TermRef] = true
		names[t.ContextID+"/"+t.Term] = true
		if match := aliasPattern.FindStringSubmatch(t.Notes); match != nil {
			for _, alias := range regexp.MustCompile(`[、,，]`).Split(match[1], -1) {
				alias = strings.Trim(strings.TrimSpace(alias), "`")
				if alias != "" {
					key := t.ContextID + "/" + alias
					if aliases[key] {
						return nil, domain.Fail("CONTEXT", "禁用别名冲突: "+key)
					}
					aliases[key] = true
					t.ForbiddenAliases = append(t.ForbiddenAliases, alias)
				}
			}
		}
		terms = append(terms, t)
	}
	for key := range aliases {
		if names[key] {
			return nil, domain.Fail("CONTEXT", "禁用别名与稳定术语冲突: "+key)
		}
	}
	for _, global := range terms {
		if global.ContextID == "Global" {
			for _, local := range terms {
				if local.ContextID != "Global" && (global.Term == local.Term || global.EnglishIdentifier == local.EnglishIdentifier) {
					return nil, domain.Fail("CONTEXT", "Global 术语不得被局部重新定义: "+global.TermRef+" / "+local.TermRef)
				}
			}
		}
	}
	return map[string]any{"context_ref": "CONTEXT.md", "context_schema_version": 1, "document_digest": "sha256:" + safefs.Digest([]byte(source)), "process_terms": len(process), "business_terms": terms, "execution_authorization": "not-evaluated", "read_only": true}, nil
}

// Struct field order is the canonical Context snapshot wire order used by the existing contract.
type canonicalTerm struct {
	TermRef           string   `json:"term_ref"`
	Term              string   `json:"term"`
	Meaning           string   `json:"meaning"`
	EnglishIdentifier string   `json:"english_identifier"`
	ContextID         string   `json:"context_id"`
	ForbiddenAliases  []string `json:"forbidden_aliases"`
}

func termsDigest(terms []Term) (string, error) {
	items := make([]canonicalTerm, 0, len(terms))
	for _, t := range terms {
		aliases := append([]string{}, t.ForbiddenAliases...)
		sort.SliceStable(aliases, func(i, j int) bool { return utf16Less(aliases[i], aliases[j]) })
		items = append(items, canonicalTerm{t.TermRef, t.Term, t.Meaning, t.EnglishIdentifier, t.ContextID, aliases})
	}
	comparator := collate.New(language.Und)
	sort.SliceStable(items, func(i, j int) bool { return comparator.CompareString(items[i].TermRef, items[j].TermRef) < 0 })
	var out strings.Builder
	out.WriteByte('[')
	for i, t := range items {
		if i > 0 {
			out.WriteByte(',')
		}
		out.WriteByte('{')
		keys := []string{"term_ref", "term", "meaning", "english_identifier", "context_id"}
		values := []string{t.TermRef, t.Term, t.Meaning, t.EnglishIdentifier, t.ContextID}
		for n, key := range keys {
			if n > 0 {
				out.WriteByte(',')
			}
			writeContextJSONString(&out, key)
			out.WriteByte(':')
			writeContextJSONString(&out, values[n])
		}
		out.WriteString(`,"forbidden_aliases":[`)
		for n, alias := range t.ForbiddenAliases {
			if n > 0 {
				out.WriteByte(',')
			}
			writeContextJSONString(&out, alias)
		}
		out.WriteString("]}")
	}
	out.WriteByte(']')
	return "sha256:" + safefs.Digest([]byte(out.String())), nil
}

func utf16Less(a, b string) bool {
	left, right := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(left) && i < len(right); i++ {
		if left[i] != right[i] {
			return left[i] < right[i]
		}
	}
	return len(left) < len(right)
}

// JSON.stringify emits valid Unicode directly, including U+2028/U+2029. Literal escapes stay literal.
func writeContextJSONString(out *strings.Builder, s string) {
	out.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		default:
			if r < 0x20 {
				out.WriteString(fmt.Sprintf(`\u%04x`, r))
			} else {
				out.WriteRune(r)
			}
		}
	}
	out.WriteByte('"')
}

func contextRun(action, root string, args map[string]string) (any, error) {
	if action != "check" && action != "verify" && action != "query" {
		return nil, domain.Fail("UNPORTED", "Context 动作尚未迁移: "+action)
	}
	result, err := contextContract(root)
	if err != nil {
		return nil, err
	}
	if ids := args["allowed-context-ids"]; ids != "" {
		allowed := map[string]bool{"Global": true}
		for _, id := range strings.Split(ids, ",") {
			allowed[strings.TrimSpace(id)] = true
		}
		for _, term := range result["business_terms"].([]Term) {
			if !allowed[term.ContextID] {
				return nil, domain.Fail("CONTEXT", "业务责任区未在调用者的边界合同中登记: "+term.ContextID)
			}
		}
	}
	ref := args["id"]
	if ref == "" {
		ref = args["arg0"]
	}
	if action == "query" && ref != "" {
		if args["term-refs"] != "" {
			return nil, domain.Fail("INPUT", "query 的单个 ID 与 --term-refs 不能同时使用")
		}
		for _, term := range result["business_terms"].([]Term) {
			if term.TermRef == ref {
				return map[string]any{"term": term, "document_digest": result["document_digest"], "read_only": true, "execution_authorization": "not-evaluated"}, nil
			}
		}
		return nil, domain.Fail("CONTEXT_REFERENCE", "术语引用无法解析: "+ref)
	}
	if refs := args["term-refs"]; refs != "" {
		selected := []Term{}
		byID := map[string]Term{}
		for _, term := range result["business_terms"].([]Term) {
			byID[term.TermRef] = term
		}
		for _, id := range strings.Split(refs, ",") {
			id = strings.TrimSpace(id)
			term, ok := byID[id]
			if !ok {
				return nil, domain.Fail("CONTEXT_REFERENCE", "术语引用无法解析: "+id)
			}
			selected = append(selected, term)
		}
		digest, e := termsDigest(selected)
		if e != nil {
			return nil, e
		}
		result["terms"] = selected
		result["referenced_terms_digest"] = digest
	}
	snapshotRef := first(args["snapshot"], args["file"])
	if action != "query" {
		snapshotRef = first(snapshotRef, args["arg0"])
	}
	if snapshotRef != "" {
		snapshot, err := load(root, snapshotRef)
		if err != nil {
			return nil, err
		}
		version, ok := integer(snapshot["context_schema_version"])
		if !ok || version != 1 || text(snapshot["context_ref"]) != "CONTEXT.md" {
			return nil, domain.Fail("CONTEXT_SNAPSHOT", "context_snapshot 身份或版本非法")
		}
		refs, ok := snapshot["term_refs"].([]any)
		if !ok {
			return nil, domain.Fail("CONTEXT_SNAPSHOT", "context_snapshot.term_refs 必须是数组")
		}
		byID := map[string]Term{}
		for _, term := range result["business_terms"].([]Term) {
			byID[term.TermRef] = term
		}
		selected := []Term{}
		for _, ref := range refs {
			id, ok := ref.(string)
			if !ok {
				return nil, domain.Fail("CONTEXT_REFERENCE", "术语引用必须是字符串")
			}
			term, ok := byID[id]
			if !ok {
				return nil, domain.Fail("CONTEXT_REFERENCE", "术语引用无法解析: "+id)
			}
			selected = append(selected, term)
		}
		digest, err := termsDigest(selected)
		if err != nil {
			return nil, err
		}
		if text(snapshot["document_digest"]) != result["document_digest"] || text(snapshot["referenced_terms_digest"]) != digest {
			return nil, domain.Fail("CONTEXT_SNAPSHOT_STALE", "Context snapshot 与当前词汇文档或引用术语摘要不一致")
		}
		result["terms"] = selected
		result["referenced_terms_digest"] = digest
		result["snapshot_validation"] = "passed"
	}
	return result, nil
}

func first(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
