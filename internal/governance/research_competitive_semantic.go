package governance

import (
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
	"path"
	"strings"
	"unicode/utf8"
)

const competitiveStart = "<!-- YSS-COMPETITIVE:START -->"

const competitiveEnd = "<!-- YSS-COMPETITIVE:END -->"

var competitiveSymbols = map[string]string{"supported": "✅ 明确支持", "partial": "⚠️ 部分支持", "absent": "❌ 明确不支持", "unknown": "❓ 未知"}

var competitiveTypes = map[string]string{"direct": "直接竞品", "indirect": "间接竞品", "substitute": "替代方案", "adjacent": "相邻产品", "our-product": "本方产品"}

func validateCompetitiveResearchSemantic(s *kernel.SemanticSession, data map[string]any, evidenceRef, slug string, claims, evidence, searches map[string]map[string]any) ([]any, error) {
	extension, ok := kernel.Object(data["competitive_analysis"])
	if !ok {
		return nil, s.Reject("RESEARCH_STRUCTURE", "竞品扩展必须是对象")
	}
	schemaRef := ".agents/skills/yss-research/references/competitive-analysis.schema.json"
	if err := s.ValidateSchema(schemaRef, extension); err != nil {
		return nil, err
	}
	const moduleRef = ".agents/skills/yss-research/scripts/lib/competitive-analysis.mjs"
	module, err := s.Bytes(moduleRef)
	if err != nil {
		return nil, err
	}
	if safefs.Digest(module) != "d52f89500b59b4bf21608154a128ceef71c40940b24a4546bc3f86e3c93ef57e" {
		return nil, s.Unavailable("CAPABILITY", "本地竞品校验规则尚未迁移")
	}
	competitors, err := researchIndex(s, extension["competitors"], "competitors", true)
	if err != nil {
		return nil, err
	}
	capabilities, err := researchIndex(s, extension["capabilities"], "capabilities", true)
	if err != nil {
		return nil, err
	}
	pairs := map[string]bool{}
	for _, v := range kernel.SemList(extension["assessments"]) {
		row := kernel.SemMap(v)
		id, capID := kernel.Text(row["competitor_id"]), kernel.Text(row["capability_id"])
		pair := id + "/" + capID
		if competitors[id] == nil || capabilities[capID] == nil || pairs[pair] {
			return nil, s.Reject("RESEARCH_REFERENCE", "竞品评估引用错误或重复: "+pair)
		}
		pairs[pair] = true
		for _, v := range kernel.SemList(row["claim_refs"]) {
			claimRef := kernel.Text(v)
			claim := claims[claimRef]
			if claim == nil {
				return nil, s.Reject("RESEARCH_REFERENCE", "竞品评估引用未知 Claim")
			}
			if row["status"] == "unknown" {
				continue
			}
			if !kernel.SemHas(kernel.SemMap(data["audit_summary"])["audited_claim_ids"], claimRef) || !researchChoice(claim, "audit_status", "supported", "partially-supported") || !researchArray(claim, "evidence_refs", true) || claim["audit_status"] == "partially-supported" && (claim["disposition"] != "qualify" || len(kernel.SemList(row["limitations"])) == 0) {
				return nil, s.Reject("RESEARCH_EVIDENCE", "竞品结论缺少已审计证据")
			}
			primary := false
			for _, ref := range kernel.SemList(claim["evidence_refs"]) {
				e := evidence[kernel.Text(ref)]
				if e == nil || e["stance"] != "support" || e["source_level"] == "lead-only" {
					return nil, s.Reject("RESEARCH_EVIDENCE", "竞品支持来源非法")
				}
				primary = primary || e["source_level"] == "primary"
				if row["status"] == "absent" && (researchChoice(e, "source_class", "search", "search-log", "search-result") || searches[kernel.Text(e["source_ref"])] != nil && searches[kernel.Text(e["source_ref"])]["result"] == "none-found") {
					return nil, s.Reject("RESEARCH_EVIDENCE", "未搜索到不能证明竞品不支持")
				}
			}
			if claim["claim_kind"] == "technical-fact" && !primary {
				return nil, s.Reject("RESEARCH_EVIDENCE", "技术竞品结论缺少一手来源")
			}
		}
		for _, key := range []string{"version", "edition", "region"} {
			val := strings.ToLower(strings.TrimSpace(kernel.Text(competitors[id][key])))
			if row["status"] != "unknown" && (val == "unknown" || val == "未知") && len(kernel.SemList(row["limitations"])) == 0 {
				return nil, s.Reject("RESEARCH_EVIDENCE", "未知版本、套餐或地区必须限定结论")
			}
		}
	}
	for id := range competitors {
		for capID := range capabilities {
			if !pairs[id+"/"+capID] {
				return nil, s.Reject("RESEARCH_COVERAGE", "竞品矩阵缺少评估组合")
			}
		}
	}
	refs := []string{}
	artifacts := kernel.SemMap(extension["artifacts"])
	for _, kind := range []string{"matrix", "report"} {
		filename, exists := artifacts[kind]
		if !exists {
			continue
		}
		suffix := "matrix"
		if kind == "report" {
			suffix = "analysis"
		}
		if kernel.Text(filename) != slug+"-competitive-"+suffix+".md" {
			return nil, s.Reject("RESEARCH_PATH", "竞品输出须相邻且同 slug")
		}
		ref := path.Join(path.Dir(evidenceRef), kernel.Text(filename))
		content, err := s.Bytes(ref)
		if err != nil {
			return nil, err
		}
		body := string(content)
		start, end := strings.Index(body, competitiveStart), strings.Index(body, competitiveEnd)
		if strings.Count(body, competitiveStart) != 1 || strings.Count(body, competitiveEnd) != 1 || end < start || body[start:end+len(competitiveEnd)] != competitiveManagedBody(data, kind, claims, evidence, searches) {
			return nil, s.Reject("RESEARCH_ARTIFACT_DRIFT", "竞品输出受管区与当前证据台账不一致")
		}
		refs = append(refs, ref)
	}
	refs = append(refs, schemaRef, moduleRef, ".agents/skills/yss-research/scripts/render-competitive-outputs.mjs", ".template-spec/plan/templates/competitive-matrix-template.md", ".template-spec/plan/templates/competitive-analysis-template.md", "scripts/lib/json-schema.mjs", "scripts/lib/validation-phase.mjs")
	// Matches the fixed Node oracle's English localeCompare order, with a local
	// collator so concurrent validation calls never share mutable collation state.
	collator := collate.New(language.English)
	collator.SortStrings(refs)
	bindings := []any{}
	for _, ref := range refs {
		b, err := s.Bytes(ref)
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, map[string]any{"ref": ref, "digest": safefs.Digest(b)})
	}
	return bindings, nil
}

