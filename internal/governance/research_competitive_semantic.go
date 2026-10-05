package governance

import (
	"path"
	"strings"
	"unicode/utf8"

	"github.com/iloveZzz/yss-cli/internal/safefs"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

const competitiveStart = "<!-- YSS-COMPETITIVE:START -->"
const competitiveEnd = "<!-- YSS-COMPETITIVE:END -->"

var competitiveSymbols = map[string]string{"supported": "✅ 明确支持", "partial": "⚠️ 部分支持", "absent": "❌ 明确不支持", "unknown": "❓ 未知"}
var competitiveTypes = map[string]string{"direct": "直接竞品", "indirect": "间接竞品", "substitute": "替代方案", "adjacent": "相邻产品", "our-product": "本方产品"}

func validateCompetitiveResearchSemantic(s *semanticSession, data map[string]any, evidenceRef, slug string, claims, evidence, searches map[string]map[string]any) ([]any, error) {
	extension, ok := object(data["competitive_analysis"])
	if !ok {
		return nil, s.reject("RESEARCH_STRUCTURE", "竞品扩展必须是对象")
	}
	schemaRef := ".agents/skills/yss-research/references/competitive-analysis.schema.json"
	if err := s.validateSchema(schemaRef, extension); err != nil {
		return nil, err
	}
	const moduleRef = ".agents/skills/yss-research/scripts/lib/competitive-analysis.mjs"
	module, err := s.bytes(moduleRef)
	if err != nil {
		return nil, err
	}
	if safefs.Digest(module) != "d52f89500b59b4bf21608154a128ceef71c40940b24a4546bc3f86e3c93ef57e" {
		return nil, s.unavailable("CAPABILITY", "本地竞品校验规则尚未迁移")
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
	for _, v := range semList(extension["assessments"]) {
		row := semMap(v)
		id, capID := text(row["competitor_id"]), text(row["capability_id"])
		pair := id + "/" + capID
		if competitors[id] == nil || capabilities[capID] == nil || pairs[pair] {
			return nil, s.reject("RESEARCH_REFERENCE", "竞品评估引用错误或重复: "+pair)
		}
		pairs[pair] = true
		for _, v := range semList(row["claim_refs"]) {
			claimRef := text(v)
			claim := claims[claimRef]
			if claim == nil {
				return nil, s.reject("RESEARCH_REFERENCE", "竞品评估引用未知 Claim")
			}
			if row["status"] == "unknown" {
				continue
			}
			if !semHas(semMap(data["audit_summary"])["audited_claim_ids"], claimRef) || !researchChoice(claim, "audit_status", "supported", "partially-supported") || !researchArray(claim, "evidence_refs", true) || claim["audit_status"] == "partially-supported" && (claim["disposition"] != "qualify" || len(semList(row["limitations"])) == 0) {
				return nil, s.reject("RESEARCH_EVIDENCE", "竞品结论缺少已审计证据")
			}
			primary := false
			for _, ref := range semList(claim["evidence_refs"]) {
				e := evidence[text(ref)]
				if e == nil || e["stance"] != "support" || e["source_level"] == "lead-only" {
					return nil, s.reject("RESEARCH_EVIDENCE", "竞品支持来源非法")
				}
				primary = primary || e["source_level"] == "primary"
				if row["status"] == "absent" && (researchChoice(e, "source_class", "search", "search-log", "search-result") || searches[text(e["source_ref"])] != nil && searches[text(e["source_ref"])]["result"] == "none-found") {
					return nil, s.reject("RESEARCH_EVIDENCE", "未搜索到不能证明竞品不支持")
				}
			}
			if claim["claim_kind"] == "technical-fact" && !primary {
				return nil, s.reject("RESEARCH_EVIDENCE", "技术竞品结论缺少一手来源")
			}
		}
		for _, key := range []string{"version", "edition", "region"} {
			val := strings.ToLower(strings.TrimSpace(text(competitors[id][key])))
			if row["status"] != "unknown" && (val == "unknown" || val == "未知") && len(semList(row["limitations"])) == 0 {
				return nil, s.reject("RESEARCH_EVIDENCE", "未知版本、套餐或地区必须限定结论")
			}
		}
	}
	for id := range competitors {
		for capID := range capabilities {
			if !pairs[id+"/"+capID] {
				return nil, s.reject("RESEARCH_COVERAGE", "竞品矩阵缺少评估组合")
			}
		}
	}
	refs := []string{}
	artifacts := semMap(extension["artifacts"])
	for _, kind := range []string{"matrix", "report"} {
		filename, exists := artifacts[kind]
		if !exists {
			continue
		}
		suffix := "matrix"
		if kind == "report" {
			suffix = "analysis"
		}
		if text(filename) != slug+"-competitive-"+suffix+".md" {
			return nil, s.reject("RESEARCH_PATH", "竞品输出须相邻且同 slug")
		}
		ref := path.Join(path.Dir(evidenceRef), text(filename))
		content, err := s.bytes(ref)
		if err != nil {
			return nil, err
		}
		body := string(content)
		start, end := strings.Index(body, competitiveStart), strings.Index(body, competitiveEnd)
		if strings.Count(body, competitiveStart) != 1 || strings.Count(body, competitiveEnd) != 1 || end < start || body[start:end+len(competitiveEnd)] != competitiveManagedBody(data, kind, claims, evidence, searches) {
			return nil, s.reject("RESEARCH_ARTIFACT_DRIFT", "竞品输出受管区与当前证据台账不一致")
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
		b, err := s.bytes(ref)
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
	ext := semMap(data["competitive_analysis"])
	competitors, capabilities, assessments := semList(ext["competitors"]), semList(ext["capabilities"]), semList(ext["assessments"])
	lines := []string{"调研截至日期：" + competitiveCell(text(ext["as_of"])), "比较范围：" + competitiveCell(text(ext["comparison_scope"])), "本区由证据台账生成；结构与引用校验不替代来源语义审核。", "", "### 比较对象与适用范围", ""}
	rows := [][]string{}
	for _, v := range competitors {
		r := semMap(v)
		rows = append(rows, []string{text(r["id"]), text(r["name"]), competitiveTypes[text(r["type"])], text(r["product"]), text(r["version"]), text(r["edition"]), text(r["region"])})
	}
	lines = append(lines, competitiveTable([]string{"竞品 ID", "竞品", "类型", "产品", "版本", "套餐", "地区"}, rows), "", "### 能力定义", "")
	rows = [][]string{}
	for _, v := range capabilities {
		r := semMap(v)
		rows = append(rows, []string{text(r["id"]), text(r["module"]), text(r["name"]), text(r["definition"]), text(r["user_value"])})
	}
	lines = append(lines, competitiveTable([]string{"能力 ID", "功能模块", "功能点", "定义", "用户价值"}, rows), "")
	if kind == "report" && ext["output_selection"] == "both" {
		file := text(semMap(ext["artifacts"])["matrix"])
		lines = append(lines, "完整功能矩阵：["+competitiveCell(file)+"](<"+competitiveEncodeURI(file)+">)", "")
	} else {
		pairs := map[string]map[string]any{}
		for _, v := range assessments {
			r := semMap(v)
			pairs[text(r["competitor_id"])+"/"+text(r["capability_id"])] = r
		}
		headers := []string{"功能模块", "能力 ID / 功能点"}
		for _, v := range competitors {
			r := semMap(v)
			headers = append(headers, text(r["id"])+" / "+text(r["name"]))
		}
		rows = [][]string{}
		for _, v := range capabilities {
			c := semMap(v)
			row := []string{text(c["module"]), text(c["id"]) + " / " + text(c["name"])}
			for _, v := range competitors {
				r := pairs[text(semMap(v)["id"])+"/"+text(c["id"])]
				refs := strings.Join(semStrings(r["claim_refs"]), ", ")
				if refs == "" {
					refs = "无（来源缺口见下表）"
				}
				row = append(row, competitiveSymbols[text(r["status"])]+"；Claim: "+refs)
			}
			rows = append(rows, row)
		}
		lines = append(lines, "### 功能比较", "", competitiveTable(headers, rows), "", "状态：✅ 明确支持；⚠️ 在注明条件下部分支持；❌ 有明确不支持证据；❓ 尚未确认。未搜索到资料不构成不支持证据。", "")
	}
	rows = [][]string{}
	for _, v := range assessments {
		r := semMap(v)
		refs := strings.Join(semStrings(r["claim_refs"]), ", ")
		if refs == "" {
			refs = "无"
		}
		limits := strings.Join(semStrings(r["limitations"]), "；")
		if limits == "" {
			limits = "无已登记限定"
		}
		gap := semMap(r["gap"])
		reason, next := text(gap["reason"]), text(gap["next_step"])
		if reason == "" {
			reason = "无已登记缺口"
		}
		if next == "" {
			next = "无"
		}
		rows = append(rows, []string{text(r["competitor_id"]) + "/" + text(r["capability_id"]), competitiveSymbols[text(r["status"])], refs, limits, reason, next})
	}
	lines = append(lines, "### 限定条件与补证计划", "", competitiveTable([]string{"竞品 / 能力", "状态", "Claim", "限定条件", "来源缺口", "补证计划"}, rows), "")
	refs := []string{}
	seen := map[string]bool{}
	for _, v := range assessments {
		for _, ref := range semStrings(semMap(v)["claim_refs"]) {
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
		rows = append(rows, []string{ref, text(c["statement"]), text(c["audit_status"]) + " / " + text(c["disposition"]), strings.Join(semStrings(c["evidence_refs"]), ", "), strings.Join(semStrings(c["counter_signal_refs"]), ", ")})
		for _, r := range append(semStrings(c["evidence_refs"]), semStrings(c["counter_signal_refs"])...) {
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
			limits := strings.Join(semStrings(e["limitations"]), "；")
			if limits == "" {
				limits = "无已登记限制"
			}
			rows = append(rows, []string{ref, text(e["source_ref"]) + " / " + text(e["locator"]), first(text(e["evidence_date"]), text(e["observed_at"])), text(e["observation"]), limits})
		} else {
			r := searches[ref]
			rows = append(rows, []string{ref, text(r["channel"]) + " / " + text(r["query_or_corpus"]), text(r["searched_at"]), text(r["result"]), "搜索结果仅记录反向检索，不能证明能力缺失"})
		}
	}
	lines = append(lines, competitiveTable([]string{"证据 / 搜索 ID", "来源与定位", "日期", "观察 / 搜索结果", "限制"}, rows))
	return competitiveStart + "\n" + strings.Join(lines, "\n") + "\n" + competitiveEnd
}