func competitiveCell(value string) string {
	value = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\\", "\\\\", "|", "\\|", "[", "\\[", "]", "\\]").Replace(value)
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.ReplaceAll(value, "\n", "<br>")
}

func competitiveTable(headers []string, rows [][]string) string {
	line := func(v []string) string {
		out := make([]string, len(v))
		for i, x := range v {
			out[i] = competitiveCell(x)
		}
		return "| " + strings.Join(out, " | ") + " |"
	}
	lines := []string{line(headers)}
	sep := make([]string, len(headers))
	for i := range sep {
		sep[i] = "---"
	}
	lines = append(lines, line(sep))
	for _, r := range rows {
		lines = append(lines, line(r))
	}
	return strings.Join(lines, "\n")
}

// encodeURI preserves URI punctuation and percent-encodes UTF-8 bytes.
func competitiveEncodeURI(value string) string {
	const safe = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789;,/?:@&=+$-_.!~*'()#"
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for _, r := range value {
		if r < 128 && strings.ContainsRune(safe, r) {
			b.WriteRune(r)
			continue
		}
		var bytes [utf8.UTFMax]byte
		n := utf8.EncodeRune(bytes[:], r)
		for _, v := range bytes[:n] {
			b.WriteByte('%')
			b.WriteByte(hex[v>>4])
			b.WriteByte(hex[v&15])
		}
	}
	return b.String()
}

func competitiveManagedBody(data map[string]any, kind string, claims, evidence, searches map[string]map[string]any) string {
	ext := kernel.SemMap(data["competitive_analysis"])
	competitors, capabilities, assessments := kernel.SemList(ext["competitors"]), kernel.SemList(ext["capabilities"]), kernel.SemList(ext["assessments"])
	lines := []string{"调研截至日期：" + competitiveCell(kernel.Text(ext["as_of"])), "比较范围：" + competitiveCell(kernel.Text(ext["comparison_scope"])), "本区由证据台账生成；结构与引用校验不替代来源语义审核。", "", "### 比较对象与适用范围", ""}
	rows := [][]string{}
	for _, v := range competitors {
		r := kernel.SemMap(v)
		rows = append(rows, []string{kernel.Text(r["id"]), kernel.Text(r["name"]), competitiveTypes[kernel.Text(r["type"])], kernel.Text(r["product"]), kernel.Text(r["version"]), kernel.Text(r["edition"]), kernel.Text(r["region"])})
	}
	lines = append(lines, competitiveTable([]string{"竞品 ID", "竞品", "类型", "产品", "版本", "套餐", "地区"}, rows), "", "### 能力定义", "")
	rows = [][]string{}
	for _, v := range capabilities {
		r := kernel.SemMap(v)
		rows = append(rows, []string{kernel.Text(r["id"]), kernel.Text(r["module"]), kernel.Text(r["name"]), kernel.Text(r["definition"]), kernel.Text(r["user_value"])})
	}
	lines = append(lines, competitiveTable([]string{"能力 ID", "功能模块", "功能点", "定义", "用户价值"}, rows), "")
	if kind == "report" && ext["output_selection"] == "both" {
		file := kernel.Text(kernel.SemMap(ext["artifacts"])["matrix"])
		lines = append(lines, "完整功能矩阵：["+competitiveCell(file)+"](<"+competitiveEncodeURI(file)+">)", "")
	} else {
		pairs := map[string]map[string]any{}
		for _, v := range assessments {
			r := kernel.SemMap(v)
			pairs[kernel.Text(r["competitor_id"])+"/"+kernel.Text(r["capability_id"])] = r
		}
		headers := []string{"功能模块", "能力 ID / 功能点"}
		for _, v := range competitors {
			r := kernel.SemMap(v)
			headers = append(headers, kernel.Text(r["id"])+" / "+kernel.Text(r["name"]))
		}
		rows = [][]string{}
		for _, v := range capabilities {
			c := kernel.SemMap(v)
			row := []string{kernel.Text(c["module"]), kernel.Text(c["id"]) + " / " + kernel.Text(c["name"])}
			for _, v := range competitors {
				r := pairs[kernel.Text(kernel.SemMap(v)["id"])+"/"+kernel.Text(c["id"])]
				refs := strings.Join(kernel.SemStrings(r["claim_refs"]), ", ")
				if refs == "" {
					refs = "无（来源缺口见下表）"
				}
				row = append(row, competitiveSymbols[kernel.Text(r["status"])]+"；Claim: "+refs)
			}
			rows = append(rows, row)
		}
		lines = append(lines, "### 功能比较", "", competitiveTable(headers, rows), "", "状态：✅ 明确支持；⚠️ 在注明条件下部分支持；❌ 有明确不支持证据；❓ 尚未确认。未搜索到资料不构成不支持证据。", "")
	}
	rows = [][]string{}
	for _, v := range assessments {
		r := kernel.SemMap(v)
		refs := strings.Join(kernel.SemStrings(r["claim_refs"]), ", ")
		if refs == "" {
			refs = "无"
		}
		limits := strings.Join(kernel.SemStrings(r["limitations"]), "；")
		if limits == "" {
			limits = "无已登记限定"
		}
		gap := kernel.SemMap(r["gap"])
		reason, next := kernel.Text(gap["reason"]), kernel.Text(gap["next_step"])
		if reason == "" {
			reason = "无已登记缺口"
		}
		if next == "" {
			next = "无"
		}
		rows = append(rows, []string{kernel.Text(r["competitor_id"]) + "/" + kernel.Text(r["capability_id"]), competitiveSymbols[kernel.Text(r["status"])], refs, limits, reason, next})
	}
	lines = append(lines, "### 限定条件与补证计划", "", competitiveTable([]string{"竞品 / 能力", "状态", "Claim", "限定条件", "来源缺口", "补证计划"}, rows), "")
	refs := []string{}
	seen := map[string]bool{}
	for _, v := range assessments {
		for _, ref := range kernel.SemStrings(kernel.SemMap(v)["claim_refs"]) {
			if !seen[ref] {
				seen[ref] = true
				refs = append(refs, ref)
			}
		}
	}
	rows = [][]string{}
	evidenceRefs := []string{}
	seen = map[string]bool{}
	for _, ref := range refs {
		c := claims[ref]
		rows = append(rows, []string{ref, kernel.Text(c["statement"]), kernel.Text(c["audit_status"]) + " / " + kernel.Text(c["disposition"]), strings.Join(kernel.SemStrings(c["evidence_refs"]), ", "), strings.Join(kernel.SemStrings(c["counter_signal_refs"]), ", ")})
		for _, r := range append(kernel.SemStrings(c["evidence_refs"]), kernel.SemStrings(c["counter_signal_refs"])...) {
			if !seen[r] {
				seen[r] = true
				evidenceRefs = append(evidenceRefs, r)
			}
		}
	}
	lines = append(lines, "### Claim 与证据摘要", "", competitiveTable([]string{"Claim", "陈述", "审核 / 处置", "支持证据", "反证 / 反向搜索"}, rows), "")
	rows = [][]string{}
	for _, ref := range evidenceRefs {
		if e := evidence[ref]; e != nil {
			limits := strings.Join(kernel.SemStrings(e["limitations"]), "；")
			if limits == "" {
				limits = "无已登记限制"
			}
			rows = append(rows, []string{ref, kernel.Text(e["source_ref"]) + " / " + kernel.Text(e["locator"]), kernel.First(kernel.Text(e["evidence_date"]), kernel.Text(e["observed_at"])), kernel.Text(e["observation"]), limits})
		} else {
			r := searches[ref]
			rows = append(rows, []string{ref, kernel.Text(r["channel"]) + " / " + kernel.Text(r["query_or_corpus"]), kernel.Text(r["searched_at"]), kernel.Text(r["result"]), "搜索结果仅记录反向检索，不能证明能力缺失"})
		}
	}
	lines = append(lines, competitiveTable([]string{"证据 / 搜索 ID", "来源与定位", "日期", "观察 / 搜索结果", "限制"}, rows))
	return competitiveStart + "\n" + strings.Join(lines, "\n") + "\n" + competitiveEnd
}
